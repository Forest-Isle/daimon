package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// mockSingleExecutor is a fake SingleToolExecutor for testing PlanTaskTool.
type mockSingleExecutor struct {
	outputs map[string]string
	errors  map[string]string
}

func (m *mockSingleExecutor) Execute(ctx context.Context, toolName, input string) (string, error) {
	if errMsg, ok := m.errors[toolName]; ok {
		return "", fmt.Errorf("%s", errMsg)
	}
	if out, ok := m.outputs[toolName]; ok {
		return out, nil
	}
	return "ok", nil
}

func TestPlanTaskSingleSubtask(t *testing.T) {
	exec := &mockSingleExecutor{
		outputs: map[string]string{"bash": "hello world\n"},
	}
	pt := NewPlanTaskTool(1, exec, nil, nil, nil)

	input := mustMarshal(t, map[string]any{
		"subtasks": []map[string]any{
			{
				"id":        "task1",
				"description": "Run hello",
				"tool_name":    "bash",
				"tool_input":   `{"command":"echo hello"}`,
			},
		},
	})

	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "task1: done") {
		t.Errorf("expected output to contain 'task1: done', got: %s", result.Output)
	}
}

func TestPlanTaskMultipleParallelSubtasks(t *testing.T) {
	exec := &mockSingleExecutor{
		outputs: map[string]string{
			"bash": "shell output",
			"read": "file content",
		},
	}
	pt := NewPlanTaskTool(5, exec, nil, nil, nil)

	input := mustMarshal(t, map[string]any{
		"subtasks": []map[string]any{
			{
				"id":        "task1",
				"description": "List files",
				"tool_name":    "bash",
				"tool_input":   `{"command":"ls"}`,
			},
			{
				"id":        "task2",
				"description": "Read config",
				"tool_name":    "read",
				"tool_input":   `{"path":"config.yaml"}`,
			},
		},
	})

	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "task1: done") {
		t.Errorf("expected output to contain 'task1: done', got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "task2: done") {
		t.Errorf("expected output to contain 'task2: done', got: %s", result.Output)
	}
}

func TestPlanTaskWithFailure(t *testing.T) {
	exec := &mockSingleExecutor{
		outputs: map[string]string{"bash": "ok"},
		errors:  map[string]string{"broken_tool": "connection refused"},
	}
	pt := NewPlanTaskTool(2, exec, nil, nil, nil)

	input := mustMarshal(t, map[string]any{
		"subtasks": []map[string]any{
			{
				"id":        "task1",
				"description": "This will fail",
				"tool_name":    "broken_tool",
				"tool_input":   `{}`,
			},
			{
				"id":        "task2",
				"description": "This should succeed",
				"tool_name":    "bash",
				"tool_input":   `{"command":"echo ok"}`,
			},
		},
	})

	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "task1: failed") {
		t.Errorf("expected output to contain 'task1: failed', got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "connection refused") {
		t.Errorf("expected output to contain error message, got: %s", result.Output)
	}
}

func TestPlanTaskInvalidJSON(t *testing.T) {
	pt := NewPlanTaskTool(1, &mockSingleExecutor{}, nil, nil, nil)

	result, err := pt.Execute(context.Background(), []byte("not valid json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected an error for invalid JSON")
	}
	if !strings.Contains(result.Error, "invalid JSON input") {
		t.Errorf("expected error to mention 'invalid JSON input', got: %s", result.Error)
	}
}

func TestPlanTaskEmptySubtasks(t *testing.T) {
	pt := NewPlanTaskTool(1, &mockSingleExecutor{}, nil, nil, nil)

	input := mustMarshal(t, map[string]any{"subtasks": []map[string]any{}})
	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "No subtasks provided") {
		t.Errorf("expected 'No subtasks provided.', got: %s", result.Output)
	}
}

func TestPlanTaskDependencyChain(t *testing.T) {
	exec := &mockSingleExecutor{
		outputs: map[string]string{
			"bash":  "shell output",
			"write": "file written",
		},
	}
	pt := NewPlanTaskTool(5, exec, nil, nil, nil)

	// task3 depends on task2, which depends on task1 — must execute sequentially.
	input := mustMarshal(t, map[string]any{
		"subtasks": []map[string]any{
			{
				"id":        "task1",
				"description": "Create directory",
				"tool_name":    "bash",
				"tool_input":   `{"command":"mkdir -p /tmp/test"}`,
			},
			{
				"id":        "task2",
				"description": "Write file",
				"tool_name":    "write",
				"tool_input":   `{"path":"/tmp/test/file.txt","content":"hello"}`,
				"depends_on":   []string{"task1"},
			},
			{
				"id":        "task3",
				"description": "Read file back",
				"tool_name":    "bash",
				"tool_input":   `{"command":"cat /tmp/test/file.txt"}`,
				"depends_on":   []string{"task2"},
			},
		},
	})

	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "task1: done") {
		t.Errorf("expected task1 done, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "task2: done") {
		t.Errorf("expected task2 done, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "task3: done") {
		t.Errorf("expected task3 done, got: %s", result.Output)
	}
}

func TestPlanTaskUpstreamFailureSkipsDownstream(t *testing.T) {
	exec := &mockSingleExecutor{
		outputs: map[string]string{"bash": "ok"},
		errors:  map[string]string{"write": "permission denied"},
	}
	pt := NewPlanTaskTool(5, exec, nil, nil, nil)

	input := mustMarshal(t, map[string]any{
		"subtasks": []map[string]any{
			{
				"id":        "task1",
				"description": "This fails",
				"tool_name":    "write",
				"tool_input":   `{"path":"/etc/config","content":"x"}`,
			},
			{
				"id":        "task2",
				"description": "Depends on failed task",
				"tool_name":    "bash",
				"tool_input":   `{"command":"echo hello"}`,
				"depends_on":   []string{"task1"},
			},
		},
	})

	result, err := pt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected result error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "task1: failed") {
		t.Errorf("expected task1: failed, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "task2: skipped") {
		t.Errorf("expected task2: skipped due to upstream failure, got: %s", result.Output)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
