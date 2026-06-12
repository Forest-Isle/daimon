package heart

import (
	"context"
	"time"
)

// TimerSource emits a periodic event. It is the simplest source — proof that the
// heart can drive itself without an external trigger (early reports, idle ticks).
type TimerSource struct {
	SourceName string
	Kind       string
	Payload    string
	Interval   time.Duration
}

func (t *TimerSource) Name() string {
	if t.SourceName == "" {
		return "timer"
	}
	return t.SourceName
}

func (t *TimerSource) Run(ctx context.Context, emit func(Event)) error {
	if t.Interval <= 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			emit(Event{Kind: t.Kind, Payload: t.Payload})
		}
	}
}
