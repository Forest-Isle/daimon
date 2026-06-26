# TUI Workflow Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each conversation round's full tool workflow inline in the TUI transcript — name, args, status, result summary, duration — with raw output expandable on a global hotkey.

**Architecture:** Enrich the tool-activity event so it carries a per-invocation id, the result, and the duration end-to-end (tool interceptor → gateway reporter → channel → TUI). The TUI persists each tool call as a `step`-role message in the existing `messages` slice (reusing the `chatRev` render cache), correlated by id, rendered grouped/indented between the user and agent lines. `Ctrl+T` toggles a global expanded view of capped raw output. No per-step cursor.

**Tech Stack:** Go 1.25.11, Bubble Tea / lipgloss TUI, glamour markdown, runewidth.

## Global Constraints

- Go 1.25.11; standard `testing`; tests in-package (not `_test` package).
- Do all work on a feature branch, not `main` (e.g. `git switch -c feat/tui-workflow-trace`).
- `context.Context` is the first parameter; never ignore errors except the existing best-effort `_ =` channel sends.
- The activity reporter is informational and MUST NOT block or alter tool execution.
- Telegram does not implement `ToolActivitySender`; do not add it there.
- Verify with `make build-bin && make vet && make test-short`.
- Match the existing calm palette and glyph-led turn style (`›` user, `⏺` agent).

---

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `internal/tool/interceptor_activity.go` | event struct, per-invocation id, carry result+duration | 1 |
| `internal/tool/interceptor_activity_test.go` | unit-test id correlation + result/duration | 1 |
| `internal/channel/channel.go` | `ToolActivity` transport struct + sender signature | 1 |
| `internal/gateway/tool_activity_reporter.go` | derive result summary, cap output, send | 1 |
| `internal/gateway/tool_activity_reporter_test.go` | unit-test summary derivation + output cap | 1 |
| `internal/gateway/tool_routing_test.go` | update mock + call site to new signatures | 1 |
| `internal/channel/tui/messages.go` | widen `toolActivityMsg` | 1 |
| `internal/channel/tui/adapter.go` | implement new `SendToolActivity` | 1 |
| `internal/channel/tui/model.go` | `workflowStep`, `chatMessage.step`, `stepIndex`, `stepsExpanded` | 2 |
| `internal/channel/tui/model_update.go` | build/update steps; reset on clear | 2 |
| `internal/channel/tui/model_dialogs.go` | reset `stepIndex` on `/clear` | 2 |
| `internal/channel/tui/model_steps_test.go` | step build/correlate test | 2 |
| `internal/channel/tui/styles.go` | step styles | 3 |
| `internal/channel/tui/model_view.go` | collapsed step render, cache bypass, grouping | 3 |
| `internal/channel/tui/model_update.go` | `Ctrl+T` toggle | 4 |
| `internal/channel/tui/model_view.go` | expanded render + status-bar hint | 4 |
| `internal/channel/tui/model_view_test.go` | render assertions (collapsed + expanded) | 3, 4 |

---

## Task 1: Activity plumbing — carry id, result, duration end-to-end

This is one task because the `ToolActivityReporter` and `ToolActivitySender` signature changes ripple across `tool`, `gateway`, and `tui`; splitting them would leave the build broken. The TUI handler stays status-bar-only here (steps land in Task 2).

**Files:**
- Modify: `internal/tool/interceptor_activity.go`
- Modify: `internal/tool/interceptor_activity_test.go`
- Modify: `internal/channel/channel.go:58-66` (the `ToolActivitySender` block)
- Modify: `internal/gateway/tool_activity_reporter.go`
- Create: `internal/gateway/tool_activity_reporter_test.go`
- Modify: `internal/gateway/tool_routing_test.go:42-47` (mock) and `:88-93` (call site)
- Modify: `internal/channel/tui/messages.go:63-70`
- Modify: `internal/channel/tui/adapter.go:291-297`
- Modify: `internal/channel/tui/model_update.go:125-136` (handler — compile-only mapping)

**Interfaces:**
- Produces: `tool.ToolActivityEvent{ID string, Done bool, Result *tool.ToolResult, Err error, Duration time.Duration}`; `tool.ToolActivityReporter.ReportToolActivity(ctx, *ToolCall, ToolActivityEvent)`.
- Produces: `channel.ToolActivity{CallID, ToolName, ArgSummary string, Done, OK bool, ResultSummary, Output string, Duration time.Duration}`; `channel.ToolActivitySender.SendToolActivity(ctx, MessageTarget, ToolActivity)`.
- Produces: `tui.toolActivityMsg{callID, tool, arg string, done, ok bool, resultSummary, output string, duration time.Duration}`.

- [ ] **Step 1: Rewrite the interceptor test for the new event shape**

Replace the whole body of `internal/tool/interceptor_activity_test.go`:

```go
package tool

import (
	"context"
	"errors"
	"testing"
)

type recordingReporter struct {
	events []ToolActivityEvent
	names  []string
}

func (r *recordingReporter) ReportToolActivity(_ context.Context, call *ToolCall, evt ToolActivityEvent) {
	r.events = append(r.events, evt)
	r.names = append(r.names, call.ToolName)
}

func TestActivityInterceptor_ReportsStartThenFinishWithMatchingID(t *testing.T) {
	rep := &recordingReporter{}
	ai := NewActivityInterceptor(rep)

	want := &ToolResult{Output: "ok"}
	final := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		if len(rep.events) != 1 || rep.events[0].Done {
			t.Errorf("expected one start event before execution, got %+v", rep.events)
		}
		return want, nil
	}

	res, err := ai.Intercept(context.Background(), &ToolCall{ToolName: "bash"}, final)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != want {
		t.Fatalf("result not passed through: %+v", res)
	}
	if len(rep.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(rep.events))
	}
	if rep.events[0].Done || !rep.events[1].Done {
		t.Errorf("expected [start, done], got done flags %v/%v", rep.events[0].Done, rep.events[1].Done)
	}
	if rep.events[0].ID == "" || rep.events[0].ID != rep.events[1].ID {
		t.Errorf("ids must be non-empty and match: %q vs %q", rep.events[0].ID, rep.events[1].ID)
	}
	if rep.events[1].Result != want {
		t.Errorf("done event must carry the result")
	}
	if rep.events[1].Duration < 0 {
		t.Errorf("done event must carry a duration")
	}
	for _, n := range rep.names {
		if n != "bash" {
			t.Errorf("reporter saw wrong tool name: %q", n)
		}
	}
}

func TestActivityInterceptor_CarriesError(t *testing.T) {
	rep := &recordingReporter{}
	ai := NewActivityInterceptor(rep)
	wantErr := errors.New("boom")
	final := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		return nil, wantErr
	}
	_, err := ai.Intercept(context.Background(), &ToolCall{ToolName: "x"}, final)
	if err != wantErr {
		t.Fatalf("error not passed through: %v", err)
	}
	if len(rep.events) != 2 || rep.events[1].Err != wantErr {
		t.Fatalf("done event must carry the error, got %+v", rep.events)
	}
}

func TestActivityInterceptor_NilReporterIsTransparent(t *testing.T) {
	ai := NewActivityInterceptor(nil)
	final := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "ok"}, nil
	}
	res, err := ai.Intercept(context.Background(), &ToolCall{ToolName: "x"}, final)
	if err != nil || res == nil || res.Output != "ok" {
		t.Fatalf("nil reporter should pass through cleanly, got res=%+v err=%v", res, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `go test ./internal/tool/ -run TestActivityInterceptor`
Expected: FAIL — `ToolActivityEvent` undefined, `ReportToolActivity` signature mismatch.

- [ ] **Step 3: Rewrite the interceptor to the new event shape**

Replace the whole body of `internal/tool/interceptor_activity.go`:

```go
package tool

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"
)

// ToolActivityEvent describes one lifecycle moment of a tool invocation.
// ID is stable across the matching start (Done=false) and done (Done=true)
// events so a consumer can correlate them even under concurrent tool runs.
type ToolActivityEvent struct {
	ID       string
	Done     bool
	Result   *ToolResult   // nil on start
	Err      error         // nil on start; the tool's error on done
	Duration time.Duration // wall time of the invocation, set on done
}

// ToolActivityReporter receives non-blocking notifications about tool
// execution lifecycle. Implementations must never block tool execution.
type ToolActivityReporter interface {
	ReportToolActivity(ctx context.Context, call *ToolCall, evt ToolActivityEvent)
}

// ActivityInterceptor reports tool-execution start and finish to a reporter.
// It never alters the call or the result, so it is safe at the front of the
// chain, ahead of permission checks.
type ActivityInterceptor struct {
	reporter ToolActivityReporter
}

// NewActivityInterceptor creates an ActivityInterceptor. A nil reporter makes
// Intercept a transparent pass-through.
func NewActivityInterceptor(reporter ToolActivityReporter) *ActivityInterceptor {
	return &ActivityInterceptor{reporter: reporter}
}

func (a *ActivityInterceptor) Name() string { return "activity" }

var activityCounter uint64

func newActivityID() string {
	return "act_" + strconv.FormatUint(atomic.AddUint64(&activityCounter, 1), 10)
}

func (a *ActivityInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
	if a.reporter == nil {
		return next(ctx, call)
	}
	id := newActivityID()
	a.reporter.ReportToolActivity(ctx, call, ToolActivityEvent{ID: id})
	start := time.Now()
	result, err := next(ctx, call)
	a.reporter.ReportToolActivity(ctx, call, ToolActivityEvent{
		ID:       id,
		Done:     true,
		Result:   result,
		Err:      err,
		Duration: time.Since(start),
	})
	return result, err
}
```

- [ ] **Step 4: Update the channel interface**

In `internal/channel/channel.go`, add `"time"` to the import block, then replace the `ToolActivitySender` block (currently lines 58-66):

```go
// ToolActivity is one tool-execution activity event. On start, only ArgSummary
// is meaningful; on done, OK/ResultSummary/Output/Duration are populated.
type ToolActivity struct {
	CallID        string
	ToolName      string
	ArgSummary    string
	Done          bool
	OK            bool
	ResultSummary string
	Output        string // capped raw output, for expand
	Duration      time.Duration
}

// ToolActivitySender is an optional interface for channels that can display
// live tool-execution activity. It never blocks and never affects execution.
type ToolActivitySender interface {
	SendToolActivity(ctx context.Context, target MessageTarget, act ToolActivity) error
}
```

- [ ] **Step 5: Update the gateway reporter to derive summary + cap output**

Replace the whole body of `internal/gateway/tool_activity_reporter.go`:

```go
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// GatewayToolActivityReporter routes live tool-activity events through the
// active gateway channel for the session. Non-blocking; never affects execution.
type GatewayToolActivityReporter struct {
	sessions *session.Manager
	channels *ChannelSubsystem
}

func NewGatewayToolActivityReporter(sessions *session.Manager, channels *ChannelSubsystem) *GatewayToolActivityReporter {
	return &GatewayToolActivityReporter{sessions: sessions, channels: channels}
}

func (r *GatewayToolActivityReporter) ReportToolActivity(ctx context.Context, call *tool.ToolCall, evt tool.ToolActivityEvent) {
	if r.sessions == nil || r.channels == nil {
		return
	}
	sess, err := r.sessions.GetByID(ctx, call.SessionID)
	if err != nil || sess == nil {
		return
	}
	ch := r.channels.Channel(sess.Channel)
	if ch == nil {
		return
	}
	sender, ok := ch.(channel.ToolActivitySender)
	if !ok {
		return
	}

	act := channel.ToolActivity{
		CallID:   evt.ID,
		ToolName: call.ToolName,
		Done:     evt.Done,
	}
	if !evt.Done {
		act.ArgSummary = summarizeToolInput(call.ToolName, call.Input)
	} else {
		errText := ""
		if evt.Err != nil {
			errText = evt.Err.Error()
		}
		output := ""
		if evt.Result != nil {
			if errText == "" {
				errText = evt.Result.Error
			}
			output = evt.Result.Output
		}
		act.Duration = evt.Duration
		act.OK = errText == ""
		act.ResultSummary = deriveResultSummary(errText, output)
		act.Output = capOutput(output)
	}

	_ = sender.SendToolActivity(ctx, channel.MessageTarget{Channel: sess.Channel, ChannelID: sess.ChannelID}, act)
}

// deriveResultSummary produces a short, tool-agnostic outcome hint: the error's
// first line on failure, a line count for multi-line output, or the clamped
// first line otherwise. Deliberately generic (no per-tool parsing).
func deriveResultSummary(errText, output string) string {
	if errText != "" {
		return "error: " + clampSummary(firstLine(errText))
	}
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return "done"
	}
	if n := strings.Count(output, "\n") + 1; n > 1 {
		return fmt.Sprintf("%d lines", n)
	}
	return clampSummary(output)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

const (
	maxOutputBytes = 4096
	maxOutputLines = 50
)

// capOutput bounds raw tool output before it reaches the TUI: at most
// maxOutputLines lines and maxOutputBytes bytes, trimmed to a valid rune
// boundary, with a truncation marker when clipped.
func capOutput(s string) string {
	truncated := false
	if lines := strings.SplitN(s, "\n", maxOutputLines+1); len(lines) > maxOutputLines {
		s = strings.Join(lines[:maxOutputLines], "\n")
		truncated = true
	}
	if len(s) > maxOutputBytes {
		s = s[:maxOutputBytes]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		truncated = true
	}
	if truncated {
		s += "\n… (truncated)"
	}
	return s
}

// summarizeToolInput extracts a short hint from a tool call's JSON input.
func summarizeToolInput(toolName, input string) string {
	var m map[string]any
	if json.Unmarshal([]byte(input), &m) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "path", "file_path", "query", "url", "pattern"} {
		if v, ok := m[key].(string); ok && v != "" {
			return clampSummary(v)
		}
	}
	return ""
}

func clampSummary(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
```

- [ ] **Step 6: Add the reporter unit test**

Create `internal/gateway/tool_activity_reporter_test.go`:

```go
package gateway

import "testing"

func TestDeriveResultSummary(t *testing.T) {
	tests := []struct {
		name    string
		errText string
		output  string
		want    string
	}{
		{"error wins", "permission denied\nstack", "ignored", "error: permission denied"},
		{"empty output", "", "", "done"},
		{"single line", "", "hello world", "hello world"},
		{"multi line", "", "a\nb\nc", "3 lines"},
		{"trailing newline single", "", "one\n", "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveResultSummary(tt.errText, tt.output); got != tt.want {
				t.Errorf("deriveResultSummary(%q,%q) = %q, want %q", tt.errText, tt.output, got, tt.want)
			}
		})
	}
}

func TestCapOutputLines(t *testing.T) {
	var b []byte
	for i := 0; i < 100; i++ {
		b = append(b, 'x', '\n')
	}
	got := capOutput(string(b))
	if n := len(splitLines(got)); n > maxOutputLines+1 {
		t.Errorf("capOutput kept %d lines, want <= %d", n, maxOutputLines+1)
	}
	if got[len(got)-len("(truncated)"):] != "(truncated)" {
		t.Errorf("expected truncation marker, got tail %q", got)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
```

- [ ] **Step 7: Update the routing test mock and call site**

In `internal/gateway/tool_routing_test.go`, replace the mock method (lines 42-47):

```go
func (c *routingTestChannel) SendToolActivity(_ context.Context, target channel.MessageTarget, act channel.ToolActivity) error {
	c.activityTarget = target
	c.activityTool = act.ToolName
	c.activityDone = act.Done
	return nil
}
```

And replace the reporter call site (lines 88-93) so it passes an event:

```go
	reporter := NewGatewayToolActivityReporter(sessions, channels)
	reporter.ReportToolActivity(context.Background(), &tool.ToolCall{
		ToolName:  "bash",
		Input:     `{"command":"echo ok"}`,
		SessionID: sess.ID,
	}, tool.ToolActivityEvent{ID: "act_test", Done: true, Result: &tool.ToolResult{Output: "ok"}})
```

- [ ] **Step 8: Widen the TUI activity message**

In `internal/channel/tui/messages.go`, replace the `toolActivityMsg` block (lines 63-70):

```go
// toolActivityMsg reports a tool lifecycle event. callID correlates the start
// (done=false) with the done (done=true). On done, ok/resultSummary/output/
// duration are populated.
type toolActivityMsg struct {
	callID        string
	tool          string
	arg           string
	done          bool
	ok            bool
	resultSummary string
	output        string
	duration      time.Duration
}
```

- [ ] **Step 9: Implement the new TUI sender**

In `internal/channel/tui/adapter.go`, replace `SendToolActivity` (lines 291-297):

```go
func (a *Adapter) SendToolActivity(_ context.Context, _ channel.MessageTarget, act channel.ToolActivity) error {
	if a.program == nil {
		return nil
	}
	a.program.Send(toolActivityMsg{
		callID:        act.CallID,
		tool:          act.ToolName,
		arg:           act.ArgSummary,
		done:          act.Done,
		ok:            act.OK,
		resultSummary: act.ResultSummary,
		output:        act.Output,
		duration:      act.Duration,
	})
	return nil
}
```

- [ ] **Step 10: Keep the TUI handler compiling (status-bar mapping only)**

In `internal/channel/tui/model_update.go`, replace the `case toolActivityMsg:` body (lines 125-136) with a field-renamed equivalent of the existing status-bar behavior (steps come in Task 2):

```go
	case toolActivityMsg:
		if msg.done {
			if msg.tool == m.activeTool {
				m.activeTool = ""
				m.activeToolSummary = ""
			}
		} else {
			m.activeTool = msg.tool
			m.activeToolSummary = msg.arg
		}
```

- [ ] **Step 11: Build and test**

Run: `make build-bin && make vet && go test ./internal/tool/ ./internal/gateway/ ./internal/channel/...`
Expected: PASS (build green; interceptor + reporter tests pass).

- [ ] **Step 12: Commit**

```bash
git add internal/tool/interceptor_activity.go internal/tool/interceptor_activity_test.go \
  internal/channel/channel.go internal/gateway/tool_activity_reporter.go \
  internal/gateway/tool_activity_reporter_test.go internal/gateway/tool_routing_test.go \
  internal/channel/tui/messages.go internal/channel/tui/adapter.go internal/channel/tui/model_update.go
git commit -m "feat(tui): carry tool-call id, result, and duration through the activity event"
```

---

## Task 2: TUI model — persist tool calls as steps

**Files:**
- Modify: `internal/channel/tui/model.go:25-37` (chatMessage) and `:40-116` (Model fields)
- Modify: `internal/channel/tui/model_update.go` (handler + helpers + reset)
- Modify: `internal/channel/tui/model_dialogs.go:31-32` (`/clear` reset)
- Create: `internal/channel/tui/model_steps_test.go`

**Interfaces:**
- Consumes: `toolActivityMsg` (Task 1).
- Produces: `workflowStep{callID, tool, arg string, done, ok bool, resultSummary, output string, duration time.Duration}`; `chatMessage.step *workflowStep` with `role == "step"`; `Model.stepIndex map[string]int`; `Model.stepsExpanded bool`; `(m *Model) appendStep(callID, tool, arg string)`; `(m *Model) hasSteps() bool`.

- [ ] **Step 1: Write the failing step-correlation test**

Create `internal/channel/tui/model_steps_test.go`:

```go
package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stepsOf(m *Model) []*workflowStep {
	var out []*workflowStep
	for i := range m.messages {
		if m.messages[i].role == "step" {
			out = append(out, m.messages[i].step)
		}
	}
	return out
}

func TestToolActivityBuildsAndUpdatesStep(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(toolActivityMsg{callID: "a1", tool: "grep", arg: "foo", done: false})
	steps := stepsOf(&m)
	require.Len(t, steps, 1)
	assert.Equal(t, "grep", steps[0].tool)
	assert.Equal(t, "foo", steps[0].arg)
	assert.False(t, steps[0].done)
	assert.True(t, m.hasSteps())

	m.Update(toolActivityMsg{
		callID: "a1", done: true, ok: true,
		resultSummary: "3 lines", output: "a\nb\nc", duration: 400 * time.Millisecond,
	})
	steps = stepsOf(&m)
	require.Len(t, steps, 1)
	assert.True(t, steps[0].done)
	assert.True(t, steps[0].ok)
	assert.Equal(t, "3 lines", steps[0].resultSummary)
	assert.Equal(t, 400*time.Millisecond, steps[0].duration)
}

func TestClearResetsStepIndex(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(toolActivityMsg{callID: "a1", tool: "grep", arg: "foo", done: false})
	m.Update(sessionResetMsg{})
	assert.Empty(t, m.stepIndex)
	assert.False(t, m.hasSteps())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/channel/tui/ -run 'TestToolActivityBuildsAndUpdatesStep|TestClearResetsStepIndex'`
Expected: FAIL — `workflowStep`, `chatMessage.step`, `hasSteps`, `stepIndex` undefined.

- [ ] **Step 3: Add the step types to the model**

In `internal/channel/tui/model.go`, add the `step` field to `chatMessage` (after `timestamp time.Time`, before the render cache comment block at line 31):

```go
	// step holds tool-call workflow data when role == "step". nil otherwise.
	step *workflowStep
```

Add the `workflowStep` type just above `type chatMessage struct` (line 25):

```go
// workflowStep is one tool call shown inline in the transcript as part of a
// round's workflow. Mutated in place from start (pending) to done.
type workflowStep struct {
	callID        string
	tool          string
	arg           string
	done          bool
	ok            bool
	resultSummary string
	output        string // capped raw output, shown when expanded
	duration      time.Duration
}
```

In the `Model` struct, add after the `activeToolSummary string` field (line 99):

```go
	// Workflow steps: tool calls persisted inline in messages as role "step".
	// stepIndex maps a callID to its index in messages (append-only, so stable).
	stepIndex     map[string]int
	stepsExpanded bool // global: show captured raw output under each step
```

- [ ] **Step 4: Build steps in the activity handler**

In `internal/channel/tui/model_update.go`, replace the `case toolActivityMsg:` body (the status-bar-only version from Task 1) with:

```go
	case toolActivityMsg:
		if msg.done {
			if i, ok := m.stepIndex[msg.callID]; ok {
				s := m.messages[i].step
				s.done = true
				s.ok = msg.ok
				s.resultSummary = msg.resultSummary
				s.output = msg.output
				s.duration = msg.duration
				m.chatRev++
			}
			if msg.tool == m.activeTool {
				m.activeTool = ""
				m.activeToolSummary = ""
			}
		} else {
			m.appendStep(msg.callID, msg.tool, msg.arg)
			m.activeTool = msg.tool
			m.activeToolSummary = msg.arg
		}
		m.updateViewportKeepScroll()
```

Add these helpers next to `addMessage` (after line 411):

```go
// appendStep adds a pending workflow step to the transcript and indexes it by
// callID so the matching done event can update it in place.
func (m *Model) appendStep(callID, tool, arg string) {
	m.messages = append(m.messages, chatMessage{
		role:      "step",
		step:      &workflowStep{callID: callID, tool: tool, arg: arg},
		timestamp: time.Now(),
	})
	if m.stepIndex == nil {
		m.stepIndex = make(map[string]int)
	}
	m.stepIndex[callID] = len(m.messages) - 1
	m.chatRev++
}

// hasSteps reports whether the transcript contains any workflow step.
func (m *Model) hasSteps() bool {
	return len(m.stepIndex) > 0
}
```

In the `case sessionResetMsg:` body (line 103), add the index reset right after `m.messages = m.messages[:0]`:

```go
		m.stepIndex = nil
```

- [ ] **Step 5: Reset the index on `/clear` too**

In `internal/channel/tui/model_dialogs.go`, in the `case "clear", "cls":` block, add right after `m.messages = m.messages[:0]` (line 32):

```go
		m.stepIndex = nil
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/channel/tui/ -run 'TestToolActivityBuildsAndUpdatesStep|TestClearResetsStepIndex'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/channel/tui/model.go internal/channel/tui/model_update.go \
  internal/channel/tui/model_dialogs.go internal/channel/tui/model_steps_test.go
git commit -m "feat(tui): persist tool calls as inline workflow steps"
```

---

## Task 3: Render collapsed steps, grouped under their round

**Files:**
- Modify: `internal/channel/tui/styles.go` (add step styles)
- Modify: `internal/channel/tui/model_view.go` (`renderStaticChat`, `renderMessageBlock`, new `renderStepLine`, `formatDuration`)
- Modify: `internal/channel/tui/model_view_test.go` (new render test)

**Interfaces:**
- Consumes: `workflowStep`, `chatMessage.step` (Task 2).
- Produces: `(m *Model) renderStepLine(s *workflowStep) string`; `formatDuration(time.Duration) string`.

- [ ] **Step 1: Write the failing collapsed-render test**

Add to `internal/channel/tui/model_view_test.go`:

```go
func TestRenderStepLineCollapsed(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.addMessage("user", "find the close logic")
	m.appendStep("a1", "grep", "implicitClose")
	if i, ok := m.stepIndex["a1"]; ok {
		s := m.messages[i].step
		s.done, s.ok, s.resultSummary, s.duration = true, true, "3 lines", 400*time.Millisecond
	}
	m.addMessage("agent", "It closes at episode.go:142")

	out := m.renderStaticChat()
	assert.Contains(t, out, "grep")
	assert.Contains(t, out, "implicitClose")
	assert.Contains(t, out, "3 lines")
	assert.Contains(t, out, "│") // round grouping guide
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 80)
	}
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "400ms", formatDuration(400*time.Millisecond))
	assert.Equal(t, "1.5s", formatDuration(1500*time.Millisecond))
}
```

Add `"time"` to the test file's import block if not present.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/channel/tui/ -run 'TestRenderStepLineCollapsed|TestFormatDuration'`
Expected: FAIL — `renderStepLine`/`formatDuration` undefined; step messages not rendered.

- [ ] **Step 3: Add step styles**

In `internal/channel/tui/styles.go`, add inside the `var (...)` block (after `statusDimStyle`, around line 117):

```go
	// Workflow steps — tool calls shown inline under a round.
	stepGuideStyle  = lipgloss.NewStyle().Foreground(colorDim)
	stepGlyphStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	stepArgStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	stepMetaStyle   = lipgloss.NewStyle().Foreground(colorDim)
	stepRunStyle    = lipgloss.NewStyle().Foreground(colorGold)
	stepOkStyle     = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	stepErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	stepOutputStyle = lipgloss.NewStyle().Foreground(colorDim)
```

- [ ] **Step 4: Render steps in the transcript**

In `internal/channel/tui/model_view.go`, add `"time"` to the import block.

Replace the loop body in `renderStaticChat` (lines 264-274) to bypass the per-message cache for steps and tighten spacing between consecutive steps:

```go
	for i := range m.messages {
		if i > 0 {
			if m.messages[i].role == "step" && m.messages[i-1].role == "step" {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		msg := &m.messages[i]
		if msg.role == "step" {
			b.WriteString(m.renderMessageBlock(msg)) // depends on stepsExpanded; never cached
			continue
		}
		if msg.renderedWidth != m.width || msg.rendered == "" {
			msg.rendered = m.renderMessageBlock(msg)
			msg.renderedWidth = m.width
		}
		b.WriteString(msg.rendered)
	}
```

Add a `case "step":` to `renderMessageBlock` (inside the `switch msg.role` at line 298, before the closing `}`):

```go
	case "step":
		return m.renderStepLine(msg.step)
```

Add `renderStepLine` and `formatDuration` after `renderMessageBlock` (after line 312):

```go
// renderStepLine renders one workflow step as a guide-prefixed line:
//   │ ⚙ <tool> · <arg>   <status> <duration> · <result>
// The variable-length arg is budgeted on plain widths so the styled line stays
// within messageContentWidth. (Expanded raw output is added in a later task.)
func (m *Model) renderStepLine(s *workflowStep) string {
	width := m.messageContentWidth()

	statusPlain, statusStyled := stepStatus(s)

	meta := ""
	if s.done {
		var parts []string
		if s.duration > 0 {
			parts = append(parts, formatDuration(s.duration))
		}
		if s.resultSummary != "" {
			parts = append(parts, s.resultSummary)
		}
		meta = strings.Join(parts, " · ")
	}

	head := "⚙ " + s.tool
	used := runewidth.StringWidth(statusPlain) + 1 + runewidth.StringWidth(head)
	metaCost := 0
	if meta != "" {
		metaCost = 2 + runewidth.StringWidth(meta)
	}

	arg := s.arg
	if arg != "" {
		argBudget := width - used - metaCost - runewidth.StringWidth(" · ")
		if argBudget < 4 {
			arg = ""
		} else {
			arg = truncateTail(arg, argBudget)
		}
	}

	line := statusStyled + " " + stepGlyphStyle.Render("⚙ ") + s.tool
	if arg != "" {
		line += stepArgStyle.Render(" · " + arg)
	}
	if meta != "" {
		line += "  " + stepMetaStyle.Render(meta)
	}

	return indentBlock(line, stepGuideStyle.Render("│ "), "  ")
}

// stepStatus returns the plain text (for width budgeting) and styled glyph for
// a step's current state.
func stepStatus(s *workflowStep) (plain, styled string) {
	switch {
	case !s.done:
		return "⏵ running", stepRunStyle.Render("⏵ running")
	case s.ok:
		return "✓", stepOkStyle.Render("✓")
	default:
		return "✗", stepErrStyle.Render("✗")
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/channel/tui/ -run 'TestRenderStepLineCollapsed|TestFormatDuration|TestViewHeightFitsWithPanels'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/channel/tui/styles.go internal/channel/tui/model_view.go internal/channel/tui/model_view_test.go
git commit -m "feat(tui): render inline workflow steps grouped under each round"
```

---

## Task 4: Expand raw output (Ctrl+T) and status-bar hint

**Files:**
- Modify: `internal/channel/tui/model_update.go` (`Ctrl+T` in `handleChatKey`)
- Modify: `internal/channel/tui/model_view.go` (`renderStepLine` expanded branch, `renderStatusBar` hint)
- Modify: `internal/channel/tui/model_view_test.go` (expanded render test)

**Interfaces:**
- Consumes: `Model.stepsExpanded`, `hasSteps`, `renderStepLine` (Tasks 2-3).

- [ ] **Step 1: Write the failing expand test**

Add to `internal/channel/tui/model_view_test.go`:

```go
func TestStepRawOutputExpandToggle(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.appendStep("a1", "grep", "implicitClose")
	if i, ok := m.stepIndex["a1"]; ok {
		s := m.messages[i].step
		s.done, s.ok, s.output = true, true, "UNIQUE_OUTPUT_MARKER"
	}

	m.stepsExpanded = false
	assert.NotContains(t, m.renderStaticChat(), "UNIQUE_OUTPUT_MARKER")

	m.chatRev++ // invalidate the cache the way the Ctrl+T handler does
	m.stepsExpanded = true
	assert.Contains(t, m.renderStaticChat(), "UNIQUE_OUTPUT_MARKER")
}

func TestCtrlTTogglesExpansion(t *testing.T) {
	m := NewModel("v", "local", "/tmp")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	before := m.stepsExpanded
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	assert.NotEqual(t, before, m.stepsExpanded)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/channel/tui/ -run 'TestStepRawOutputExpandToggle|TestCtrlTTogglesExpansion'`
Expected: FAIL — Ctrl+T does nothing; expanded output not rendered.

- [ ] **Step 3: Handle Ctrl+T**

In `internal/channel/tui/model_update.go`, in `handleChatKey`, add right after the `Ctrl+O` block (after line 224):

```go
	if msg.Type == tea.KeyCtrlT {
		m.stepsExpanded = !m.stepsExpanded
		m.chatRev++
		m.updateViewportKeepScroll()
		return m, nil
	}
```

- [ ] **Step 4: Render expanded output**

In `internal/channel/tui/model_view.go`, in `renderStepLine`, replace the final `return indentBlock(...)` with a body that appends captured output when expanded:

```go
	body := line
	if m.stepsExpanded && s.output != "" {
		body += "\n" + stepOutputStyle.Render(wrapText(s.output, width-2))
	}

	return indentBlock(body, stepGuideStyle.Render("│ "), "  ")
```

- [ ] **Step 5: Add the status-bar hint**

In `internal/channel/tui/model_view.go`, in `renderStatusBar`, add to the right-segment assembly right before the `if m.currentModel != ""` block (around line 95):

```go
	if m.hasSteps() {
		rightSegments = append(rightSegments, statusSegment{text: "Ctrl+T details", style: statusHintStyle})
	}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/channel/tui/ -run 'TestStepRawOutputExpandToggle|TestCtrlTTogglesExpansion|TestViewHeightFitsWithPanels|TestStatusBarTruncatesLongSegments'`
Expected: PASS.

- [ ] **Step 7: Full verification**

Run: `make build-bin && make vet && make test-short`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/channel/tui/model_update.go internal/channel/tui/model_view.go internal/channel/tui/model_view_test.go
git commit -m "feat(tui): expand workflow step raw output with Ctrl+T"
```

---

## Self-Review

**Spec coverage:**
- Plumbing (id/result/duration), result summary, output cap → Task 1.
- TUI step model + correlation + clear reset → Task 2.
- Grouped collapsed render with `│` guide → Task 3.
- Global `Ctrl+T` expand + status hint → Task 4.
- Part 2 visual cleanup (step styles + round spacing) → folded into Task 3.
- Out-of-scope items (per-step cursor, tokens/sub-agents, Telegram, bash stream) → not implemented, as specified.

**Type consistency:** `ToolActivityEvent.ID` → `ToolActivity.CallID` → `toolActivityMsg.callID` → `workflowStep.callID` form one correlation chain across layers (renamed at each boundary intentionally). `workflowStep` field names are identical in `model.go`, the handler, and `renderStepLine`. `stepIndex`/`stepsExpanded`/`hasSteps`/`appendStep`/`renderStepLine`/`formatDuration`/`stepStatus` are each defined once and used consistently.

**Divergence from spec (intentional):** result summary is tool-agnostic ("N lines"/first line) rather than per-tool ("N hits") — avoids coupling the gateway to individual tool output formats; conveys the same magnitude signal.
