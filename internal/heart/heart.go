// Package heart is the event heart: it turns every change in the world — chat
// messages, mail, calendar edits, timers — into a single persisted event stream
// and delivers each event to a handler exactly once. Persisting before routing
// makes the stream auditable and crash-recoverable: a restart replays whatever
// had been stored but not yet routed.
package heart

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Event is one observed change in the world.
type Event struct {
	ID         string
	Source     string
	Kind       string
	Payload    string
	OccurredAt string
	DedupKey   string
}

// Source is a long-running producer of events (a channel adapter, a mail poller,
// a timer). Run must keep emitting until ctx is cancelled, reconnecting on its
// own transient failures. emit returns an error when the event could not be
// persisted, so a source that mutates its own state on emit (e.g. marking a
// follow-up fired) can avoid losing work that never reached the stream.
type Source interface {
	Name() string
	Run(ctx context.Context, emit func(Event) error) error
}

// Handler consumes a routed event. The router (attention) plugs in here.
type Handler func(ctx context.Context, ev Event)

// Heart registers sources, persists what they emit (deduplicated), and delivers
// each newly stored event to the handler exactly once.
type Heart struct {
	store   *Store
	handler Handler

	mu      sync.Mutex
	sources []Source
}

func New(store *Store, handler Handler) *Heart {
	return &Heart{store: store, handler: handler}
}

// Register adds a source. Call before Run.
func (h *Heart) Register(s Source) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sources = append(h.sources, s)
}

// Run replays any unrouted backlog, then starts every source and processes
// emitted events until ctx is cancelled.
func (h *Heart) Run(ctx context.Context) error {
	h.recover(ctx)

	h.mu.Lock()
	sources := append([]Source(nil), h.sources...)
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			emit := func(ev Event) error {
				ev.Source = src.Name()
				return h.process(ctx, ev)
			}
			if err := src.Run(ctx, emit); err != nil && ctx.Err() == nil {
				slog.Error("heart: source stopped", "source", src.Name(), "err", err)
			}
		}(s)
	}
	wg.Wait()
	return ctx.Err()
}

// process persists one event and, if it was newly stored (not a duplicate),
// hands it to the router and marks it routed. It returns an error only when the
// event could not be persisted; a duplicate is reported as success (it was
// already handled), so a caller mutating its own state may safely proceed.
func (h *Heart) process(ctx context.Context, ev Event) error {
	if ev.OccurredAt == "" {
		ev.OccurredAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	inserted, err := h.store.Persist(ctx, &ev)
	if err != nil {
		slog.Error("heart: persist event failed", "source", ev.Source, "kind", ev.Kind, "err", err)
		return err
	}
	if !inserted {
		return nil // duplicate; already handled
	}
	h.deliver(ctx, ev)
	return nil
}

// deliver routes an event and records the outcome. Routing failures are not
// fatal: the event stays marked routed (the handler owns its own retries), but
// the persisted event remains for audit.
func (h *Heart) deliver(ctx context.Context, ev Event) {
	verdict := "routed"
	if h.handler != nil {
		h.handler(ctx, ev)
	} else {
		verdict = "no_handler"
	}
	if err := h.store.MarkRouted(ctx, ev.ID, verdict); err != nil {
		slog.Error("heart: mark routed failed", "id", ev.ID, "err", err)
	}
}

// recover replays events that were persisted but never routed (e.g. a crash
// between persist and deliver).
func (h *Heart) recover(ctx context.Context) {
	pending, err := h.store.Unrouted(ctx)
	if err != nil {
		slog.Error("heart: load unrouted backlog failed", "err", err)
		return
	}
	for _, ev := range pending {
		h.deliver(ctx, ev)
	}
	if len(pending) > 0 {
		slog.Info("heart: replayed unrouted backlog", "count", len(pending))
	}
}
