package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// StreamingPlanner wraps the existing Planner with streaming LLM output.
type StreamingPlanner struct {
	inner *Planner
}

func NewStreamingPlanner(inner *Planner) *StreamingPlanner {
	return &StreamingPlanner{inner: inner}
}

// Stream runs PLAN using provider.Stream() for incremental subtask extraction.
func (sp *StreamingPlanner) Stream(
	ctx context.Context,
	state *CognitiveState,
	contextCh <-chan *ContextChunk,
	out chan<- *SubTask,
	streamOut chan<- string,
) (*TaskPlan, error) {
	if state == nil {
		return nil, fmt.Errorf("nil cognitive state")
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	var extraContext strings.Builder
collectContext:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-contextCh:
			if !ok {
				break collectContext
			}
			if chunk == nil || strings.TrimSpace(chunk.Content) == "" {
				continue
			}
			extraContext.WriteString("\n[")
			extraContext.WriteString(strings.ToUpper(chunk.Source))
			extraContext.WriteString("]\n")
			extraContext.WriteString(chunk.Content)
			extraContext.WriteString("\n")
		case <-timer.C:
			break collectContext
		}
	}

	userMsg := buildPlanUserMessage(state, sp.inner.tools)
	if extra := strings.TrimSpace(extraContext.String()); extra != "" {
		userMsg += "\n\nSTREAMING CONTEXT:\n" + extra
	}

	maxTokens := sp.inner.cfg.PlanMaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	system := PlanSystemPrompt
	if state.PersistentRules != "" {
		system += "\n\nADDITIONAL RULES (must follow):\n" + state.PersistentRules
	}

	model := sp.inner.llmModel
	if state.ModelOverride != "" {
		model = state.ModelOverride
	}
	if state.MaxTokensOverride > 0 {
		maxTokens = state.MaxTokensOverride
	}

	stream, err := sp.inner.provider.Stream(ctx, CompletionRequest{
		Model:     model,
		System:    system,
		Messages:  []CompletionMessage{{Role: "user", Content: userMsg}},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("plan llm stream: %w", err)
	}
	defer stream.Close()

	parser := NewIncrementalJSONParser()
	var (
		fullText   strings.Builder
		toolCalls  []ToolUseBlock
		subtasks   []*SubTask
		seen       = map[string]bool{}
		parsedPlan *TaskPlan
	)

	handleRaw := func(raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		var single subTaskJSON
		if err := json.Unmarshal(raw, &single); err == nil && single.ID != "" {
			if seen[single.ID] {
				return
			}
			st := &SubTask{
				ID:          single.ID,
				Description: single.Description,
				ToolName:    single.ToolName,
				ToolInput:   single.ToolInput,
				DependsOn:   single.DependsOn,
				Confidence:  single.Confidence,
				Status:      SubTaskPending,
			}
			seen[st.ID] = true
			subtasks = append(subtasks, st)
			select {
			case <-ctx.Done():
			case out <- st:
			}
			return
		}

		var pj planJSON
		if err := json.Unmarshal(raw, &pj); err == nil {
			parsedPlan = planJSONToTaskPlan(pj)
			for _, st := range parsedPlan.SubTasks {
				if seen[st.ID] {
					continue
				}
				seen[st.ID] = true
				subtasks = append(subtasks, st)
				select {
				case <-ctx.Done():
				case out <- st:
				}
			}
		}
	}

	for {
		delta, err := stream.Next()
		if err != nil {
			return nil, fmt.Errorf("plan stream next: %w", err)
		}

		if delta.Text != "" {
			fullText.WriteString(delta.Text)
			parser.Feed(delta.Text)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case streamOut <- "[PLAN] " + delta.Text:
			default:
			}
			for _, raw := range parser.ExtractCompleteObjects() {
				handleRaw(raw)
			}
		}

		if len(delta.ToolCalls) > 0 {
			toolCalls = append(toolCalls, delta.ToolCalls...)
		}
		if delta.ToolCall != nil {
			toolCalls = append(toolCalls, *delta.ToolCall)
		}
		if delta.Done {
			break
		}
	}

	for _, raw := range parser.Finalize() {
		handleRaw(raw)
	}

	plan := parsedPlan
	if plan == nil {
		parsed, parseErr := parsePlanResponse(fullText.String())
		if parseErr == nil {
			plan = parsed
		} else if len(toolCalls) > 0 {
			slog.Info("streaming plan: tool call blocks received, using direct reply fallback", "tool_calls", len(toolCalls))
		}
	}
	if plan == nil {
		plan = &TaskPlan{
			Summary:           "Streaming plan",
			SubTasks:          subtasks,
			OverallConfidence: 1 - state.Goal.AmbiguityScore,
		}
	}
	if plan.OverallConfidence == 0 {
		plan.OverallConfidence = 1 - state.Goal.AmbiguityScore
	}
	if len(plan.SubTasks) == 0 && len(subtasks) > 0 {
		plan.SubTasks = subtasks
	}

	for _, st := range plan.SubTasks {
		if st.ToolName == "" {
			continue
		}
		if _, err := sp.inner.tools.Get(st.ToolName); err != nil {
			slog.Warn("streaming plan: unknown tool in subtask, clearing", "task", st.ID, "tool", st.ToolName)
			st.ToolName = ""
			st.ToolInput = ""
		}
	}
	if len(plan.SubTasks) > 0 {
		if err := validateDAG(plan.SubTasks); err != nil {
			return &TaskPlan{
				Summary:           "Direct reply (plan had cyclic dependencies)",
				DirectReply:       "I was unable to build a valid execution plan. " + state.UserMessage,
				OverallConfidence: 0.3,
			}, nil
		}
	}

	return plan, nil
}
