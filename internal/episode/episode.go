package episode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/world"
)

const (
	episodeCloseToolName = "episode_close"
	defaultMaxIterations = 20
)

// State describes one cognitive episode execution.
type State struct {
	ID        string
	Goal      string
	Trigger   string
	CreatedAt time.Time
	Budget    Budget
}

// Budget constrains a single episode run.
type Budget struct {
	MaxIterations int
	MaxTokens     int
	Deadline      time.Duration
}

// Outcome is the structured exit contract for an episode.
type Outcome struct {
	Status       string           `json:"status"`
	Summary      string           `json:"summary"`
	WorldWrites  []world.Mutation `json:"world_writes,omitempty"`
	Receipts     []string         `json:"receipts,omitempty"`
	FollowUps    []FollowUp       `json:"follow_ups,omitempty"`
	OpenQuestion *string          `json:"open_question,omitempty"`
	Salvaged     bool             `json:"salvaged,omitempty"`
}

// FollowUp describes future work to plant after an episode closes.
type FollowUp struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Goal   string `json:"goal"`
}

// Runner executes the bare episode ReAct loop.
type Runner struct {
	provider agent.Provider
	tools    *tool.Registry
	world    *world.Store
	identity *world.Identity
}

// NewRunner creates a Runner for the episode subsystem.
func NewRunner(p agent.Provider, tr *tool.Registry, ws *world.Store, id *world.Identity) *Runner {
	if tr == nil {
		tr = tool.NewRegistry()
	}
	return &Runner{
		provider: p,
		tools:    tr,
		world:    ws,
		identity: id,
	}
}

// Run executes an episode until the model calls episode_close or the runner
// salvages a non-compliant exit.
func (r *Runner) Run(ctx context.Context, ep State) (Outcome, error) {
	if r == nil || r.provider == nil {
		return failedOutcome("episode provider unavailable"), fmt.Errorf("episode provider unavailable")
	}
	if ep.Budget.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ep.Budget.Deadline)
		defer cancel()
	}

	system, messages := composePromptWithTools(ctx, ep, r.world, r.identity, r.tools)
	maxIter := ep.Budget.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}

	var transcript []transcriptTurn
	closeReminderSent := false

	for iteration := 0; iteration < maxIter; iteration++ {
		req := agent.CompletionRequest{
			System:    system,
			Messages:  messages,
			Tools:     r.toolDefinitions(),
			MaxTokens: ep.Budget.MaxTokens,
		}
		fullText, toolCalls, streamErr := streamCompletion(ctx, r.provider, req)
		if streamErr != nil {
			out := failedOutcome("episode stream error: " + streamErr.Error())
			transcript = append(transcript, transcriptTurn{Role: "error", Content: streamErr.Error()})
			return out, streamErr
		}

		if fullText != "" || len(toolCalls) > 0 {
			messages = append(messages, agent.CompletionMessage{
				Role:       "assistant",
				Content:    fullText,
				ToolBlocks: toolCalls,
			})
			transcript = append(transcript, transcriptTurn{Role: "assistant", Content: fullText, Tools: toolCalls})
		}

		if len(toolCalls) == 0 {
			if iteration == maxIter-1 {
				out := r.salvage(ctx, ep, system, messages, transcript)
				return out, nil
			}
			if !closeReminderSent {
				reminder := "You must call `episode_close` with a complete Outcome before finishing. Until you call it, the system will treat your work as incomplete."
				messages = append(messages, agent.CompletionMessage{Role: "user", Content: reminder})
				transcript = append(transcript, transcriptTurn{Role: "user", Content: reminder})
				closeReminderSent = true
			}
			continue
		}

		for _, tc := range toolCalls {
			if tc.Name == episodeCloseToolName {
				out, err := parseOutcome(tc.Input)
				if err != nil {
					result := "episode_close rejected: " + err.Error()
					messages = append(messages, agent.CompletionMessage{Role: "user", Content: result, ToolUseID: tc.ID})
					transcript = append(transcript, transcriptTurn{Role: "tool", Content: result, ToolID: tc.ID})
					continue
				}
				out.Salvaged = false
				if r.world != nil {
					if err := r.world.ApplyOutcome(ctx, ep.ID, out.WorldWrites, out.Summary); err != nil {
						return out, err
					}
				}
				return out, nil
			}

			result := r.dispatchTool(ctx, tc)
			messages = append(messages, agent.CompletionMessage{Role: "user", Content: result, ToolUseID: tc.ID})
			transcript = append(transcript, transcriptTurn{Role: "tool", Content: result, ToolID: tc.ID})
		}

		if iteration == maxIter-1 {
			out := r.salvage(ctx, ep, system, messages, transcript)
			return out, nil
		}
	}

	out := r.salvage(ctx, ep, system, messages, transcript)
	return out, nil
}

func (r *Runner) dispatchTool(ctx context.Context, tc agent.ToolUseBlock) string {
	if r == nil || r.tools == nil {
		return "tool registry unavailable"
	}
	t, err := r.tools.Get(tc.Name)
	if err != nil {
		return err.Error()
	}
	result, err := t.Execute(ctx, []byte(tc.Input))
	if err != nil {
		return err.Error()
	}
	if result.Error != "" {
		return result.Error
	}
	return result.Output
}

func streamCompletion(ctx context.Context, p agent.Provider, req agent.CompletionRequest) (string, []agent.ToolUseBlock, error) {
	stream, err := p.Stream(ctx, req)
	if err != nil {
		return "", nil, err
	}
	defer stream.Close()

	var fullText strings.Builder
	var toolCalls []agent.ToolUseBlock
	for {
		delta, err := stream.Next()
		if err != nil {
			return fullText.String(), toolCalls, err
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
			return fullText.String(), toolCalls, nil
		}
	}
}

func parseOutcome(raw string) (Outcome, error) {
	var out Outcome
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Outcome{}, fmt.Errorf("decode outcome: %w", err)
	}
	if err := validateOutcome(out); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

func validateOutcome(out Outcome) error {
	if strings.TrimSpace(out.Status) == "" {
		return errors.New("status is required")
	}
	if utf8.RuneCountInString(out.Summary) > 500 {
		return fmt.Errorf("summary is %d chars, max 500", utf8.RuneCountInString(out.Summary))
	}
	return nil
}

func failedOutcome(summary string) Outcome {
	return Outcome{
		Status:   "failed",
		Summary:  truncateRunes(strings.TrimSpace(summary), 500),
		Salvaged: true,
	}
}

type transcriptTurn struct {
	Role    string
	Content string
	ToolID  string
	Tools   []agent.ToolUseBlock
}

func (r *Runner) salvage(ctx context.Context, ep State, system string, messages []agent.CompletionMessage, transcript []transcriptTurn) Outcome {
	prompt := "Extract a compact JSON episode Outcome from the transcript. Return only JSON with fields: status, summary, world_writes, receipts, follow_ups, open_question. Status must be one of done, blocked, handed_off. Summary must be <=500 chars."
	salvageMessages := append(append([]agent.CompletionMessage(nil), messages...), agent.CompletionMessage{
		Role:    "user",
		Content: prompt,
	})
	resp, err := r.provider.Complete(ctx, agent.CompletionRequest{
		System:         system,
		Messages:       salvageMessages,
		Tools:          []agent.ToolDefinition{episodeCloseToolDefinition()},
		MaxTokens:      1024,
		ToolChoice:     "none",
		ResponseFormat: &agent.ResponseFormat{Type: "json_object"},
	})
	if err == nil && resp != nil {
		if out, parseErr := parseOutcome(resp.Text); parseErr == nil {
			out.Salvaged = true
			return out
		}
	}

	out := inferOutcomeFromTranscript(ep, transcript)
	out.Salvaged = true
	return out
}

func inferOutcomeFromTranscript(ep State, transcript []transcriptTurn) Outcome {
	text := transcriptText(transcript)
	status := "done"
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "blocked") || strings.Contains(lower, "waiting") || strings.Contains(lower, "need"):
		status = "blocked"
	case strings.Contains(lower, "handed_off") || strings.Contains(lower, "handed off") || strings.Contains(lower, "handoff"):
		status = "handed_off"
	case strings.TrimSpace(text) == "":
		status = "blocked"
	}

	summary := summarizeTranscript(ep, text)
	return Outcome{Status: status, Summary: summary}
}

func transcriptText(transcript []transcriptTurn) string {
	var lines []string
	for _, turn := range transcript {
		if strings.TrimSpace(turn.Content) == "" {
			continue
		}
		lines = append(lines, turn.Role+": "+strings.TrimSpace(turn.Content))
	}
	return strings.Join(lines, "\n")
}

func summarizeTranscript(ep State, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "Episode ended without a compliant episode_close call."
	}
	if ep.Goal != "" && !strings.Contains(text, ep.Goal) {
		text = "Goal: " + ep.Goal + "\n" + text
	}
	return truncateRunes(compactWhitespace(text), 500)
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (r *Runner) toolDefinitions() []agent.ToolDefinition {
	defs := make([]agent.ToolDefinition, 0)
	if r != nil && r.tools != nil {
		registered := r.tools.All()
		sort.Slice(registered, func(i, j int) bool { return registered[i].Name() < registered[j].Name() })
		for _, t := range registered {
			if t.Name() == episodeCloseToolName {
				continue
			}
			defs = append(defs, agent.ToolDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
	}
	defs = append(defs, episodeCloseToolDefinition())
	return defs
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
							"op":     map[string]any{"type": "string", "description": "Mutation op, such as commitment.create, commitment.update, or journal.append."},
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
