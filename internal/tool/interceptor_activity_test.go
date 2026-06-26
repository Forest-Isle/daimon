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
