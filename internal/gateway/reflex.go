package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/Forest-Isle/daimon/internal/workflow"
)

const defaultReflexTimeout = 60 * time.Second

type reflexWorkflow struct {
	id      string
	spec    []byte
	timeout time.Duration
}

type reflexExecutor struct {
	workflows map[string]reflexWorkflow
	tools     *tool.Registry
	chain     *tool.InterceptorChain
}

func newReflexExecutor(defs map[string]config.ReflexConfig, tools *tool.Registry, chain *tool.InterceptorChain) (*reflexExecutor, error) {
	ex := &reflexExecutor{
		workflows: make(map[string]reflexWorkflow, len(defs)),
		tools:     tools,
		chain:     chain,
	}
	for id, def := range defs {
		rw, err := loadReflexWorkflow(id, def)
		if err != nil {
			return nil, err
		}
		ex.workflows[id] = rw
	}
	return ex, nil
}

func loadReflexWorkflow(id string, def config.ReflexConfig) (reflexWorkflow, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return reflexWorkflow{}, fmt.Errorf("reflex id must not be empty")
	}
	data := []byte(strings.TrimSpace(def.Workflow))
	if len(data) == 0 {
		path := cleanReflexPath(def.WorkflowPath)
		read, err := os.ReadFile(path)
		if err != nil {
			return reflexWorkflow{}, fmt.Errorf("reflex %q: read workflow_path %s: %w", id, path, err)
		}
		data = read
	}
	if _, err := workflow.ParseSpec(data); err != nil {
		return reflexWorkflow{}, fmt.Errorf("reflex %q: %w", id, err)
	}
	timeout := time.Duration(def.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultReflexTimeout
	}
	return reflexWorkflow{id: id, spec: data, timeout: timeout}, nil
}

func cleanReflexPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path)
}

func (e *reflexExecutor) Execute(ctx context.Context, reflexID string, ev heart.Event) (*workflow.Run, error) {
	if e == nil {
		return nil, fmt.Errorf("reflex executor unavailable")
	}
	reflexID = strings.TrimSpace(reflexID)
	if reflexID == "" {
		return nil, fmt.Errorf("reflex_id is required for action=reflex")
	}
	rw, ok := e.workflows[reflexID]
	if !ok {
		return nil, fmt.Errorf("unknown reflex_id %q", reflexID)
	}
	spec, err := workflow.ParseSpec(rw.spec)
	if err != nil {
		return nil, fmt.Errorf("reflex %q: %w", reflexID, err)
	}
	ctx, cancel := context.WithTimeout(ctx, rw.timeout)
	defer cancel()
	ctx = tool.WithChannelClass(ctx, tool.ToolChannelInternal)
	ctx = tool.WithSessionID(ctx, reflexSessionID(reflexID, ev))
	if tool.WorkDirFromContext(ctx) == "" {
		if cwd, err := os.Getwd(); err == nil && cwd != "" {
			ctx = tool.WithWorkDir(ctx, cwd)
		}
	}
	run, execErr := (&workflow.Executor{
		Runner:      &reflexStepRunner{reflexID: reflexID, event: ev, tools: e.tools, chain: e.chain},
		MaxParallel: 3,
		// Do not attach the workflow replay cache here: the executor's cache key is
		// derived from the static spec, while reflex tool input may interpolate the
		// current event payload. Reusing a prior event's result would be incorrect.
	}).Execute(ctx, spec)
	if execErr != nil {
		return run, execErr
	}
	if run != nil && run.Status == workflow.RunFailed {
		if failure := firstWorkflowFailure(run); failure != "" {
			return run, fmt.Errorf("reflex %q workflow failed: %s", reflexID, failure)
		}
		return run, fmt.Errorf("reflex %q workflow failed", reflexID)
	}
	return run, nil
}

func firstWorkflowFailure(run *workflow.Run) string {
	if run == nil {
		return ""
	}
	for _, result := range run.Results {
		if result.Error != "" {
			return result.Error
		}
		if result.Status == workflow.StatusError && result.Summary != "" {
			return result.Summary
		}
	}
	return ""
}

func reflexSessionID(reflexID string, ev heart.Event) string {
	id := ev.ID
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "reflex_" + safeReflexID(reflexID) + "_" + safeReflexID(id)
}

func safeReflexID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-':
			return r
		default:
			return '_'
		}
	}, s)
}

type reflexStepRunner struct {
	reflexID string
	event    heart.Event
	tools    *tool.Registry
	chain    *tool.InterceptorChain
}

func (r *reflexStepRunner) RunStep(ctx context.Context, step workflow.Step, input workflow.StepInput) (workflow.StepOutput, error) {
	if step.Type != workflow.StepTypeTool {
		return workflow.StepOutput{Status: workflow.StatusError}, fmt.Errorf("reflex workflow step %q uses %q; reflexes only run deterministic tool steps", step.ID, step.Type)
	}
	return r.runToolStep(ctx, step, input)
}

func (r *reflexStepRunner) runToolStep(ctx context.Context, step workflow.Step, input workflow.StepInput) (workflow.StepOutput, error) {
	if r.tools == nil {
		return workflow.StepOutput{Status: workflow.StatusError}, fmt.Errorf("reflex tool registry unavailable")
	}
	rendered := expandReflexInput(step.Input, r.reflexID, r.event, input)
	data, err := json.Marshal(rendered)
	if err != nil {
		return workflow.StepOutput{Status: workflow.StatusError}, fmt.Errorf("marshal reflex tool input: %w", err)
	}
	t, err := r.tools.Get(step.Tool)
	if err != nil {
		return workflow.StepOutput{Status: workflow.StatusError}, err
	}
	call := &tool.ToolCall{
		ToolName:  step.Tool,
		Input:     string(data),
		SessionID: tool.SessionIDFromContext(ctx),
		Metadata: map[string]string{
			"reflex_id":  r.reflexID,
			"event_id":   r.event.ID,
			"event_kind": r.event.Kind,
		},
		Capabilities: tool.GetCapabilities(t),
	}
	chain := r.chain
	if chain == nil {
		chain = tool.NewInterceptorChain(nil)
	}
	result, execErr := chain.Execute(ctx, call, func(ctx context.Context, call *tool.ToolCall) (*tool.ToolResult, error) {
		t, getErr := r.tools.Get(call.ToolName)
		if getErr != nil {
			return &tool.ToolResult{Error: getErr.Error()}, nil
		}
		res, runErr := t.Execute(ctx, []byte(call.Input))
		if runErr != nil {
			return &tool.ToolResult{Error: runErr.Error()}, nil
		}
		return &tool.ToolResult{Output: res.Output, Error: res.Error, Metadata: stringifyToolMetadata(res.Metadata)}, nil
	})
	if execErr != nil {
		return workflow.StepOutput{Status: workflow.StatusError}, execErr
	}
	output := ""
	errText := ""
	metadata := map[string]any{
		"tool":              step.Tool,
		"reflex_id":         r.reflexID,
		"event_id":          r.event.ID,
		"workflow_name":     input.WorkflowName,
		"workflow_stage_id": input.StageID,
	}
	if result != nil {
		output = result.Output
		errText = result.Error
		for k, v := range result.Metadata {
			metadata[k] = v
		}
	}
	if errText != "" {
		return workflow.StepOutput{Status: workflow.StatusError, Summary: errText, Output: output, Metadata: metadata}, fmt.Errorf("reflex tool %s: %s", step.Tool, errText)
	}
	return workflow.StepOutput{Status: workflow.StatusSuccess, Summary: summarizeToolOutput(output), Output: output, Metadata: metadata}, nil
}

func stringifyToolMetadata(in map[string]any) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func summarizeToolOutput(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:157] + "..."
	}
	return s
}

func expandReflexInput(in map[string]any, reflexID string, ev heart.Event, step workflow.StepInput) map[string]any {
	out := make(map[string]any, len(in))
	vars := map[string]string{
		"${reflex.id}":         reflexID,
		"${event.id}":          ev.ID,
		"${event.source}":      ev.Source,
		"${event.kind}":        ev.Kind,
		"${event.payload}":     ev.Payload,
		"${event.occurred_at}": ev.OccurredAt,
		"${event.dedup_key}":   ev.DedupKey,
		"${workflow.name}":     step.WorkflowName,
		"${workflow.hash}":     step.WorkflowHash,
		"${workflow.stage_id}": step.StageID,
	}
	for k, v := range in {
		out[k] = expandReflexValue(v, vars)
	}
	return out
}

func expandReflexValue(v any, vars map[string]string) any {
	switch x := v.(type) {
	case string:
		for key, val := range vars {
			x = strings.ReplaceAll(x, key, val)
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = expandReflexValue(x[i], vars)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = expandReflexValue(v, vars)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = expandReflexValue(v, vars)
		}
		return out
	default:
		return v
	}
}
