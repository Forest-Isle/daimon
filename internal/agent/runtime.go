package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/punkopunko/ironclaw/internal/channel"
	"github.com/punkopunko/ironclaw/internal/config"
	"github.com/punkopunko/ironclaw/internal/memory"
	"github.com/punkopunko/ironclaw/internal/session"
	"github.com/punkopunko/ironclaw/internal/skill"
	"github.com/punkopunko/ironclaw/internal/store"
	"github.com/punkopunko/ironclaw/internal/tool"
)

// ApprovalFunc is called when a tool requires user approval.
// It should return true if approved, false if denied.
type ApprovalFunc func(ctx context.Context, ch channel.Channel, target channel.MessageTarget, toolName string, input string) (bool, error)

// Runtime orchestrates the agent loop: context → LLM → tools → reply.
type Runtime struct {
	provider       Provider
	tools          *tool.Registry
	sessions       *session.Manager
	db             *store.DB
	cfg            config.AgentConfig
	llmCfg         config.LLMConfig
	approvalFunc   ApprovalFunc
	memStore       memory.Store
	skillMgr       *skill.Manager
	agentMgr       *AgentManager
	compressor     *memory.IncrementalCompressor
	memoryBaseDir  string // base directory for file-based memory storage
}

// SetMemoryStore attaches a memory.md store to the runtime.
func (r *Runtime) SetMemoryStore(s memory.Store) { r.memStore = s }

// SetMemoryBaseDir sets the base directory for file-based memory storage.
func (r *Runtime) SetMemoryBaseDir(dir string) { r.memoryBaseDir = dir }

// SetSkillManager attaches a skill manager to the runtime.
func (r *Runtime) SetSkillManager(m *skill.Manager) { r.skillMgr = m }

// SetAgentManager attaches an agent manager to the runtime.
func (r *Runtime) SetAgentManager(m *AgentManager) { r.agentMgr = m }

// SetCompressor attaches an incremental compressor to the runtime.
func (r *Runtime) SetCompressor(c *memory.IncrementalCompressor) { r.compressor = c }

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

	// Build system prompt, augmented with relevant memories if available
	systemPrompt := r.buildSystemPrompt(ctx, msg.Text)

	// Compact history if it has grown too large, replacing old messages with a summary
	if err := CompactHistory(ctx, r.provider, sess, r.llmCfg.Model); err != nil {
		slog.Warn("history compaction failed", "session", sess.ID, "err", err)
	}

	// Agent loop — each iteration gets its own streaming message so that
	// previous text/tool-status is not overwritten by the next response.
	target := channel.MessageTarget{Channel: msg.Channel, ChannelID: msg.ChannelID}

	for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
		slog.Info("agent iteration", "iteration", iteration, "session", sess.ID)

		// Each iteration creates a fresh streaming message
		updater, err := ch.SendStreaming(ctx, target)
		if err != nil {
			// Fallback to non-streaming for this iteration
			return r.handleNonStreaming(ctx, ch, sess, target, systemPrompt)
		}

		req := CompletionRequest{
			Model:     r.llmCfg.Model,
			System:    systemPrompt,
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
				// Compress long tool outputs
				if r.compressor != nil {
					output = r.compressor.CompressToolResult(output)
				}
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

	// Save user message to memory.md for future retrieval
	if r.memStore != nil {
		if err := r.memStore.Save(ctx, memory.Entry{
			SessionID: sess.ID,
			Content:   msg.Text,
			Metadata:  map[string]string{"role": "user", "channel": msg.Channel},
			CreatedAt: time.Now(),
		}); err != nil {
			slog.Warn("failed to save memory.md", "err", err)
		}
	}

	return nil
}

func (r *Runtime) handleNonStreaming(ctx context.Context, ch channel.Channel, sess *session.Session, target channel.MessageTarget, systemPrompt string) error {
	for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
		req := CompletionRequest{
			Model:     r.llmCfg.Model,
			System:    systemPrompt,
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

// buildSystemPrompt returns the system prompt, structured as:
// Personality → core system prompt → persistent rules → memories → skills.
func (r *Runtime) buildSystemPrompt(ctx context.Context, userText string) string {
	var sb strings.Builder

	// 1. Personality (Soul.md)
	if r.cfg.Personality != "" {
		sb.WriteString("## Personality\n")
		sb.WriteString(r.cfg.Personality)
		sb.WriteString("\n\n")
	}

	// 2. Core system prompt (Agent.md + YAML system_prompt)
	sb.WriteString(r.cfg.SystemPrompt)

	// 3. Persistent rules (Memory.md)
	if r.cfg.PersistentRules != "" {
		sb.WriteString("\n\n## Rules\n")
		sb.WriteString(r.cfg.PersistentRules)
	}

	// 4. Relevant memories (runtime retrieval)
	if r.memStore != nil {
		results, err := r.memStore.Search(ctx, memory.SearchQuery{Text: userText, Limit: 5})
		if err != nil {
			slog.Warn("memory.md search failed", "err", err)
		} else if len(results) > 0 {
			sb.WriteString("\n\n## Relevant memories\n")
			for _, res := range results {
				sb.WriteString("- ")
				sb.WriteString(res.Entry.Content)
				sb.WriteString("\n")
			}
		}
	}

	// 5. User profile (loaded from memory base dir)
	if r.memoryBaseDir != "" {
		// Attempt to load user profile — userID is not available in simple mode,
		// so we use a default. The cognitive agent has proper user tracking.
		profileContent, err := memory.LoadUserProfile(r.memoryBaseDir, "default")
		if err == nil && profileContent != "" {
			sb.WriteString("\n\n## User Context\n")
			sb.WriteString(profileContent)
		}
	}

	// 6. Skills
	if r.skillMgr != nil {
		if section := r.skillMgr.BuildPromptSection(userText); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
			slog.Debug("skills injected into system prompt", "user_text_len", len(userText))
		}
	}

	// 7. Available agents
	if r.agentMgr != nil {
		if section := r.agentMgr.BuildPromptSection(); section != "" {
			sb.WriteString("\n\n")
			sb.WriteString(section)
		}
	}

	return sb.String()
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
