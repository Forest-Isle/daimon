package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// ApprovalFunc is called when a tool requires user approval.
// It should return true if approved, false if denied.
type ApprovalFunc func(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error)

// Runtime orchestrates the agent loop: context → LLM → tools → reply.
type Runtime struct {
	provider     Provider
	tools        *tool.Registry
	sessions     *session.Manager
	db           *store.DB
	cfg          config.AgentConfig
	llmCfg       config.LLMConfig
	approvalFunc ApprovalFunc
}

func NewRuntime(
	provider Provider,
	tools *tool.Registry,
	sessions *session.Manager,
	db *store.DB,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *Runtime {
	return &Runtime{
		provider: provider,
		tools:    tools,
		sessions: sessions,
		db:       db,
		cfg:      cfg,
		llmCfg:   llmCfg,
	}
}

// SetApprovalFunc sets the callback for tool approval requests.
func (r *Runtime) SetApprovalFunc(fn ApprovalFunc) {
	r.approvalFunc = fn
}

// HandleMessage processes an inbound message through the agent loop.
func (r *Runtime) HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error {
	sess, err := r.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// Add user message to session
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   msg.Text,
		CreatedAt: time.Now(),
	})

	// Agent loop — each iteration gets its own streaming message so that
	// previous text/tool-status is not overwritten by the next response.
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
		slog.Info("agent iteration", "iteration", iteration, "session", sess.ID)

		// Each iteration creates a fresh streaming message
		updater, err := ch.SendStreaming(ctx, target)
		if err != nil {
			// Fallback to non-streaming for this iteration
			return r.handleNonStreaming(ctx, ch, sess, target)
		}

		req := CompletionRequest{
			Model:     r.llmCfg.Model,
			System:    r.cfg.SystemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     r.buildToolDefs(),
			MaxTokens: r.llmCfg.MaxTokens,
		}

		stream, err := r.provider.Stream(ctx, req)
		if err != nil {
			updater.Finish("Error: " + err.Error())
			return fmt.Errorf("llm stream: %w", err)
		}

		var fullText string
		var toolCalls []ToolUseBlock
		var stopReason StopReason

		for {
			delta, err := stream.Next()
			if err != nil {
				stream.Close()
				updater.Finish("Error: " + err.Error())
				return fmt.Errorf("stream next: %w", err)
			}

			if delta.Text != "" {
				fullText += delta.Text
				updater.Update(fullText)
			}

			if delta.ToolCall != nil {
				toolCalls = append(toolCalls, *delta.ToolCall)
			}
			// Collect all tool calls from the final delta
			if delta.Done && len(delta.ToolCalls) > 0 {
				toolCalls = delta.ToolCalls
			}

			if delta.Done {
				stopReason = delta.StopReason
				break
			}
		}
		stream.Close()

		// If stop reason is tool_use but we didn't capture any tool calls from stream,
		// fall back to non-streaming to get them
		if stopReason == StopToolUse && len(toolCalls) == 0 {
			resp, err := r.provider.Complete(ctx, req)
			if err != nil {
				updater.Finish("Error: " + err.Error())
				return err
			}
			fullText = resp.Text
			toolCalls = resp.ToolCalls
		}

		// Save assistant text message
		if fullText != "" {
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   fullText,
				CreatedAt: time.Now(),
			})
		}

		// Save tool_use messages
		for _, tc := range toolCalls {
			sess.AddMessage(session.Message{
				ID:        tc.ID,
				Role:      "tool_use",
				ToolName:  tc.Name,
				ToolInput: tc.Input,
				CreatedAt: time.Now(),
			})
		}

		// If no tool calls, we're done — finalize this message
		if len(toolCalls) == 0 {
			updater.Finish(fullText)
			break
		}

		// Finalize this message with tool-call status, then proceed.
		// The approval request and final answer will be separate messages.
		statusText := "🔧 Calling tools..."
		if fullText != "" {
			statusText = fullText + "\n\n🔧 Calling tools..."
		}
		updater.Finish(statusText)

		// Execute tool calls
		for _, tc := range toolCalls {
			t, err := r.tools.Get(tc.Name)
			if err != nil {
				r.addToolResult(sess, tc.ID, "tool not found: "+tc.Name)
				continue
			}

			// Check approval — sends a separate inline-keyboard message
			if t.RequiresApproval() && r.approvalFunc != nil {
				approved, err := r.approvalFunc(ctx, ch, target, tc.Name, tc.Input)
				if err != nil || !approved {
					r.addToolResult(sess, tc.ID, "tool execution denied by user")
					session.LogToolExecution(ctx, r.db, sess.ID, tc.Name, tc.Input, "", "denied", 0)
					continue
				}
			}

			// Execute tool
			start := time.Now()
			result, err := t.Execute(ctx, []byte(tc.Input))
			duration := time.Since(start).Milliseconds()

			var output string
			status := "success"
			if err != nil {
				output = "error: " + err.Error()
				status = "error"
			} else if result.Error != "" {
				output = "error: " + result.Error
				status = "error"
			} else {
				output = result.Output
			}

			session.LogToolExecution(ctx, r.db, sess.ID, tc.Name, tc.Input, output, status, duration)
			r.addToolResult(sess, tc.ID, output)

			slog.Info("tool executed", "tool", tc.Name, "status", status, "duration_ms", duration)
		}
		// Next iteration will create a new streaming message for the LLM's follow-up.
	}

	// Persist session
	if err := r.sessions.Persist(ctx, sess); err != nil {
		slog.Error("failed to persist session", "err", err)
	}

	return nil
}

func (r *Runtime) handleNonStreaming(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget) error {
	for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
		req := CompletionRequest{
			Model:     r.llmCfg.Model,
			System:    r.cfg.SystemPrompt,
			Messages:  BuildMessages(sess),
			Tools:     r.buildToolDefs(),
			MaxTokens: r.llmCfg.MaxTokens,
		}

		resp, err := r.provider.Complete(ctx, req)
		if err != nil {
			return err
		}

		if resp.Text != "" {
			sess.AddMessage(session.Message{
				ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role:      "assistant",
				Content:   resp.Text,
				CreatedAt: time.Now(),
			})
		}

		for _, tc := range resp.ToolCalls {
			sess.AddMessage(session.Message{
				ID:        tc.ID,
				Role:      "tool_use",
				ToolName:  tc.Name,
				ToolInput: tc.Input,
				CreatedAt: time.Now(),
			})
		}

		if len(resp.ToolCalls) == 0 {
			ch.Send(ctx, channel.OutboundMessage{
				Channel:   target.Channel,
				ChannelID: target.ChannelID,
				Text:      resp.Text,
			})
			break
		}

		for _, tc := range resp.ToolCalls {
			t, err := r.tools.Get(tc.Name)
			if err != nil {
				r.addToolResult(sess, tc.ID, "tool not found: "+tc.Name)
				continue
			}

			start := time.Now()
			result, execErr := t.Execute(ctx, []byte(tc.Input))
			duration := time.Since(start).Milliseconds()

			var output, status string
			if execErr != nil {
				output = "error: " + execErr.Error()
				status = "error"
			} else if result.Error != "" {
				output = "error: " + result.Error
				status = "error"
			} else {
				output = result.Output
				status = "success"
			}

			session.LogToolExecution(ctx, r.db, sess.ID, tc.Name, tc.Input, output, status, duration)
			r.addToolResult(sess, tc.ID, output)
		}
	}

	if err := r.sessions.Persist(ctx, sess); err != nil {
		slog.Error("failed to persist session", "err", err)
	}
	return nil
}

func (r *Runtime) addToolResult(sess *session.Session, toolUseID, content string) {
	sess.AddMessage(session.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Role:      "tool_result",
		Content:   content,
		ToolName:  toolUseID, // Store tool_use ID in ToolName for tool_result messages
		CreatedAt: time.Now(),
	})
}

func (r *Runtime) buildToolDefs() []ToolDefinition {
	tools := r.tools.All()
	defs := make([]ToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}
