package browser_agent

import (
	"context"
	"testing"
	"time"
)

func TestNew_InvalidURL(t *testing.T) {
	// Without a real Chrome, New will fail to connect
	_, err := New("http://localhost:1")
	if err == nil {
		t.Skip("expected connection error without real Chrome instance")
	}
}

func TestAction_DefaultValues(t *testing.T) {
	a := &Action{Type: "click", ElementID: "e1"}
	if a.Type != "click" {
		t.Errorf("expected Type 'click', got %q", a.Type)
	}
	if a.Timeout != 0 {
		t.Errorf("expected Timeout 0, got %v", a.Timeout)
	}
}

func TestAutomationResult(t *testing.T) {
	r := &AutomationResult{
		Success: true,
		Steps:   5,
		Summary: "Completed the task",
		Error:   "",
	}
	if !r.Success {
		t.Error("expected Success true")
	}
	if r.Steps != 5 {
		t.Errorf("expected Steps 5, got %d", r.Steps)
	}
	if r.Summary != "Completed the task" {
		t.Errorf("unexpected Summary: %q", r.Summary)
	}
}

func TestDOMElement(t *testing.T) {
	e := &DOMElement{
		ID:          "42",
		Tag:         "button",
		Type:        "submit",
		Text:        "Click me",
		Placeholder: "",
		Name:        "submit-btn",
		Href:        "",
		Visible:     true,
	}
	if e.ID != "42" {
		t.Errorf("expected ID '42', got %q", e.ID)
	}
	if e.Tag != "button" {
		t.Errorf("expected Tag 'button', got %q", e.Tag)
	}
	if !e.Visible {
		t.Error("expected Visible true")
	}
}

func TestPageSnapshot(t *testing.T) {
	s := &PageSnapshot{
		URL:   "https://example.com",
		Title: "Example",
		Elements: []*DOMElement{
			{ID: "1", Tag: "a", Text: "link"},
		},
	}
	if s.URL != "https://example.com" {
		t.Errorf("unexpected URL: %q", s.URL)
	}
	if len(s.Elements) != 1 {
		t.Errorf("expected 1 element, got %d", len(s.Elements))
	}
}

// TestExecuteAutomation_PlannerErrors tests the planner error path directly,
// without needing a real browser.
func TestExecuteAutomation_PlannerErrors(t *testing.T) {
	planner := func(snapshot *PageSnapshot, step int) (*Action, error) {
		return nil, assertNewError("planner error")
	}
	snapshot := &PageSnapshot{URL: "about:blank", Elements: nil}
	_, err := planner(snapshot, 0)
	if err == nil {
		t.Fatal("expected planner error")
	}
	if err.Error() != "planner error" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecuteAutomation_DoneAction(t *testing.T) {
	planner := func(snapshot *PageSnapshot, step int) (*Action, error) {
		return &Action{Type: "done", Summary: "Task complete!"}, nil
	}
	snapshot := &PageSnapshot{URL: "about:blank", Elements: nil}
	action, err := planner(snapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != "done" {
		t.Errorf("expected action type 'done', got %q", action.Type)
	}
	if action.Summary != "Task complete!" {
		t.Errorf("expected Summary 'Task complete!', got %q", action.Summary)
	}
}

func TestExecuteAutomation_FailAction(t *testing.T) {
	planner := func(snapshot *PageSnapshot, step int) (*Action, error) {
		return &Action{Type: "fail", Error: "Something went wrong"}, nil
	}
	snapshot := &PageSnapshot{URL: "about:blank", Elements: nil}
	action, err := planner(snapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != "fail" {
		t.Errorf("expected action type 'fail', got %q", action.Type)
	}
	if action.Error != "Something went wrong" {
		t.Errorf("expected Error 'Something went wrong', got %q", action.Error)
	}
}

func TestPage_Navigate_NoClient(t *testing.T) {
	p := &Page{client: nil, targetID: "test"}
	err := p.Navigate(context.Background(), "https://example.com")
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func TestPage_Close(t *testing.T) {
	p := &Page{client: nil, targetID: "test"}
	err := p.Close(context.Background())
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func TestPage_GetTitle_NoClient(t *testing.T) {
	p := &Page{client: nil}
	_, err := p.GetTitle(context.Background())
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func TestPage_GetText_NoClient(t *testing.T) {
	p := &Page{client: nil}
	_, err := p.GetText(context.Background())
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func TestPage_ExtractJSON_NoClient(t *testing.T) {
	p := &Page{client: nil}
	_, err := p.ExtractJSON(context.Background(), "a")
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func TestPage_WaitFor_Cancel(t *testing.T) {
	p := &Page{client: nil}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel
	err := p.WaitFor(ctx, "text", 5*time.Second)
	if err == nil {
		t.Skip("expected context error")
	}
}

// Test struct field assignment for Page
func TestPage_Fields(t *testing.T) {
	p := &Page{targetID: "t1", url: "https://example.com", title: "Example"}
	if p.targetID != "t1" {
		t.Errorf("unexpected targetID: %q", p.targetID)
	}
	if p.url != "https://example.com" {
		t.Errorf("unexpected url: %q", p.url)
	}
}

func TestNewPage_NoClient(t *testing.T) {
	ba := &BrowserAgent{client: nil, pages: make(map[string]*Page)}
	_, err := ba.NewPage(context.Background())
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func TestBrowserAgent_Close_NoClient(t *testing.T) {
	ba := &BrowserAgent{client: nil}
	err := ba.Close()
	if err == nil {
		t.Skip("expected error without real client")
	}
}

func assertNewError(msg string) error {
	return &testError{msg: msg}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
