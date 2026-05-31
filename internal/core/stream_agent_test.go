package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/core"
)

// streamingFakeProvider returns a provider whose Stream method yields pre-built
// chunks and whose Complete falls back. Used for both happy-path and fallback
// testing. Tracks Stream call count internally for multi-turn tests.
type streamingFakeProvider struct {
	mu         sync.Mutex
	chunks     []core.LLMChunk
	fallback   *core.LLMResponse
	streamErr  error
	streamCalls int
}

func (f *streamingFakeProvider) Complete(ctx context.Context, req core.LLMRequest) (*core.LLMResponse, error) {
	if f.fallback != nil {
		return f.fallback, nil
	}
	return &core.LLMResponse{Text: "fallback", StopReason: core.StopEndTurn}, nil
}

func (f *streamingFakeProvider) Stream(ctx context.Context, req core.LLMRequest) (core.Stream, error) {
	f.mu.Lock()
	f.streamCalls++
	callN := f.streamCalls
	f.mu.Unlock()

	if f.streamErr != nil {
		return nil, f.streamErr
	}
	// After first call, reject streaming so the agent loop falls back to Complete.
	if callN > 1 {
		return nil, errors.New("multi-turn: use Complete")
	}
	cp := make([]core.LLMChunk, len(f.chunks))
	copy(cp, f.chunks)
	return &fakeStream{chunks: cp, pos: 0}, nil
}

type fakeStream struct {
	chunks []core.LLMChunk
	pos    int
}

func (f *fakeStream) Next(ctx context.Context) (core.LLMChunk, error) {
	if f.pos >= len(f.chunks) {
		return core.LLMChunk{Done: true, Stop: core.StopEndTurn}, nil
	}
	c := f.chunks[f.pos]
	f.pos++
	return c, nil
}

func (f *fakeStream) Close() error { return nil }

// TestStreamAgentHappyPath: two text chunks → tool call → final text via fallback
// when the second stream turn returns empty.
func TestStreamAgentHappyPath(t *testing.T) {
	reg := core.NewToolRegistry()
	reg.Register(newEcho())

	prov := &streamingFakeProvider{
		chunks: []core.LLMChunk{
			{Text: "Hello "},
			{Text: "world"},
			{Done: true, ToolCall: &core.ToolCall{ID: "u1", Name: "echo", Input: nil}, Stop: core.StopToolUse},
		},
		fallback: &core.LLMResponse{Text: "all done", StopReason: core.StopEndTurn},
	}

	var got []core.EventKind
	var mu sync.Mutex
	sink := core.EventSinkFunc(func(e core.Event) {
		mu.Lock()
		got = append(got, e.Kind)
		mu.Unlock()
	})

	ag, err := core.New(prov, reg, nil, core.Config{Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	sa := core.NewStreamAgent(ag)
	res, err := sa.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text != "all done" {
		t.Fatalf("got %q", res.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	chunks := 0
	for _, k := range got {
		if k == core.EventLLMChunk {
			chunks++
		}
	}
	if chunks < 2 {
		t.Fatalf("expected >=2 chunk events, got %d", chunks)
	}
}

// TestStreamAgentFallback: provider rejects streaming → should use Complete.
func TestStreamAgentFallback(t *testing.T) {
	prov := &streamingFakeProvider{
		streamErr: errors.New("streaming not supported"),
		fallback:  &core.LLMResponse{Text: "I completed", StopReason: core.StopEndTurn},
	}
	ag, err := core.New(prov, nil, nil, core.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sa := core.NewStreamAgent(ag)
	res, err := sa.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "completed") {
		t.Fatalf("got %q", res.Text)
	}
}
