package selfops

import (
	"context"
	"log/slog"
	"sync"
)

type ErrorRing struct {
	mu   sync.Mutex
	buf  []string
	max  int
	next int
	full bool
}

func NewErrorRing(max int) *ErrorRing {
	if max < 0 {
		max = 0
	}
	return &ErrorRing{
		buf: make([]string, max),
		max: max,
	}
}

func (r *ErrorRing) Append(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.max <= 0 {
		return
	}
	r.buf[r.next] = msg
	r.next = (r.next + 1) % r.max
	if r.next == 0 {
		r.full = true
	}
}

func (r *ErrorRing) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.max <= 0 {
		return nil
	}
	if !r.full {
		out := make([]string, r.next)
		copy(out, r.buf[:r.next])
		return out
	}
	out := make([]string, r.max)
	copy(out, r.buf[r.next:])
	copy(out[r.max-r.next:], r.buf[:r.next])
	return out
}

var Errors = NewErrorRing(256)

type teeHandler struct {
	base slog.Handler
	ring *ErrorRing
}

func NewTeeHandler(base slog.Handler) slog.Handler {
	return &teeHandler{base: base, ring: Errors}
}

func (h *teeHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.base.Enabled(ctx, lvl)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.ring.Append(r.Message)
	}
	return h.base.Handle(ctx, r)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{base: h.base.WithAttrs(attrs), ring: h.ring}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{base: h.base.WithGroup(name), ring: h.ring}
}
