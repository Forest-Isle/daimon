package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

type ReplayEngine struct {
	store    ReplayStore
	sessions *session.Manager
}

type EventCursor struct {
	Event   ReplayEvent
	Index   int
	Total   int
	IsFirst bool
	IsLast  bool
}

type ReconstructedState struct {
	Replay   *Replay
	Event    *ReplayEvent
	Messages []session.Message
}

func NewReplayEngine(store ReplayStore, sessions *session.Manager) *ReplayEngine {
	return &ReplayEngine{store: store, sessions: sessions}
}

func (e *ReplayEngine) Load(ctx context.Context, replayID string) (*Replay, error) {
	replay, err := e.store.LoadReplay(ctx, replayID)
	if err != nil {
		return nil, err
	}
	if replay == nil {
		return nil, fmt.Errorf("replay not found: %s", replayID)
	}
	return replay, nil
}

func (e *ReplayEngine) Traverse(ctx context.Context, replayID string, fn func(EventCursor) error) error {
	events, err := e.store.LoadEvents(ctx, replayID, 0, 100000)
	if err != nil {
		return err
	}

	total := len(events)
	for i, event := range events {
		cursor := EventCursor{
			Event:   event,
			Index:   i,
			Total:   total,
			IsFirst: i == 0,
			IsLast:  i == total-1,
		}
		if err := fn(cursor); err != nil {
			return err
		}
	}
	return nil
}

func (e *ReplayEngine) GetReconstructedState(ctx context.Context, replayID string, sequence int) (*ReconstructedState, error) {
	replay, err := e.Load(ctx, replayID)
	if err != nil {
		return nil, err
	}

	events, err := e.store.LoadEvents(ctx, replayID, 0, 100000)
	if err != nil {
		return nil, err
	}

	var selected *ReplayEvent
	for i := range events {
		if int(events[i].Sequence) == sequence {
			selected = &events[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("event not found for replay %s sequence %d", replayID, sequence)
	}

	messages, err := e.loadSessionMessages(ctx, replay.SessionID, selected.Timestamp)
	if err != nil {
		return nil, err
	}

	return &ReconstructedState{
		Replay:   replay,
		Event:    selected,
		Messages: messages,
	}, nil
}

func (e *ReplayEngine) Summary(ctx context.Context, replayID string) (string, error) {
	replay, err := e.Load(ctx, replayID)
	if err != nil {
		return "", err
	}

	var toolCount int
	var planCount int
	var replanCount int
	var reflected bool
	var durationMs int64
	if err := e.Traverse(ctx, replayID, func(cursor EventCursor) error {
		switch cursor.Event.EventType {
		case EventToolEnd:
			toolCount++
		case EventPlanGenerated:
			planCount++
		case EventReplanStart:
			replanCount++
		case EventReflectionDone:
			reflected = true
		}
		if cursor.Event.DurationMs != nil {
			durationMs += *cursor.Event.DurationMs
		}
		return nil
	}); err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"Replay %s | mode=%s status=%s model=%s | %d tools in %dms | %d plans %d replans | reflected=%t",
		replay.ID,
		replay.AgentMode,
		replay.Status,
		replay.Model,
		toolCount,
		durationMs,
		planCount,
		replanCount,
		reflected,
	), nil
}

func (e *ReplayEngine) loadSessionMessages(ctx context.Context, sessionID string, at time.Time) ([]session.Message, error) {
	if e.sessions == nil || sessionID == "" {
		return nil, nil
	}

	sess, err := e.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, nil
	}

	history := sess.History()
	messages := make([]session.Message, 0, len(history))
	for _, msg := range history {
		if msg.CreatedAt.After(at) {
			break
		}
		messages = append(messages, msg)
	}
	return messages, nil
}
