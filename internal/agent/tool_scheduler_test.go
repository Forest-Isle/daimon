package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type schedulerTool struct {
	name   string
	safety tool.ParallelSafety
	paths  []string
}

func (t *schedulerTool) Name() string                { return t.name }
func (t *schedulerTool) Description() string         { return "scheduler test tool" }
func (t *schedulerTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *schedulerTool) RequiresApproval() bool      { return false }
func (t *schedulerTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	return tool.Result{Output: "ok"}, nil
}
func (t *schedulerTool) Capabilities() tool.ToolCapabilities {
	return tool.ToolCapabilities{ParallelSafety: t.safety, IsReadOnly: t.safety == tool.ParallelSafe}
}
func (t *schedulerTool) ExtractPaths(input []byte) ([]string, error) {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Path != "" {
		return []string{payload.Path}, nil
	}
	return t.paths, nil
}

func TestScheduleToolBatchesRespectsCapabilities(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&schedulerTool{name: "read_a", safety: tool.ParallelSafe})
	registry.Register(&schedulerTool{name: "read_b", safety: tool.ParallelSafe})
	registry.Register(&schedulerTool{name: "write", safety: tool.ParallelPathScoped})
	registry.Register(&schedulerTool{name: "bash", safety: tool.ParallelNever})

	deps := AgentDeps{}.WithDefaults()
	deps.Core.Tools = registry
	deps.Core.Cfg.Execution.MaxParallelTools = 2
	deps.Core.ToolsCfg.ConcurrentExecution = config.ConcurrentExecutionConfig{
		Enabled:        true,
		MaxConcurrency: 4,
	}
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	calls := []mind.ToolUseBlock{
		{ID: "1", Name: "read_a", Input: `{}`},
		{ID: "2", Name: "read_b", Input: `{}`},
		{ID: "3", Name: "write", Input: `{"path":"a.go"}`},
		{ID: "4", Name: "write", Input: `{"path":"a.go"}`},
		{ID: "5", Name: "write", Input: `{"path":"b.go"}`},
		{ID: "6", Name: "bash", Input: `{}`},
		{ID: "7", Name: "read_a", Input: `{}`},
	}

	batches := a.scheduleToolBatches(calls)
	got := batchIDs(batches)
	want := [][]string{
		{"1", "2"},
		{"3"},
		{"4", "5"},
		{"6"},
		{"7"},
	}
	if !equalStringMatrix(got, want) {
		t.Fatalf("batches = %#v, want %#v", got, want)
	}
}

func batchIDs(batches [][]mind.ToolUseBlock) [][]string {
	out := make([][]string, 0, len(batches))
	for _, batch := range batches {
		var ids []string
		for _, call := range batch {
			ids = append(ids, call.ID)
		}
		out = append(out, ids)
	}
	return out
}

func equalStringMatrix(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
