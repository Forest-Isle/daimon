package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// ReplayEventType classifies agent replay events.
type ReplayEventType string

const (
	EventSessionStart    ReplayEventType = "session_start"
	EventSessionEnd      ReplayEventType = "session_end"
	EventPhaseStart      ReplayEventType = "phase_start"
	EventPhaseEnd        ReplayEventType = "phase_end"
	EventLLMRequest      ReplayEventType = "llm_request"
	EventLLMResponse     ReplayEventType = "llm_response"
	EventToolStart       ReplayEventType = "tool_start"
	EventToolEnd         ReplayEventType = "tool_end"
	EventPlanGenerated   ReplayEventType = "plan_generated"
	EventObservationDone ReplayEventType = "observation_done"
	EventReflectionDone  ReplayEventType = "reflection_done"
	EventReplanStart     ReplayEventType = "replan_start"
	EventError           ReplayEventType = "error"
)

// ReplayEvent is a single recorded event.
type ReplayEvent struct {
	ID         string          `json:"id"`
	ReplayID   string          `json:"replay_id"`
	Sequence   int64           `json:"sequence"`
	EventType  ReplayEventType `json:"event_type"`
	Timestamp  time.Time       `json:"timestamp"`
	DurationMs *int64          `json:"duration_ms,omitempty"`
	Data       []byte          `json:"data"`
}

// Replay is the header record for one agent execution recording.
type Replay struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"session_id"`
	AgentMode    string     `json:"agent_mode"`
	Status       string     `json:"status"`
	Model        string     `json:"model"`
	MessageCount int        `json:"message_count"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	Metadata     []byte     `json:"metadata,omitempty"`
}

// ReplayStore persists and retrieves replays.
type ReplayStore interface {
	CreateReplay(ctx context.Context, sessionID, agentMode, model string) (string, error)
	AppendEvent(ctx context.Context, replayID string, event *ReplayEvent) error
	CompleteReplay(ctx context.Context, replayID, status string, messageCount int) error
	LoadReplay(ctx context.Context, replayID string) (*Replay, error)
	LoadEvents(ctx context.Context, replayID string, offset, limit int) ([]ReplayEvent, error)
	ListReplays(ctx context.Context, sessionID string, offset, limit int) ([]Replay, error)
	DeleteReplay(ctx context.Context, replayID string) error
}

// ReplayRecorder is the optional recording attachment. All methods are nil-safe.
type ReplayRecorder struct {
	store   ReplayStore
	seq     atomic.Int64
	onEvent func(event *ReplayEvent) // for testing
}

func NewReplayRecorder(store ReplayStore) *ReplayRecorder {
	if store == nil {
		return nil
	}
	return &ReplayRecorder{store: store}
}

func (r *ReplayRecorder) RecordSessionStart(ctx context.Context, sessionID, channel, agentMode, model string) string {
	if r == nil || r.store == nil {
		return ""
	}

	replayID, err := r.store.CreateReplay(ctx, sessionID, agentMode, model)
	if err != nil {
		slog.Warn("replay: create replay failed", "session_id", sessionID, "err", err)
		return ""
	}

	r.record(ctx, replayID, EventSessionStart, nil, map[string]any{
		"session_id": sessionID,
		"channel":    channel,
		"agent_mode": agentMode,
		"model":      model,
	})
	return replayID
}

func (r *ReplayRecorder) RecordSessionEnd(ctx context.Context, replayID string, succeeded bool, messageCount int) {
	if r == nil || r.store == nil {
		return
	}

	status := "failed"
	if succeeded {
		status = "completed"
	}
	r.record(ctx, replayID, EventSessionEnd, nil, map[string]any{
		"succeeded":     succeeded,
		"status":        status,
		"message_count": messageCount,
	})
	if err := r.store.CompleteReplay(ctx, replayID, status, messageCount); err != nil {
		slog.Warn("replay: complete replay failed", "replay_id", replayID, "err", err)
	}
}

func (r *ReplayRecorder) RecordPhaseStart(ctx context.Context, replayID, phase string) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventPhaseStart, nil, map[string]any{
		"phase": phase,
	})
}

func (r *ReplayRecorder) RecordPhaseEnd(ctx context.Context, replayID, phase string, durationMs int64) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventPhaseEnd, &durationMs, map[string]any{
		"phase": phase,
	})
}

func (r *ReplayRecorder) RecordLLMRequest(ctx context.Context, replayID, model string, msgCount int, systemLen int, toolNames []string) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventLLMRequest, nil, map[string]any{
		"model":                model,
		"message_count":        msgCount,
		"system_prompt_length": systemLen,
		"tool_names":           toolNames,
	})
}

func (r *ReplayRecorder) RecordLLMResponse(ctx context.Context, replayID string, stopReason string, inputTokens, outputTokens int64, textLen int, toolCallCount int) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventLLMResponse, nil, map[string]any{
		"stop_reason":     stopReason,
		"input_tokens":    inputTokens,
		"output_tokens":   outputTokens,
		"text_length":     textLen,
		"tool_call_count": toolCallCount,
	})
}

func (r *ReplayRecorder) RecordToolStart(ctx context.Context, replayID, toolName, input string) {
	if r == nil || r.store == nil {
		return
	}

	if len(input) > 500 {
		input = input[:500] + "..."
	}
	r.record(ctx, replayID, EventToolStart, nil, map[string]any{
		"tool_name": toolName,
		"input":     input,
	})
}

func (r *ReplayRecorder) RecordToolEnd(ctx context.Context, replayID, toolName string, succeeded, denied bool, errStr string, durationMs int64) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventToolEnd, &durationMs, map[string]any{
		"tool_name": toolName,
		"succeeded": succeeded,
		"denied":    denied,
		"error":     errStr,
	})
}

func (r *ReplayRecorder) RecordPlanGenerated(ctx context.Context, replayID, summary string, subtaskCount int, confidence float64, hasDirectReply bool) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventPlanGenerated, nil, map[string]any{
		"summary":          summary,
		"subtask_count":    subtaskCount,
		"confidence":       confidence,
		"has_direct_reply": hasDirectReply,
	})
}

func (r *ReplayRecorder) RecordObservationResult(ctx context.Context, replayID string, successCount, failureCount, deniedCount int, progress float64) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventObservationDone, nil, map[string]any{
		"success_count": successCount,
		"failure_count": failureCount,
		"denied_count":  deniedCount,
		"progress":      progress,
	})
}

func (r *ReplayRecorder) RecordReflection(ctx context.Context, replayID string, confidence float64, succeeded, needsReplan bool, lessonCount int, finalAnswerLen int) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventReflectionDone, nil, map[string]any{
		"confidence":       confidence,
		"succeeded":        succeeded,
		"needs_replan":     needsReplan,
		"lesson_count":     lessonCount,
		"final_answer_len": finalAnswerLen,
	})
}

func (r *ReplayRecorder) RecordReplanStart(ctx context.Context, replayID string, attempt int, reason string) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventReplanStart, nil, map[string]any{
		"attempt": attempt,
		"reason":  reason,
	})
}

func (r *ReplayRecorder) RecordError(ctx context.Context, replayID, errStr, phase string) {
	if r == nil || r.store == nil {
		return
	}

	r.record(ctx, replayID, EventError, nil, map[string]any{
		"error": errStr,
		"phase": phase,
	})
}

func (r *ReplayRecorder) record(ctx context.Context, replayID string, eventType ReplayEventType, durationMs *int64, data map[string]any) {
	if replayID == "" {
		return
	}

	payload, err := json.Marshal(data)
	if err != nil {
		slog.Warn("replay: marshal event payload failed", "replay_id", replayID, "event_type", eventType, "err", err)
		return
	}

	event := ReplayEvent{
		ID:         fmt.Sprintf("evt_%d", time.Now().UnixNano()),
		ReplayID:   replayID,
		Sequence:   r.seq.Add(1),
		EventType:  eventType,
		Timestamp:  time.Now(),
		DurationMs: durationMs,
		Data:       payload,
	}
	if err := r.store.AppendEvent(ctx, replayID, &event); err != nil {
		slog.Warn("replay: append event failed", "replay_id", replayID, "event_type", eventType, "err", err)
		return
	}
	if r.onEvent != nil {
		ev := event
		go r.onEvent(&ev)
	}
}
