package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/core"
	"github.com/Forest-Isle/IronClaw/internal/core/adapter"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// fakeLegacyProvider satisfies internal/agent.Provider with scripted output.
type fakeLegacyProvider struct {
	turns []agent.CompletionResponse
	calls int
}

func (f *fakeLegacyProvider) Complete(_ context.Context, _ agent.CompletionRequest) (*agent.CompletionResponse, error) {
	if f.calls >= len(f.turns) {
		return nil, errors.New("over")
	}
	r := f.turns[f.calls]
	f.calls++
	return &r, nil
}

func (f *fakeLegacyProvider) Stream(_ context.Context, _ agent.CompletionRequest) (agent.StreamIterator, error) {
	return nil, errors.New("not used")
}

// fakeLegacyTool implements internal/tool.Tool plus ReadOnlyTool.
type fakeLegacyTool struct{}

func (fakeLegacyTool) Name() string                                 { return "echo" }
func (fakeLegacyTool) Description() string                          { return "echo legacy" }
func (fakeLegacyTool) InputSchema() map[string]any                  { return map[string]any{"type": "object"} }
func (fakeLegacyTool) RequiresApproval() bool                       { return false }
func (fakeLegacyTool) IsReadOnly() bool                             { return true }
func (fakeLegacyTool) Execute(_ context.Context, in []byte) (tool.Result, error) {
	var p struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(in, &p)
	return tool.Result{Output: p.Message}, nil
}

// TestLegacyAdapter wires a fake legacy provider + legacy tool registry
// to the new core.Agent and validates the entire round-trip.
func TestLegacyAdapter(t *testing.T) {
	legacyTools := tool.NewRegistry()
	legacyTools.Register(fakeLegacyTool{})

	legacyProv := &fakeLegacyProvider{turns: []agent.CompletionResponse{
		{
			ToolCalls:  []agent.ToolUseBlock{{ID: "u1", Name: "echo", Input: `{"message":"hi"}`}},
			StopReason: agent.StopToolUse,
		},
		{Text: "the tool said hi", StopReason: agent.StopEndTurn},
	}}

	prov := adapter.NewLegacyProvider(legacyProv)
	tools := adapter.ImportToolRegistry(legacyTools)

	ag := core.New(prov, tools, nil, core.Config{Model: "test"})
	out, stop, err := ag.Run(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "the tool said hi" {
		t.Fatalf("got %q", out)
	}
	if stop != core.StopEndTurn {
		t.Fatalf("stop %v", stop)
	}
}
