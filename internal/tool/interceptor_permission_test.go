package tool

import (
	"context"
	"strings"
	"testing"
)

type mockNotifier struct {
	called   bool
	lastCall *ToolCall
}

func (n *mockNotifier) NotifyToolExecution(_ context.Context, call *ToolCall) error {
	n.called = true
	n.lastCall = call
	return nil
}

type mockApprover struct {
	approve bool
	called  bool
}

func (a *mockApprover) RequestApproval(_ context.Context, _ *ToolCall) (bool, error) {
	a.called = true
	return a.approve, nil
}

type mockPermissionAuditSink struct {
	entries []mockPermissionAuditEntry
}

type mockPermissionAuditEntry struct {
	sessionID    string
	toolName     string
	inputSummary string
	action       string
	matchedRule  string
	reason       string
}

type mockPermissionDecisionReporter struct {
	records []PermissionDecisionRecord
}

func (r *mockPermissionDecisionReporter) ReportPermissionDecision(_ context.Context, record PermissionDecisionRecord) {
	r.records = append(r.records, record)
}

func (s *mockPermissionAuditSink) InsertAuditLog(_ context.Context, sessionID, toolName, inputSummary, action, matchedRule, reason string) error {
	s.entries = append(s.entries, mockPermissionAuditEntry{
		sessionID:    sessionID,
		toolName:     toolName,
		inputSummary: inputSummary,
		action:       action,
		matchedRule:  matchedRule,
		reason:       reason,
	})
	return nil
}

func TestPermissionInterceptorReportsDecisions(t *testing.T) {
	reporter := &mockPermissionDecisionReporter{}
	interceptor := NewPermissionInterceptor(makeEngine("deny"), WithPermissionDecisionReporter(reporter))

	res, err := interceptor.Intercept(context.Background(), &ToolCall{
		ToolName:  "bash",
		Input:     "rm file",
		SessionID: "sess_1",
	}, func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "should not run"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected deny error")
	}
	if len(reporter.records) != 1 {
		t.Fatalf("records = %#v", reporter.records)
	}
	got := reporter.records[0]
	if got.SessionID != "sess_1" || got.ToolName != "bash" || got.Action != "deny" {
		t.Fatalf("record = %#v", got)
	}
}

func makeEngine(action string) *PermissionEngine {
	return NewPermissionEngine([]PermissionRule{
		{Tool: "*", Action: action},
	}, "none", nil)
}

func TestPermissionInterceptor_None(t *testing.T) {
	notifier := &mockNotifier{}
	interceptor := NewPermissionInterceptor(makeEngine("none"), WithNotifier(notifier))

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "ok"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "ls"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called")
	}
	if res.Output != "ok" {
		t.Fatalf("expected output 'ok', got %q", res.Output)
	}
	if notifier.called {
		t.Fatal("notifier should NOT be called for action=none")
	}
}

func TestPermissionInterceptor_Notify(t *testing.T) {
	notifier := &mockNotifier{}
	interceptor := NewPermissionInterceptor(makeEngine("notify"), WithNotifier(notifier))

	nextCalled := false
	call := &ToolCall{ToolName: "bash", Input: "echo hello"}
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "hello"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), call, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called")
	}
	if res.Output != "hello" {
		t.Fatalf("expected output 'hello', got %q", res.Output)
	}
	if !notifier.called {
		t.Fatal("notifier should be called for action=notify")
	}
	if notifier.lastCall != call {
		t.Fatal("notifier should receive the original ToolCall")
	}
}

func TestPermissionInterceptor_Deny(t *testing.T) {
	interceptor := NewPermissionInterceptor(makeEngine("deny"))

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "rm -rf /"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called for action=deny")
	}
	if res.Error == "" {
		t.Fatal("expected error in result for denied tool")
	}
}

func TestPermissionInterceptor_ApproveGranted(t *testing.T) {
	approver := &mockApprover{approve: true}
	interceptor := NewPermissionInterceptor(makeEngine("approve"), WithApprover(approver))

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "approved"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "deploy"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Fatal("expected next() to be called when approval granted")
	}
	if res.Output != "approved" {
		t.Fatalf("expected output 'approved', got %q", res.Output)
	}
}

func TestPermissionInterceptor_ApproveDenied(t *testing.T) {
	approver := &mockApprover{approve: false}
	interceptor := NewPermissionInterceptor(makeEngine("approve"), WithApprover(approver))

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "deploy"}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called when approval denied")
	}
	if res.Error == "" {
		t.Fatal("expected error in result when approval denied")
	}
}

func TestPermissionInterceptor_ApproveWithoutApproverDenies(t *testing.T) {
	interceptor := NewPermissionInterceptor(makeEngine("approve"))

	nextCalled := false
	res, err := interceptor.Intercept(context.Background(), &ToolCall{ToolName: "bash", Input: "deploy"}, func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("next() should NOT be called without approver")
	}
	if res.Error == "" || !strings.Contains(res.Error, "no approver") {
		t.Fatalf("expected no approver error, got %#v", res)
	}
}

func TestPermissionInterceptor_ScheduledDestructiveCannotBypassApproval(t *testing.T) {
	engine := NewPermissionEngine([]PermissionRule{
		{Tool: "bash", Pattern: "git *", Action: "none"},
	}, "none", nil)
	interceptor := NewPermissionInterceptor(engine)
	ctx := WithChannelClass(context.Background(), ToolChannelScheduled)

	nextCalled := false
	res, err := interceptor.Intercept(ctx, &ToolCall{
		ToolName: "bash",
		Input:    `{"command":"git commit -m scheduled"}`,
		Capabilities: ToolCapabilities{
			IsDestructive: true,
		},
	}, func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextCalled {
		t.Fatal("scheduled destructive tool should not execute without approver")
	}
	if res.Error == "" || !strings.Contains(res.Error, "no approver") {
		t.Fatalf("expected no approver denial, got %#v", res)
	}
}

func TestPermissionInterceptor_UsesToolCapabilities(t *testing.T) {
	approver := &mockApprover{approve: false}
	interceptor := NewPermissionInterceptor(NewPermissionEngine(nil, "none", nil), WithApprover(approver))

	nextCalled := false
	next := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		nextCalled = true
		return &ToolResult{Output: "should not reach"}, nil
	}

	res, err := interceptor.Intercept(context.Background(), &ToolCall{
		ToolName: "custom_destructive_tool",
		Input:    "{}",
		Capabilities: ToolCapabilities{
			IsDestructive: true,
		},
	}, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approver.called {
		t.Fatal("expected destructive capability to require approval")
	}
	if nextCalled {
		t.Fatal("next() should not be called when destructive tool approval is denied")
	}
	if res.Error == "" {
		t.Fatal("expected denial error in result")
	}
}

func TestPermissionInterceptor_AuditsApprovalDecision(t *testing.T) {
	audit := &mockPermissionAuditSink{}
	approver := &mockApprover{approve: true}
	interceptor := NewPermissionInterceptor(makeEngine("approve"),
		WithApprover(approver),
		WithPermissionAuditSink(audit),
	)

	res, err := interceptor.Intercept(WithChannelClass(context.Background(), ToolChannelRemote), &ToolCall{
		ToolName:  "bash",
		Input:     `{"command":"echo ok"}`,
		SessionID: "sess_123",
	}, func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected tool error: %s", res.Error)
	}
	if len(audit.entries) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(audit.entries))
	}
	if audit.entries[0].action != "approve" || audit.entries[1].action != "approved" {
		t.Fatalf("unexpected audit actions: %+v", audit.entries)
	}
	if !strings.Contains(audit.entries[0].inputSummary, "channel_class=remote") {
		t.Fatalf("input summary missing channel class: %q", audit.entries[0].inputSummary)
	}
}
