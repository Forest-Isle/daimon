package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type reflexCaptureTool struct {
	name     string
	input    string
	executed bool
	readOnly bool
}

func (t *reflexCaptureTool) Name() string                { return t.name }
func (t *reflexCaptureTool) Description() string         { return "capture reflex input" }
func (t *reflexCaptureTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *reflexCaptureTool) RequiresApproval() bool      { return false }
func (t *reflexCaptureTool) Capabilities() tool.ToolCapabilities {
	return tool.ToolCapabilities{IsReadOnly: t.readOnly}
}
func (t *reflexCaptureTool) Execute(_ context.Context, input []byte) (tool.Result, error) {
	t.executed = true
	t.input = string(input)
	return tool.Result{Output: string(input)}, nil
}

func TestReflexExecutorRunsConfiguredToolWorkflow(t *testing.T) {
	reg := tool.NewRegistry()
	capture := &reflexCaptureTool{name: "capture", readOnly: true}
	reg.Register(capture)

	ex, err := newReflexExecutor(map[string]config.ReflexConfig{
		"mail_digest": {Workflow: `
name: mail-digest-reflex
stages:
  - id: run
    steps:
      - id: capture_event
        type: tool
        tool: capture
        input:
          text: "kind=${event.kind} payload=${event.payload} reflex=${reflex.id}"
`},
	}, reg, tool.NewInterceptorChain(nil))
	if err != nil {
		t.Fatalf("newReflexExecutor() error = %v", err)
	}

	run, err := ex.Execute(context.Background(), "mail_digest", heart.Event{
		ID: "evt-1", Source: "mail", Kind: "mail.received", Payload: "invoice arrived",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if run == nil || run.WorkflowName != "mail-digest-reflex" {
		t.Fatalf("run = %#v", run)
	}
	if !capture.executed {
		t.Fatal("configured reflex tool did not execute")
	}
	for _, want := range []string{"mail.received", "invoice arrived", "mail_digest"} {
		if !strings.Contains(capture.input, want) {
			t.Fatalf("captured input %q missing %q", capture.input, want)
		}
	}
}

func TestReflexExecutorUnknownIDFailsClosed(t *testing.T) {
	ex, err := newReflexExecutor(nil, tool.NewRegistry(), tool.NewInterceptorChain(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Execute(context.Background(), "missing", heart.Event{}); err == nil {
		t.Fatal("unknown reflex id must error")
	}
}

func TestReflexExecutorInternalWriteRequiresApprovalAndDoesNotRun(t *testing.T) {
	reg := tool.NewRegistry()
	write := &reflexCaptureTool{name: "write_like", readOnly: false}
	reg.Register(write)
	permissions := tool.NewPermissionEngine(nil, "none", nil)
	chain := tool.NewInterceptorChain([]tool.ToolInterceptor{tool.NewPermissionInterceptor(permissions)})

	ex, err := newReflexExecutor(map[string]config.ReflexConfig{
		"write_reflex": {Workflow: `
name: write-reflex
stages:
  - id: run
    steps:
      - id: write
        type: tool
        tool: write_like
        input:
          text: "${event.payload}"
`},
	}, reg, chain)
	if err != nil {
		t.Fatal(err)
	}

	run, err := ex.Execute(context.Background(), "write_reflex", heart.Event{ID: "evt-write", Kind: "mail.received", Payload: "write"})
	if err == nil {
		t.Fatal("internal write reflex must fail without an approver")
	}
	if run == nil || run.Status == "" {
		t.Fatalf("failed reflex should still return a workflow run, got %#v", run)
	}
	if write.executed {
		t.Fatal("write-like tool executed despite internal approval requirement")
	}
	if !strings.Contains(err.Error(), "approval required") {
		t.Fatalf("error = %v, want approval required", err)
	}
}

func TestReflexExecutorRejectsAgentSteps(t *testing.T) {
	ex, err := newReflexExecutor(map[string]config.ReflexConfig{
		"agent_reflex": {Workflow: `
name: agent-reflex
stages:
  - id: run
    steps:
      - id: delegate
        type: agent
        agent: researcher
        task: summarize
`},
	}, tool.NewRegistry(), tool.NewInterceptorChain(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ex.Execute(context.Background(), "agent_reflex", heart.Event{ID: "evt-agent"})
	if err == nil {
		t.Fatal("agent step reflex must fail")
	}
	if !strings.Contains(err.Error(), "deterministic tool steps") {
		t.Fatalf("error = %v, want deterministic tool step guard", err)
	}
}
