package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProvider_Complete_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong auth header")
		}

		var req oaiRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("invalid request JSON: %v", err)
		}
		if req.Model != "gpt-4" {
			t.Errorf("model = %q, want gpt-4", req.Model)
		}

		resp := oaiResponse{
			Choices: []oaiChoice{{
				Message:      oaiMessage{Role: "assistant", Content: "Hello!"},
				FinishReason: "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", "gpt-4", server.URL)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		System:   "You are helpful.",
		Messages: []CompletionMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Text != "Hello!" {
		t.Errorf("text = %q, want %q", resp.Text, "Hello!")
	}
	if resp.StopReason != StopEndTurn {
		t.Errorf("stop_reason = %q, want %q", resp.StopReason, StopEndTurn)
	}
}

func TestOpenAIProvider_Complete_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := oaiResponse{
			Choices: []oaiChoice{{
				Message: oaiMessage{
					Role: "assistant",
					ToolCalls: []oaiToolCall{{
						ID:   "call_123",
						Type: "function",
						Function: oaiToolCallFunc{
							Name:      "bash",
							Arguments: `{"command":"echo hello"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("", "gpt-4", server.URL)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []CompletionMessage{{Role: "user", Content: "run echo hello"}},
		Tools: []ToolDefinition{{
			Name:        "bash",
			Description: "Run a bash command",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopToolUse {
		t.Errorf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "bash" {
		t.Errorf("tool name = %q, want bash", resp.ToolCalls[0].Name)
	}
}

func TestOpenAIProvider_Complete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"invalid api key","type":"auth_error"}}`)
	}))
	defer server.Close()

	p := NewOpenAIProvider("bad-key", "gpt-4", server.URL)
	_, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []CompletionMessage{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestOpenAIProvider_BuildRequest_MessageMapping(t *testing.T) {
	p := NewOpenAIProvider("", "gpt-4", "")
	req := CompletionRequest{
		System: "system prompt",
		Messages: []CompletionMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi!", ToolBlocks: []ToolUseBlock{
				{ID: "c1", Name: "bash", Input: `{"cmd":"ls"}`},
			}},
			{Role: "tool_result", Content: "file1\nfile2", ToolUseID: "c1"},
			{Role: "user", Content: "Thanks"},
		},
		Tools: []ToolDefinition{
			{Name: "bash", Description: "Run bash", InputSchema: map[string]any{"type": "object"}},
		},
	}

	oai := p.buildRequest(req, false)

	if len(oai.Messages) != 5 {
		t.Fatalf("message count = %d, want 5 (system + 4 conv)", len(oai.Messages))
	}
	if oai.Messages[0].Role != "system" {
		t.Error("first message should be system")
	}
	if oai.Messages[2].Role != "assistant" {
		t.Error("third message should be assistant")
	}
	if len(oai.Messages[2].ToolCalls) != 1 {
		t.Error("assistant message should have 1 tool call")
	}
	if oai.Messages[3].Role != "tool" {
		t.Error("fourth message should be tool (tool_result)")
	}
	if oai.Messages[3].ToolCallID != "c1" {
		t.Errorf("tool call ID = %q, want c1", oai.Messages[3].ToolCallID)
	}
	if len(oai.Tools) != 1 {
		t.Errorf("tools count = %d, want 1", len(oai.Tools))
	}
	if oai.Tools[0].Function.Name != "bash" {
		t.Errorf("tool name = %q, want bash", oai.Tools[0].Function.Name)
	}
}

// TestOpenAIProvider_BuildRequest_PipelineShapedToolResult verifies the OpenAI
// adapter correctly serializes tool results in the shape the real agent pipeline
// actually emits. BuildMessages (context.go) converts a tool_result session
// message into CompletionMessage{Role: "user", ToolUseID: ...} — the Anthropic
// internal convention. The OpenAI adapter must translate that into a
// role:"tool" message with tool_call_id, or the provider returns HTTP 400 on
// the second iteration of any tool-using conversation.
func TestOpenAIProvider_BuildRequest_PipelineShapedToolResult(t *testing.T) {
	p := NewOpenAIProvider("", "gpt-4", "")
	req := CompletionRequest{
		Messages: []CompletionMessage{
			{Role: "user", Content: "run ls"},
			{Role: "assistant", Content: "", ToolBlocks: []ToolUseBlock{
				{ID: "call_42", Name: "bash", Input: `{"cmd":"ls"}`},
			}},
			// This is exactly what BuildMessages emits for a tool result:
			// role "user" with ToolUseID set, NOT role "tool_result".
			{Role: "user", Content: "file1\nfile2", ToolUseID: "call_42"},
		},
	}

	oai := p.buildRequest(req, false)

	if len(oai.Messages) != 3 {
		t.Fatalf("message count = %d, want 3 (user + assistant + tool)", len(oai.Messages))
	}
	if oai.Messages[1].Role != "assistant" || len(oai.Messages[1].ToolCalls) != 1 {
		t.Fatalf("second message should be assistant with 1 tool call, got role=%q calls=%d",
			oai.Messages[1].Role, len(oai.Messages[1].ToolCalls))
	}
	if oai.Messages[2].Role != "tool" {
		t.Errorf("tool result must serialize as role %q, got %q", "tool", oai.Messages[2].Role)
	}
	if oai.Messages[2].ToolCallID != "call_42" {
		t.Errorf("tool_call_id = %q, want call_42", oai.Messages[2].ToolCallID)
	}
	if oai.Messages[2].Content != "file1\nfile2" {
		t.Errorf("tool content = %q, want file1\\nfile2", oai.Messages[2].Content)
	}
}

func TestOpenAIProvider_Stream_TextOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := NewOpenAIProvider("", "gpt-4", server.URL)
	iter, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []CompletionMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()

	var text string
	for {
		d, err := iter.Next()
		if d.Done {
			if d.StopReason != StopEndTurn {
				t.Errorf("stop_reason = %q, want end_turn", d.StopReason)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		text += d.Text
	}

	if text != "Hello world" {
		t.Errorf("streamed text = %q, want %q", text, "Hello world")
	}
}

func TestOpenAIProvider_Stream_ToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"cmd\":"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := NewOpenAIProvider("", "gpt-4", server.URL)
	iter, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []CompletionMessage{{Role: "user", Content: "list files"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()

	var final StreamDelta
	for {
		d, err := iter.Next()
		if d.Done {
			final = d
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	if final.StopReason != StopToolUse {
		t.Errorf("stop_reason = %q, want tool_use", final.StopReason)
	}
	if len(final.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(final.ToolCalls))
	}
	if final.ToolCalls[0].Name != "bash" {
		t.Errorf("tool name = %q, want bash", final.ToolCalls[0].Name)
	}
	if final.ToolCalls[0].Input != `{"cmd":"ls"}` {
		t.Errorf("tool args = %q, want {\"cmd\":\"ls\"}", final.ToolCalls[0].Input)
	}
}

// TestOpenAIProvider_Stream_MultipleToolCalls_IndexKeyed verifies that parallel
// tool calls streamed by OpenAI/DeepSeek are accumulated by their "index" field.
// Argument-delta chunks carry only the index (no id/name); the accumulator must
// route each fragment to the matching call. The old code keyed off insertion
// order and routed id-less fragments to the most recent call, corrupting any
// response with 2+ tool calls — which unified_loop produces routinely.
func TestOpenAIProvider_Stream_MultipleToolCalls_IndexKeyed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"write","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"path\":\"b\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := NewOpenAIProvider("", "gpt-4", server.URL)
	iter, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []CompletionMessage{{Role: "user", Content: "do two things"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()

	var final StreamDelta
	for {
		d, err := iter.Next()
		if d.Done {
			final = d
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(final.ToolCalls) != 2 {
		t.Fatalf("tool_calls = %d, want 2", len(final.ToolCalls))
	}
	if final.ToolCalls[0].ID != "call_a" || final.ToolCalls[0].Name != "read" {
		t.Errorf("call[0] = %q/%q, want call_a/read", final.ToolCalls[0].ID, final.ToolCalls[0].Name)
	}
	if final.ToolCalls[0].Input != `{"path":"a"}` {
		t.Errorf("call[0] args = %q, want {\"path\":\"a\"}", final.ToolCalls[0].Input)
	}
	if final.ToolCalls[1].ID != "call_b" || final.ToolCalls[1].Name != "write" {
		t.Errorf("call[1] = %q/%q, want call_b/write", final.ToolCalls[1].ID, final.ToolCalls[1].Name)
	}
	if final.ToolCalls[1].Input != `{"path":"b"}` {
		t.Errorf("call[1] args = %q, want {\"path\":\"b\"}", final.ToolCalls[1].Input)
	}
}

func TestOpenAIProvider_DefaultBaseURL(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-4", "")
	if p.baseURL != defaultOpenAIURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, defaultOpenAIURL)
	}
}

func TestContentString(t *testing.T) {
	if contentString(nil) != "" {
		t.Error("nil should return empty string")
	}
	if contentString("hello") != "hello" {
		t.Error("string should pass through")
	}
	if contentString(42) != "42" {
		t.Error("non-string should use Sprintf")
	}
}
