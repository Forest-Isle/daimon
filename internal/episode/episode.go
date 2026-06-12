package episode

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/world"
)

const (
	episodeCloseToolName = "episode_close"
	defaultMaxIterations = 20
)

// Outcome is the structured exit contract a model declares via episode_close.
type Outcome struct {
	Status       string           `json:"status"`
	Summary      string           `json:"summary"`
	WorldWrites  []world.Mutation `json:"world_writes,omitempty"`
	Receipts     []string         `json:"receipts,omitempty"`
	FollowUps    []FollowUp       `json:"follow_ups,omitempty"`
	OpenQuestion *string          `json:"open_question,omitempty"`
}

// FollowUp describes future work to plant after an episode closes.
type FollowUp struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Goal   string `json:"goal"`
}

// Runner is the cognitive kernel: it composes context from the world model,
// runs a bare ReAct loop, and enforces the episode_close exit contract. Tool
// execution and replay recording are delegated to the agent runtime via the
// CognitiveRequest, so episodes share the same governance as the legacy path.
type Runner struct {
	provider agent.Provider
	world    *world.Store
	identity *world.Identity
	bus      agent.EventBus
}

// NewRunner builds a cognitive kernel. bus may be nil (events are then skipped).
func NewRunner(p agent.Provider, ws *world.Store, id *world.Identity, bus agent.EventBus) *Runner {
	return &Runner{provider: p, world: ws, identity: id, bus: bus}
}

// Execute implements agent.CognitiveKernel.
func (r *Runner) Execute(ctx context.Context, req agent.CognitiveRequest) (agent.CognitiveOutcome, error) {
	if r == nil || r.provider == nil {
		return agent.CognitiveOutcome{Status: "failed", Summary: "episode provider unavailable"}, errors.New("episode provider unavailable")
	}

	episodeID := newEpisodeID()
	system := composeSystem(ctx, req, r.world, r.identity)
	messages := append([]agent.CompletionMessage(nil), req.Transcript...)
	if len(messages) == 0 {
		messages = []agent.CompletionMessage{{Role: "user", Content: req.Trigger}}
	}
	toolDefs := append(append([]agent.ToolDefinition(nil), req.ToolDefs...), episodeCloseToolDefinition())

	var transcript []transcriptTurn
	var lastReply string
	closeReminderSent := false

	for iteration := 0; iteration < defaultMaxIterations; iteration++ {
		creq := agent.CompletionRequest{
			Model:    req.Model,
			System:   system,
			Messages: messages,
			Tools:    toolDefs,
		}
		start := time.Now()
		fullText, toolCalls, stopReason, streamErr := streamCompletion(ctx, r.provider, creq)
		r.publishExchange(req, iteration, system, messages, fullText, toolCalls, stopReason, time.Since(start).Milliseconds())
		if streamErr != nil {
			return r.fail(req, "episode stream error: "+streamErr.Error()), streamErr
		}

		if fullText != "" {
			lastReply = fullText
		}
		if fullText != "" || len(toolCalls) > 0 {
			messages = append(messages, agent.CompletionMessage{Role: "assistant", Content: fullText, ToolBlocks: toolCalls})
			transcript = append(transcript, transcriptTurn{Role: "assistant", Content: fullText})
		}

		if len(toolCalls) == 0 {
			if iteration == defaultMaxIterations-1 || closeReminderSent {
				break
			}
			reminder := "You must call `episode_close` with a complete Outcome before finishing. Until you call it, the system will treat your work as incomplete."
			messages = append(messages, agent.CompletionMessage{Role: "user", Content: reminder})
			closeReminderSent = true
			continue
		}

		for _, tc := range toolCalls {
			if tc.Name == episodeCloseToolName {
				out, perr := parseOutcome(tc.Input)
				if perr != nil {
					rejection := "episode_close rejected: " + perr.Error()
					messages = append(messages, agent.CompletionMessage{Role: "user", Content: rejection, ToolUseID: tc.ID})
					transcript = append(transcript, transcriptTurn{Role: "tool", Content: rejection})
					continue
				}
				return r.close(ctx, req, episodeID, out, lastReply), nil
			}

			output, _ := req.Invoke(ctx, iteration, tc)
			messages = append(messages, agent.CompletionMessage{Role: "user", Content: output, ToolUseID: tc.ID})
			transcript = append(transcript, transcriptTurn{Role: "tool", Content: output})
		}

		if iteration == defaultMaxIterations-1 {
			break
		}
	}

	out := r.salvage(ctx, req, system, messages, transcript)
	return r.close(ctx, req, episodeID, out, lastReply), nil
}

// close applies the outcome's world writes plus a journal entry, records the
// turn-close event, and shapes the user-facing reply.
func (r *Runner) close(ctx context.Context, req agent.CognitiveRequest, episodeID string, out Outcome, lastReply string) agent.CognitiveOutcome {
	if r.world != nil {
		if err := r.world.ApplyOutcome(ctx, episodeID, out.WorldWrites, out.Summary); err != nil {
			return r.fail(req, "world write failed: "+err.Error())
		}
	}
	reply := strings.TrimSpace(lastReply)
	if reply == "" {
		reply = out.Summary
	}
	r.publishTurnClosed(req.SessionID, reply)
	return agent.CognitiveOutcome{Status: out.Status, Reply: reply, Summary: out.Summary}
}

func (r *Runner) fail(req agent.CognitiveRequest, summary string) agent.CognitiveOutcome {
	r.publishTurnClosed(req.SessionID, "")
	return agent.CognitiveOutcome{Status: "failed", Summary: truncateRunes(strings.TrimSpace(summary), 500)}
}

func streamCompletion(ctx context.Context, p agent.Provider, req agent.CompletionRequest) (string, []agent.ToolUseBlock, string, error) {
	stream, err := p.Stream(ctx, req)
	if err != nil {
		return "", nil, "", err
	}
	defer stream.Close()

	var fullText strings.Builder
	var toolCalls []agent.ToolUseBlock
	stopReason := ""
	for {
		delta, err := stream.Next()
		if err != nil {
			return fullText.String(), toolCalls, stopReason, err
		}
		if delta.Text != "" {
			fullText.WriteString(delta.Text)
		}
		if delta.ToolCall != nil {
			toolCalls = append(toolCalls, *delta.ToolCall)
		}
		if delta.Done {
			if len(delta.ToolCalls) > 0 {
				toolCalls = delta.ToolCalls
			}
			stopReason = string(delta.StopReason)
			return fullText.String(), toolCalls, stopReason, nil
		}
	}
}

func parseOutcome(raw string) (Outcome, error) {
	var out Outcome
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Outcome{}, fmt.Errorf("decode outcome: %w", err)
	}
	if strings.TrimSpace(out.Status) == "" {
		return Outcome{}, errors.New("status is required")
	}
	if utf8.RuneCountInString(out.Summary) > 500 {
		return Outcome{}, fmt.Errorf("summary is %d chars, max 500", utf8.RuneCountInString(out.Summary))
	}
	return out, nil
}

type transcriptTurn struct {
	Role    string
	Content string
}

// salvage recovers an Outcome when the model never called episode_close: it
// first asks the provider for a JSON-only extraction, then falls back to a
// transcript heuristic.
func (r *Runner) salvage(ctx context.Context, req agent.CognitiveRequest, system string, messages []agent.CompletionMessage, transcript []transcriptTurn) Outcome {
	prompt := "Extract a compact JSON episode Outcome from the transcript. Return only JSON with fields: status, summary, world_writes, receipts, follow_ups, open_question. Status must be one of done, blocked, handed_off. Summary must be <=500 chars."
	salvageMessages := append(append([]agent.CompletionMessage(nil), messages...), agent.CompletionMessage{Role: "user", Content: prompt})
	resp, err := r.provider.Complete(ctx, agent.CompletionRequest{
		Model:          req.Model,
		System:         system,
		Messages:       salvageMessages,
		MaxTokens:      1024,
		ToolChoice:     "none",
		ResponseFormat: &agent.ResponseFormat{Type: "json_object"},
	})
	if err == nil && resp != nil {
		if out, perr := parseOutcome(resp.Text); perr == nil {
			return out
		}
	}
	return inferOutcomeFromTranscript(req.Goal, transcript)
}

func inferOutcomeFromTranscript(goal string, transcript []transcriptTurn) Outcome {
	var lines []string
	for _, turn := range transcript {
		if strings.TrimSpace(turn.Content) == "" {
			continue
		}
		lines = append(lines, turn.Role+": "+strings.TrimSpace(turn.Content))
	}
	text := strings.Join(lines, "\n")

	status := "done"
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "blocked") || strings.Contains(lower, "waiting") || strings.Contains(lower, "need"):
		status = "blocked"
	case strings.Contains(lower, "handed off") || strings.Contains(lower, "handoff"):
		status = "handed_off"
	case strings.TrimSpace(text) == "":
		status = "blocked"
	}

	summary := strings.TrimSpace(text)
	if summary == "" {
		summary = "Episode ended without a compliant episode_close call."
	}
	if goal != "" && !strings.Contains(summary, goal) {
		summary = "Goal: " + goal + "\n" + summary
	}
	return Outcome{Status: status, Summary: truncateRunes(compactWhitespace(summary), 500)}
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (r *Runner) publishExchange(req agent.CognitiveRequest, iteration int, system string, messages []agent.CompletionMessage, responseText string, toolCalls []agent.ToolUseBlock, stopReason string, durationMs int64) {
	if r.bus == nil {
		return
	}
	r.bus.Publish(agent.ProviderExchange{
		SessionID:     req.SessionID,
		Iteration:     iteration,
		Model:         req.Model,
		Provider:      req.Provider,
		SystemPrompt:  system,
		MessagesJSON:  marshalRaw(messages),
		ResponseText:  responseText,
		ToolCallsJSON: marshalRaw(toolCalls),
		StopReason:    stopReason,
		DurationMs:    durationMs,
	})
}

func (r *Runner) publishTurnClosed(sessionID, reply string) {
	if r.bus == nil {
		return
	}
	r.bus.Publish(agent.TurnClosed{SessionID: sessionID, FinalReply: reply})
}

func marshalRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

// newEpisodeID returns a lexicographically sortable, time-prefixed identifier.
func newEpisodeID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	ms := uint64(time.Now().UnixMilli())
	return fmt.Sprintf("ep_%012x%x", ms&0xffffffffffff, binary.BigEndian.Uint64(b[:8]))
}

func episodeCloseToolDefinition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        episodeCloseToolName,
		Description: "Close the current episode with the complete structured Outcome. This is mandatory before finishing.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"done", "blocked", "handed_off"},
					"description": "Episode result status: done, blocked, or handed_off.",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Concise journal summary of what happened, 500 characters or fewer.",
				},
				"world_writes": map[string]any{
					"type":        "array",
					"description": "World model mutations to persist from this episode.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"op":     map[string]any{"type": "string", "description": "Mutation op: commitment.create, commitment.update, or journal.append."},
							"target": map[string]any{"type": "string", "description": "Optional mutation target ID."},
							"body":   map[string]any{"type": "object", "description": "Mutation body."},
						},
					},
				},
				"receipts": map[string]any{
					"type":        "array",
					"description": "Episode-level action receipt IDs produced by tools.",
					"items":       map[string]any{"type": "string"},
				},
				"follow_ups": map[string]any{
					"type":        "array",
					"description": "Timers, watches, or checks to create after this episode.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind":   map[string]any{"type": "string", "enum": []string{"timer", "watch", "check"}},
							"detail": map[string]any{"type": "string", "description": "Timer interval, watch target, or check detail."},
							"goal":   map[string]any{"type": "string", "description": "Goal for the follow-up episode."},
						},
					},
				},
				"open_question": map[string]any{
					"type":        "string",
					"description": "If blocked, the exact question or missing input needed from the user.",
				},
			},
			"required": []string{"status", "summary"},
		},
	}
}
