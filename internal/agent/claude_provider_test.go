package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestClaudeProvider_BuildParams_ToolResultShape locks in the Claude side of the
// internal tool-result contract: BuildMessages emits a tool result as
// CompletionMessage{Role:"user", ToolUseID:set}, and the Claude adapter must
// serialize that into an Anthropic tool_result block carrying the tool_use_id.
// This is the symmetric guard to the OpenAI adapter tests — the two providers
// must agree on the same internal shape, which is the root-cause class behind
// the P0/P1 bugs. Asserting on the marshaled wire JSON avoids coupling to
// SDK-internal Go field names.
func TestClaudeProvider_BuildParams_ToolResultShape(t *testing.T) {
	p := NewClaudeProvider("", "claude-sonnet-4-6", "")
	req := CompletionRequest{
		Messages: []CompletionMessage{
			{Role: "user", Content: "run the tool"},
			{Role: "assistant", Content: "", ToolBlocks: []ToolUseBlock{
				{ID: "call_99", Name: "bash", Input: `{"cmd":"ls"}`},
			}},
			// Exactly what BuildMessages emits for a tool result:
			{Role: "user", Content: "file1\nfile2", ToolUseID: "call_99"},
		},
	}

	params := p.buildParams(req)

	if len(params.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(params.Messages))
	}

	data, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	wire := string(data)

	// The tool result must serialize as an Anthropic tool_result block that
	// references the originating tool_use id.
	if !strings.Contains(wire, "tool_result") {
		t.Errorf("expected a tool_result block in wire JSON:\n%s", wire)
	}
	if !strings.Contains(wire, "tool_use_id") {
		t.Errorf("expected tool_use_id in wire JSON:\n%s", wire)
	}
	if !strings.Contains(wire, "call_99") {
		t.Errorf("tool_use id call_99 missing from wire JSON:\n%s", wire)
	}
	// The assistant turn must serialize its tool call as a tool_use block.
	if !strings.Contains(wire, "tool_use") {
		t.Errorf("expected a tool_use block for the assistant turn:\n%s", wire)
	}
	if !strings.Contains(wire, "bash") {
		t.Errorf("tool name bash missing from wire JSON:\n%s", wire)
	}
}

// TestClaudeProvider_BuildParams_PlainUserMessage verifies a user message with
// no ToolUseID serializes as an ordinary text block, not a tool result.
func TestClaudeProvider_BuildParams_PlainUserMessage(t *testing.T) {
	p := NewClaudeProvider("", "claude-sonnet-4-6", "")
	req := CompletionRequest{
		Messages: []CompletionMessage{{Role: "user", Content: "just text"}},
	}

	params := p.buildParams(req)
	data, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	wire := string(data)

	if strings.Contains(wire, "tool_result") {
		t.Errorf("plain user message must not produce a tool_result block:\n%s", wire)
	}
	if !strings.Contains(wire, "just text") {
		t.Errorf("user text missing from wire JSON:\n%s", wire)
	}
}
