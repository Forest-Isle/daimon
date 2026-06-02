package mcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Mock: minimal MCPClient that only implements CallTool; all other methods
// panic so any unexpected invocation is caught immediately.
// ---------------------------------------------------------------------------

type mockMCPClient struct {
	callToolFn func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

func (m *mockMCPClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if m.callToolFn != nil {
		return m.callToolFn(ctx, req)
	}
	panic("mockMCPClient.CallTool: not configured")
}

// --- stubs for the rest of client.MCPClient ---

func (m *mockMCPClient) Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) Ping(context.Context) error { panic("unexpected call") }
func (m *mockMCPClient) ListResourcesByPage(context.Context, mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ListResources(context.Context, mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ListResourceTemplatesByPage(context.Context, mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ListResourceTemplates(context.Context, mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ReadResource(context.Context, mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) Subscribe(context.Context, mcp.SubscribeRequest) error {
	panic("unexpected call")
}
func (m *mockMCPClient) Unsubscribe(context.Context, mcp.UnsubscribeRequest) error {
	panic("unexpected call")
}
func (m *mockMCPClient) ListPromptsByPage(context.Context, mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ListPrompts(context.Context, mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) GetPrompt(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ListToolsByPage(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) SetLevel(context.Context, mcp.SetLevelRequest) error {
	panic("unexpected call")
}
func (m *mockMCPClient) Complete(context.Context, mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	panic("unexpected call")
}
func (m *mockMCPClient) Close() error                                              { return nil }
func (m *mockMCPClient) OnNotification(func(notification mcp.JSONRPCNotification)) {}

// ---------------------------------------------------------------------------
// Tests: ToolAdapter property accessors
// ---------------------------------------------------------------------------

func TestToolAdapter_Name(t *testing.T) {
	tests := []struct {
		server, tool, want string
	}{
		{"srv", "tool", "mcp_srv_tool"},
		{"my_server", "my_tool", "mcp_my_server_my_tool"},
		{"a", "b", "mcp_a_b"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			a := NewToolAdapter(&mockMCPClient{}, tt.server, mcp.Tool{Name: tt.tool}, false)
			if got := a.Name(); got != tt.want {
				t.Errorf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolAdapter_Description(t *testing.T) {
	for _, desc := range []string{"a useful tool", "", "multi\nline\ndesc"} {
		t.Run(desc, func(t *testing.T) {
			a := NewToolAdapter(&mockMCPClient{}, "s", mcp.Tool{Description: desc}, false)
			if got := a.Description(); got != desc {
				t.Errorf("Description() = %q, want %q", got, desc)
			}
		})
	}
}

func TestToolAdapter_RequiresApproval(t *testing.T) {
	for _, want := range []bool{true, false} {
		a := NewToolAdapter(&mockMCPClient{}, "s", mcp.Tool{}, want)
		if got := a.RequiresApproval(); got != want {
			t.Errorf("RequiresApproval() = %v, want %v", got, want)
		}
	}
}

func TestToolAdapter_InputSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema mcp.ToolInputSchema
		want   map[string]any
	}{
		{
			name:   "type only",
			schema: mcp.ToolInputSchema{Type: "object"},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			name: "with properties",
			schema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: map[string]any{"p": map[string]any{"type": "string"}},
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{"p": map[string]any{"type": "string"}},
			},
		},
		{
			name: "with required",
			schema: mcp.ToolInputSchema{
				Type:     "object",
				Required: []string{"a", "b"},
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{"a", "b"},
			},
		},
		{
			name: "empty required omitted",
			schema: mcp.ToolInputSchema{
				Type:     "object",
				Required: []string{},
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			name: "nil properties gets default",
			schema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: nil,
			},
			want: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewToolAdapter(&mockMCPClient{}, "s", mcp.Tool{InputSchema: tt.schema}, false)
			got := a.InputSchema()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InputSchema() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: extractText
// ---------------------------------------------------------------------------

func TestExtractText(t *testing.T) {
	tests := []struct {
		name     string
		contents []mcp.Content
		want     string
	}{
		{"nil", nil, ""},
		{"empty", []mcp.Content{}, ""},
		{"single text", []mcp.Content{mcp.TextContent{Text: "hello"}}, "hello"},
		{
			"multiple texts",
			[]mcp.Content{
				mcp.TextContent{Text: "a"},
				mcp.TextContent{Text: "b"},
				mcp.TextContent{Text: "c"},
			},
			"a\nb\nc",
		},
		{
			"mixed content skips non-text",
			[]mcp.Content{
				mcp.TextContent{Text: "t1"},
				mcp.ImageContent{Data: "x", MIMEType: "image/png"},
				mcp.TextContent{Text: "t2"},
			},
			"t1\nt2",
		},
		{
			"only non-text",
			[]mcp.Content{
				mcp.ImageContent{Data: "x", MIMEType: "image/png"},
			},
			"",
		},
		{
			"text with embedded newlines",
			[]mcp.Content{
				mcp.TextContent{Text: "line1\nline2"},
				mcp.TextContent{Text: "line3"},
			},
			"line1\nline2\nline3",
		},
		{
			"unicode",
			[]mcp.Content{
				mcp.TextContent{Text: "你好"},
				mcp.TextContent{Text: "世界"},
			},
			"你好\n世界",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractText(tt.contents); got != tt.want {
				t.Errorf("extractText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractText_LargeInput(t *testing.T) {
	var contents []mcp.Content
	var expected strings.Builder
	for i := 0; i < 100; i++ {
		s := fmt.Sprintf("block-%d", i)
		contents = append(contents, mcp.TextContent{Text: s})
		if i > 0 {
			expected.WriteString("\n")
		}
		expected.WriteString(s)
	}
	if got := extractText(contents); got != expected.String() {
		t.Errorf("length mismatch: got %d, want %d", len(got), expected.Len())
	}
}

// ---------------------------------------------------------------------------
// Tests: Execute
// ---------------------------------------------------------------------------

func TestToolAdapter_Execute_Success(t *testing.T) {
	td := mcp.Tool{Name: "echo"}
	mc := &mockMCPClient{
		callToolFn: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if req.Params.Name != td.Name {
				t.Errorf("CallTool received name %q, want %q", req.Params.Name, td.Name)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Text: "ok"}},
			}, nil
		},
	}
	a := NewToolAdapter(mc, "srv", td, false)

	res, err := a.Execute(context.Background(), []byte(`{"key":"val"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Output != "ok" {
		t.Errorf("Output = %q, want %q", res.Output, "ok")
	}
	if res.Error != "" {
		t.Errorf("Error = %q, want empty", res.Error)
	}
}

func TestToolAdapter_Execute_ToolError(t *testing.T) {
	mc := &mockMCPClient{
		callToolFn: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{mcp.TextContent{Text: "bad input"}},
			}, nil
		},
	}
	a := NewToolAdapter(mc, "srv", mcp.Tool{Name: "t"}, false)

	res, err := a.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "bad input" {
		t.Errorf("Error = %q, want %q", res.Error, "bad input")
	}
	if res.Output != "" {
		t.Errorf("Output = %q, want empty", res.Output)
	}
}

func TestToolAdapter_Execute_TransportError(t *testing.T) {
	mc := &mockMCPClient{
		callToolFn: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New("connection reset")
		},
	}
	a := NewToolAdapter(mc, "srv", mcp.Tool{Name: "t"}, false)

	_, err := a.Execute(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "mcp call mcp_srv_t") {
		t.Errorf("error %q should mention tool name", err.Error())
	}
}

func TestToolAdapter_Execute_InvalidJSON(t *testing.T) {
	a := NewToolAdapter(&mockMCPClient{}, "srv", mcp.Tool{Name: "t"}, false)

	res, err := a.Execute(context.Background(), []byte(`not json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error == "" {
		t.Error("expected non-empty Error for invalid JSON")
	}
}

func TestToolAdapter_Execute_EmptyAndNilInput(t *testing.T) {
	a := NewToolAdapter(&mockMCPClient{}, "srv", mcp.Tool{Name: "t"}, false)

	for _, input := range [][]byte{nil, {}} {
		res, err := a.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("input=%v: unexpected error: %v", input, err)
		}
		if res.Error == "" {
			t.Errorf("input=%v: expected non-empty Error", input)
		}
	}
}

func TestToolAdapter_Execute_ArgumentsPassedThrough(t *testing.T) {
	var received map[string]any
	mc := &mockMCPClient{
		callToolFn: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if args, ok := req.Params.Arguments.(map[string]any); ok {
				received = args
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Text: "ok"}},
			}, nil
		},
	}
	a := NewToolAdapter(mc, "srv", mcp.Tool{Name: "t"}, false)

	_, err := a.Execute(context.Background(), []byte(`{"s":"v","n":42}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{"s": "v", "n": float64(42)}
	if !reflect.DeepEqual(received, want) {
		t.Errorf("arguments = %+v, want %+v", received, want)
	}
}

func TestToolAdapter_Execute_MultipleTextBlocks(t *testing.T) {
	mc := &mockMCPClient{
		callToolFn: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{Text: "a"},
					mcp.TextContent{Text: "b"},
				},
			}, nil
		},
	}
	a := NewToolAdapter(mc, "srv", mcp.Tool{Name: "t"}, false)

	res, err := a.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Output != "a\nb" {
		t.Errorf("Output = %q, want %q", res.Output, "a\nb")
	}
}

func TestToolAdapter_Execute_CanceledContext(t *testing.T) {
	mc := &mockMCPClient{
		callToolFn: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, ctx.Err()
		},
	}
	a := NewToolAdapter(mc, "srv", mcp.Tool{Name: "t"}, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Execute(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}
