package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type testChannel struct{}

func (m *testChannel) Name() string                                        { return "test" }
func (m *testChannel) Start(context.Context, channel.InboundHandler) error { return nil }
func (m *testChannel) Send(context.Context, channel.OutboundMessage) error { return nil }
func (m *testChannel) Stop(context.Context) error                          { return nil }
func (m *testChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return &testUpdater{}, nil
}

type testUpdater struct{}

func (m *testUpdater) Update(text string) error { return nil }
func (m *testUpdater) Finish(text string) error { return nil }

type testProvider struct {
	text      string
	toolCalls []mind.ToolUseBlock
	callCount int
}

func (m *testProvider) Complete(_ context.Context, _ mind.CompletionRequest) (*mind.CompletionResponse, error) {
	m.callCount++
	if m.callCount > 1 {
		return &mind.CompletionResponse{Text: m.text}, nil
	}
	return &mind.CompletionResponse{Text: m.text, ToolCalls: m.toolCalls}, nil
}

func (m *testProvider) Capabilities() mind.Caps { return mind.Caps{} }

func (m *testProvider) Stream(_ context.Context, _ mind.CompletionRequest) (mind.StreamIterator, error) {
	m.callCount++
	if m.callCount > 1 {
		return &testStream{text: m.text}, nil
	}
	return &testStream{text: m.text, toolCalls: m.toolCalls}, nil
}

type testStream struct {
	text      string
	toolCalls []mind.ToolUseBlock
	done      bool
}

func (m *testStream) Next() (mind.StreamDelta, error) {
	if !m.done {
		m.done = true
		return mind.StreamDelta{Text: m.text, ToolCalls: m.toolCalls, Done: true, StopReason: "end_turn"}, nil
	}
	return mind.StreamDelta{}, nil
}

func (m *testStream) Close() {}

type testReadTool struct{}

func (t *testReadTool) Name() string        { return "read" }
func (t *testReadTool) Description() string { return "Read a file" }
func (t *testReadTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *testReadTool) RequiresApproval() bool { return false }
func (t *testReadTool) Execute(_ context.Context, input []byte) (tool.Result, error) {
	return tool.Result{Output: "file contents"}, nil
}

type testWriteTool struct{}

func (t *testWriteTool) Name() string        { return "write" }
func (t *testWriteTool) Description() string { return "Write a file" }
func (t *testWriteTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *testWriteTool) RequiresApproval() bool { return false }
func (t *testWriteTool) Execute(_ context.Context, input []byte) (tool.Result, error) {
	return tool.Result{Output: "written"}, nil
}

func TestLinearLoop_SingleToolCall(t *testing.T) {
	sess := &session.Session{
		ID: "test-sess-1", Channel: "test", ChannelID: "ch1", CreatedAt: time.Now(),
	}

	registry := tool.NewRegistry()
	registry.Register(&testReadTool{})

	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = registry
	deps.Core.Cfg.MaxIterations = 3
	deps.Core.Provider = &testProvider{
		text:      "Let me read that.",
		toolCalls: []mind.ToolUseBlock{{ID: "call_1", Name: "read", Input: `{"path":"/tmp/test"}`}},
	}

	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	ch := &testChannel{}

	loop := &LinearLoop{}
	err := loop.Execute(context.Background(), a, ch, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", Text: "read /tmp/test",
	}, sess, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msgs := sess.History()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
}

func TestLinearLoop_NoToolCall(t *testing.T) {
	sess := &session.Session{
		ID: "test-sess-2", Channel: "test", ChannelID: "ch1", CreatedAt: time.Now(),
	}

	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = tool.NewRegistry()
	deps.Core.Cfg.MaxIterations = 3
	deps.Core.Provider = &testProvider{text: "Hello, how can I help?"}

	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	ch := &testChannel{}

	loop := &LinearLoop{}
	err := loop.Execute(context.Background(), a, ch, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", Text: "hello",
	}, sess, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLinearLoop_ParallelDispatch(t *testing.T) {
	sess := &session.Session{
		ID: "test-sess-3", Channel: "test", ChannelID: "ch1", CreatedAt: time.Now(),
	}

	registry := tool.NewRegistry()
	registry.Register(&testReadTool{})
	registry.Register(&testWriteTool{})

	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = registry
	deps.Core.Cfg.MaxIterations = 3
	deps.Core.Provider = &testProvider{
		text: "Reading two files.",
		toolCalls: []mind.ToolUseBlock{
			{ID: "call_1", Name: "read", Input: `{"path":"/tmp/a"}`},
			{ID: "call_2", Name: "write", Input: `{"path":"/tmp/b","content":"x"}`},
		},
	}

	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	ch := &testChannel{}

	loop := &LinearLoop{}
	err := loop.Execute(context.Background(), a, ch, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", Text: "read both files",
	}, sess, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both tool calls should have been dispatched (parallel)
	msgs := sess.History()
	toolResultCount := 0
	for _, m := range msgs {
		if m.Role == "tool_result" {
			toolResultCount++
		}
	}
	if toolResultCount != 2 {
		t.Fatalf("expected 2 tool results, got %d", toolResultCount)
	}
}
