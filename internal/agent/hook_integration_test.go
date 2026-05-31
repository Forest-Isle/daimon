package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/hook"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// --- Mock hook handlers ---

// denyHookHandler is a PreToolUse handler that denies all tool calls.
type denyHookHandler struct {
	reason string
}

func (h *denyHookHandler) OnPreToolUse(_ context.Context, _ hook.PreToolUseEvent) (hook.PreToolUseResult, error) {
	return hook.PreToolUseResult{
		Action: "deny",
		Reason: h.reason,
	}, nil
}

// allowHookHandler is a PreToolUse handler that allows all tool calls (skips approval).
type allowHookHandler struct{}

func (h *allowHookHandler) OnPreToolUse(_ context.Context, _ hook.PreToolUseEvent) (hook.PreToolUseResult, error) {
	return hook.PreToolUseResult{
		Action: "allow",
	}, nil
}

// trackingPostHookHandler is a PostToolUse handler that tracks calls.
type trackingPostHookHandler struct {
	callCount atomic.Int32
	lastEvent hook.PostToolUseEvent
}

func (h *trackingPostHookHandler) OnPostToolUse(_ context.Context, event hook.PostToolUseEvent) (hook.PostToolUseResult, error) {
	h.callCount.Add(1)
	h.lastEvent = event
	return hook.PostToolUseResult{}, nil
}

// contextInjectorHandler is an OnUserMessage handler that injects context.
type contextInjectorHandler struct {
	context string
}

func (h *contextInjectorHandler) OnUserMessage(_ context.Context, _ hook.OnUserMessageEvent) (hook.OnUserMessageResult, error) {
	return hook.OnUserMessageResult{
		InjectedContext: []string{h.context},
	}, nil
}

// --- Mock tool for hook tests ---

type hookTestTool struct {
	name      string
	execCount atomic.Int32
}

func (t *hookTestTool) Name() string                                    { return t.name }
func (t *hookTestTool) Description() string                             { return "test tool for hooks" }
func (t *hookTestTool) InputSchema() map[string]any                     { return nil }
func (t *hookTestTool) RequiresApproval() bool                          { return false }
func (t *hookTestTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	t.execCount.Add(1)
	return tool.Result{Output: "executed " + t.name}, nil
}

func TestPreToolUseDenyPreventsExecution(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()

	mockTool := &hookTestTool{name: "test_tool"}
	registry.Register(mockTool)

	hookMgr := hook.NewManager()
	hookMgr.RegisterPreToolUse(&denyHookHandler{reason: "blocked by test"})

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Tools: registry,
			DB:    db,
			Cfg:   config.AgentConfig{},
		},
		Security: SecurityDeps{
			HookMgr: hookMgr,
		},
	}.WithDefaults())

	sess := concurrentTestSession()
	tc := ToolUseBlock{ID: "tc_1", Name: "test_tool", Input: "{}"}

	result := rt.executeToolCall(context.Background(), nil, sess, channel.MessageTarget{}, tc)

	// Tool should NOT have been executed
	if mockTool.execCount.Load() != 0 {
		t.Errorf("tool was executed %d times, expected 0 (should have been denied by hook)", mockTool.execCount.Load())
	}

	// Result should indicate denial
	if result.status != "denied" {
		t.Errorf("expected status 'denied', got '%s'", result.status)
	}

	if !strings.Contains(result.output, "denied by hook: blocked by test") {
		t.Errorf("expected output to contain denial reason, got: %s", result.output)
	}
	if !strings.Contains(result.output, "[Recovery Hint:") {
		t.Errorf("expected output to contain recovery hint, got: %s", result.output)
	}
}

func TestPreToolUseAllowSkipsApproval(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()

	// Tool that requires approval
	mockTool := &approvalTestTool{name: "approval_tool"}
	registry.Register(mockTool)

	hookMgr := hook.NewManager()
	hookMgr.RegisterPreToolUse(&allowHookHandler{})

	// Approval func that always denies (should be skipped)
	denyApproval := func(_ context.Context, _ channel.Channel, _ channel.MessageTarget, _ string, _ string) (bool, error) {
		return false, nil
	}

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Tools: registry,
			DB:    db,
			Cfg:   config.AgentConfig{},
		},
		Security: SecurityDeps{
			HookMgr: hookMgr,
		},
	}.WithDefaults())
	rt.SetApprovalFunc(denyApproval)

	sess := concurrentTestSession()
	tc := ToolUseBlock{ID: "tc_1", Name: "approval_tool", Input: "{}"}

	result := rt.executeToolCall(context.Background(), nil, sess, channel.MessageTarget{}, tc)

	// Tool SHOULD have been executed (hook allowed, skipping approval)
	if mockTool.execCount.Load() != 1 {
		t.Errorf("tool was executed %d times, expected 1 (hook should have skipped approval)", mockTool.execCount.Load())
	}

	if result.status != "success" {
		t.Errorf("expected status 'success', got '%s'", result.status)
	}
}

// approvalTestTool requires approval.
type approvalTestTool struct {
	name      string
	execCount atomic.Int32
}

func (t *approvalTestTool) Name() string                                    { return t.name }
func (t *approvalTestTool) Description() string                             { return "test tool requiring approval" }
func (t *approvalTestTool) InputSchema() map[string]any                     { return nil }
func (t *approvalTestTool) RequiresApproval() bool                          { return true }
func (t *approvalTestTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	t.execCount.Add(1)
	return tool.Result{Output: "executed " + t.name}, nil
}

func TestPostToolUseAuditHandlerCalled(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()

	mockTool := &hookTestTool{name: "audited_tool"}
	registry.Register(mockTool)

	tracker := &trackingPostHookHandler{}
	hookMgr := hook.NewManager()
	hookMgr.RegisterPostToolUse(tracker)

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Tools: registry,
			DB:    db,
			Cfg:   config.AgentConfig{},
		},
		Security: SecurityDeps{
			HookMgr: hookMgr,
		},
	}.WithDefaults())

	sess := concurrentTestSession()
	tc := ToolUseBlock{ID: "tc_1", Name: "audited_tool", Input: `{"cmd":"test"}`}

	result := rt.executeToolCall(context.Background(), nil, sess, channel.MessageTarget{}, tc)

	// Tool should have been executed
	if mockTool.execCount.Load() != 1 {
		t.Errorf("tool was executed %d times, expected 1", mockTool.execCount.Load())
	}

	// PostToolUse handler should have been called
	if tracker.callCount.Load() != 1 {
		t.Errorf("post-tool-use handler called %d times, expected 1", tracker.callCount.Load())
	}

	// Verify event data
	if tracker.lastEvent.ToolName != "audited_tool" {
		t.Errorf("expected tool name 'audited_tool', got '%s'", tracker.lastEvent.ToolName)
	}
	if tracker.lastEvent.Status != "success" {
		t.Errorf("expected status 'success', got '%s'", tracker.lastEvent.Status)
	}
	if result.output != "executed audited_tool" {
		t.Errorf("unexpected output: %s", result.output)
	}
}

func TestOnUserMessageContextInjection(t *testing.T) {
	hookMgr := hook.NewManager()
	hookMgr.RegisterOnUserMessage(&contextInjectorHandler{
		context: "Current time: 2026-04-02T12:00:00Z",
	})

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Cfg: config.AgentConfig{
				SystemPrompt: "You are a helpful assistant.",
			},
		},
		Security: SecurityDeps{
			HookMgr: hookMgr,
		},
	}.WithDefaults())

	// Build system prompt
	ctx := context.Background()
	systemPrompt := rt.buildSystemPrompt(ctx, "What time is it?")

	// Simulate OnUserMessage hook firing (as done in HandleMessage)
	if rt.deps.Security.HookMgr != nil && rt.deps.Security.HookMgr.HasOnUserMessageHandlers() {
		msgResult, _ := rt.deps.Security.HookMgr.FireOnUserMessage(ctx, hook.OnUserMessageEvent{
			Channel:   "test",
			ChannelID: "test-channel",
			UserID:    "user-1",
			Text:      "What time is it?",
		})
		if len(msgResult.InjectedContext) > 0 {
			for _, ic := range msgResult.InjectedContext {
				systemPrompt += "\n\n## Environment Context\n" + ic
			}
		}
	}

	// Verify injected context appears in system prompt
	if !containsSubstring(systemPrompt, "Current time: 2026-04-02T12:00:00Z") {
		t.Errorf("expected injected context in system prompt, got: %s", systemPrompt)
	}

	if !containsSubstring(systemPrompt, "## Environment Context") {
		t.Errorf("expected '## Environment Context' section in system prompt, got: %s", systemPrompt)
	}
}

func TestPermissionEngineDenyPreventsExecution(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()

	mockTool := &hookTestTool{name: "bash"}
	registry.Register(mockTool)

	// Permission engine that denies bash with rm commands
	rules := []tool.PermissionRule{
		{Tool: "bash", Pattern: "rm *", Action: "deny"},
	}
	permEngine := tool.NewPermissionEngine(rules, "ask", nil)

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Tools: registry,
			DB:    db,
			Cfg:   config.AgentConfig{},
		},
		Security: SecurityDeps{
			PermEngine: permEngine,
		},
	}.WithDefaults())

	sess := concurrentTestSession()
	tc := ToolUseBlock{ID: "tc_1", Name: "bash", Input: `{"command":"rm -rf /tmp/test"}`}

	result := rt.executeToolCall(context.Background(), nil, sess, channel.MessageTarget{}, tc)

	// Tool should NOT have been executed
	if mockTool.execCount.Load() != 0 {
		t.Errorf("tool was executed %d times, expected 0 (should have been denied by permission engine)", mockTool.execCount.Load())
	}

	if result.status != "denied" {
		t.Errorf("expected status 'denied', got '%s'", result.status)
	}
}

func TestHookAndPermissionEngineIntegration(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()

	mockTool := &hookTestTool{name: "test_tool"}
	registry.Register(mockTool)

	// Hook that passes through (doesn't deny or allow)
	hookMgr := hook.NewManager()
	tracker := &trackingPostHookHandler{}
	hookMgr.RegisterPostToolUse(tracker)

	// Permission engine allows all by default
	permEngine := tool.NewPermissionEngine(nil, "allow", nil)

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Tools: registry,
			DB:    db,
			Cfg:   config.AgentConfig{},
		},
		Security: SecurityDeps{
			HookMgr:    hookMgr,
			PermEngine: permEngine,
		},
	}.WithDefaults())

	sess := concurrentTestSession()
	tc := ToolUseBlock{ID: "tc_1", Name: "test_tool", Input: "{}"}

	result := rt.executeToolCall(context.Background(), nil, sess, channel.MessageTarget{}, tc)

	// Tool should have been executed
	if mockTool.execCount.Load() != 1 {
		t.Errorf("tool was executed %d times, expected 1", mockTool.execCount.Load())
	}
	if result.status != "success" {
		t.Errorf("expected status 'success', got '%s'", result.status)
	}
	// Post hook should have been called
	if tracker.callCount.Load() != 1 {
		t.Errorf("post-tool-use handler called %d times, expected 1", tracker.callCount.Load())
	}
}

func TestConcurrentExecutionWithHooks(t *testing.T) {
	db := newTestDB(t)
	registry := tool.NewRegistry()

	tools := make([]*hookTestTool, 3)
	for i := 0; i < 3; i++ {
		tools[i] = &hookTestTool{name: fmt.Sprintf("tool_%d", i)}
		registry.Register(tools[i])
	}

	tracker := &trackingPostHookHandler{}
	hookMgr := hook.NewManager()
	hookMgr.RegisterPostToolUse(tracker)

	rt := NewRuntime(AgentDeps{
		Core: CoreDeps{
			Tools:    registry,
			DB:       db,
			Cfg:      config.AgentConfig{},
			ToolsCfg: config.ToolsConfig{
				ConcurrentExecution: config.ConcurrentExecutionConfig{
					Enabled:        true,
					MaxConcurrency: 4,
				},
			},
		},
		Security: SecurityDeps{
			HookMgr: hookMgr,
		},
	}.WithDefaults())

	sess := concurrentTestSession()
	toolCalls := []ToolUseBlock{
		{ID: "tc_0", Name: "tool_0", Input: "{}"},
		{ID: "tc_1", Name: "tool_1", Input: "{}"},
		{ID: "tc_2", Name: "tool_2", Input: "{}"},
	}

	rt.executeTools(context.Background(), nil, sess, channel.MessageTarget{}, toolCalls)

	// All tools should have been executed
	for i, tl := range tools {
		if tl.execCount.Load() != 1 {
			t.Errorf("tool_%d executed %d times, expected 1", i, tl.execCount.Load())
		}
	}

	// Post hook should have been called for each tool
	if tracker.callCount.Load() != 3 {
		t.Errorf("post-tool-use handler called %d times, expected 3", tracker.callCount.Load())
	}

	// Verify results were added to session
	history := sess.History()
	toolResults := 0
	for _, m := range history {
		if m.Role == "tool_result" {
			toolResults++
		}
	}
	if toolResults != 3 {
		t.Errorf("expected 3 tool_result messages, got %d", toolResults)
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
