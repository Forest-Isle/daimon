package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMemoryManageToolName(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	if tool.Name() != "memory_manage" {
		t.Errorf("expected tool name 'memory_manage', got %q", tool.Name())
	}
}

func TestMemoryManageToolRequiresApproval(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	if !tool.RequiresApproval() {
		t.Error("memory_manage tool should require approval")
	}
}

func TestMemoryManageToolInputSchema(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	schema := tool.InputSchema()

	// Check it's an object type
	if schema["type"] != "object" {
		t.Errorf("expected schema type 'object', got %v", schema["type"])
	}

	// Check required fields
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("expected required to be []string")
	}
	foundAction := false
	for _, r := range required {
		if r == "action" {
			foundAction = true
		}
	}
	if !foundAction {
		t.Error("'action' should be a required field")
	}

	// Check properties include expected fields
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}
	expectedFields := []string{"action", "query", "sensitivity", "memory_type", "retention_days", "confirm_ids"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("expected field %q in schema properties", field)
		}
	}
}

func TestMemoryManageToolInvalidAction(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "invalid_action"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid action")
	}
}

func TestMemoryManageToolInvalidJSON(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	result, err := tool.Execute(context.Background(), []byte("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("expected error for invalid JSON input")
	}
}

func TestMemoryManageToolForgetRequiresQuery(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "forget"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("forget without query or confirm_ids should return an error")
	}
}

func TestMemoryManageToolRetentionRequiresType(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]string{"action": "retention"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("retention without memory_type should return an error")
	}
}

func TestMemoryManageToolRetentionRequiresPositiveDays(t *testing.T) {
	tool := NewMemoryManageTool(nil, nil, "")
	input, _ := json.Marshal(map[string]any{
		"action":         "retention",
		"memory_type":    "episodic",
		"retention_days": 0,
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Error("retention with 0 days should return an error")
	}
}

// TODO: Task 3.11-3.12 - Lifecycle decision tests and memory consolidation tests
// are skipped for now as they require complex LLM mocking. The lifecycle manager
// uses an LLM to make ADD/UPDATE/DELETE/NOOP decisions, and the consolidation
// process needs an LLM for summarization. These would need a mock Completer
// implementation that returns predictable lifecycle decisions.
