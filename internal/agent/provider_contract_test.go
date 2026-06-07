package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Provider adapter contract.
//
// Both provider adapters consume the same internal transcript (the
// Anthropic-shaped CompletionMessage that BuildMessages emits) and must agree
// on its meaning. P0 and P1 were caused by the OpenAI adapter silently
// diverging from this contract while the Claude adapter honored it; the
// separate per-provider tests missed it. This suite defines canonical scenarios
// ONCE, runs them through every adapter, normalizes each provider's wire output
// to a shared token stream, and asserts they match. Adding a new provider means
// writing one normalizer here — divergence then fails loudly.
//
// Token vocabulary (order-significant):
//   user:text
//   assistant:text
//   assistant:tooluse:<id>:<name>
//   toolresult:<tool_use_id>

// normalizeOpenAI serializes a request through the OpenAI adapter and reduces
// the wire messages to the shared contract token stream.
func normalizeOpenAI(t *testing.T, req CompletionRequest) []string {
	t.Helper()
	p := NewOpenAIProvider("", "gpt-4", "")
	data, err := json.Marshal(p.buildRequest(req, false).Messages)
	if err != nil {
		t.Fatalf("marshal openai: %v", err)
	}
	var msgs []struct {
		Role      string `json:"role"`
		Content   any    `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_calls"`
		ToolCallID string `json:"tool_call_id"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("unmarshal openai: %v", err)
	}
	var tokens []string
	for _, m := range msgs {
		switch m.Role {
		case "system":
			// not part of the conversation contract
		case "user":
			tokens = append(tokens, "user:text")
		case "assistant":
			if s, ok := m.Content.(string); ok && s != "" {
				tokens = append(tokens, "assistant:text")
			}
			for _, tc := range m.ToolCalls {
				tokens = append(tokens, "assistant:tooluse:"+tc.ID+":"+tc.Function.Name)
			}
		case "tool":
			tokens = append(tokens, "toolresult:"+m.ToolCallID)
		}
	}
	return tokens
}

// normalizeClaude serializes a request through the Claude adapter and reduces
// the wire messages to the same shared contract token stream.
func normalizeClaude(t *testing.T, req CompletionRequest) []string {
	t.Helper()
	p := NewClaudeProvider("", "claude-sonnet-4-6", "")
	data, err := json.Marshal(p.buildParams(req).Messages)
	if err != nil {
		t.Fatalf("marshal claude: %v", err)
	}
	var msgs []struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			ToolUseID string `json:"tool_use_id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("unmarshal claude: %v", err)
	}
	var tokens []string
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				tokens = append(tokens, m.Role+":text")
			case "tool_use":
				tokens = append(tokens, "assistant:tooluse:"+b.ID+":"+b.Name)
			case "tool_result":
				tokens = append(tokens, "toolresult:"+b.ToolUseID)
			}
		}
	}
	return tokens
}

func TestProviderAdapterContract(t *testing.T) {
	scenarios := []struct {
		name string
		req  CompletionRequest
		want []string
	}{
		{
			name: "plain user message",
			req: CompletionRequest{Messages: []CompletionMessage{
				{Role: "user", Content: "hello"},
			}},
			want: []string{"user:text"},
		},
		{
			name: "tool result round trip",
			req: CompletionRequest{Messages: []CompletionMessage{
				{Role: "user", Content: "run ls"},
				{Role: "assistant", Content: "", ToolBlocks: []ToolUseBlock{
					{ID: "call_99", Name: "bash", Input: `{"cmd":"ls"}`},
				}},
				{Role: "user", Content: "file1\nfile2", ToolUseID: "call_99"},
			}},
			want: []string{
				"user:text",
				"assistant:tooluse:call_99:bash",
				"toolresult:call_99",
			},
		},
		{
			name: "assistant with text and tool call",
			req: CompletionRequest{Messages: []CompletionMessage{
				{Role: "user", Content: "do it"},
				{Role: "assistant", Content: "thinking", ToolBlocks: []ToolUseBlock{
					{ID: "c1", Name: "read", Input: `{}`},
				}},
			}},
			want: []string{
				"user:text",
				"assistant:text",
				"assistant:tooluse:c1:read",
			},
		},
		{
			name: "two tool results in sequence",
			req: CompletionRequest{Messages: []CompletionMessage{
				{Role: "user", Content: "two"},
				{Role: "assistant", Content: "", ToolBlocks: []ToolUseBlock{
					{ID: "a", Name: "read", Input: `{}`},
					{ID: "b", Name: "write", Input: `{}`},
				}},
				{Role: "user", Content: "ra", ToolUseID: "a"},
				{Role: "user", Content: "rb", ToolUseID: "b"},
			}},
			want: []string{
				"user:text",
				"assistant:tooluse:a:read",
				"assistant:tooluse:b:write",
				"toolresult:a",
				"toolresult:b",
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			oai := normalizeOpenAI(t, sc.req)
			claude := normalizeClaude(t, sc.req)

			if !reflect.DeepEqual(oai, sc.want) {
				t.Errorf("OpenAI adapter violated contract\n  got:  %v\n  want: %v", oai, sc.want)
			}
			if !reflect.DeepEqual(claude, sc.want) {
				t.Errorf("Claude adapter violated contract\n  got:  %v\n  want: %v", claude, sc.want)
			}
			if !reflect.DeepEqual(oai, claude) {
				t.Errorf("adapters disagree on the same transcript\n  openai: %v\n  claude: %v", oai, claude)
			}
		})
	}
}
