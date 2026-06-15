package episode

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/mind"
)

// fakePlanter records timer follow-ups handed to it.
type fakePlanter struct {
	planted []FollowUp
	err     error
}

func (p *fakePlanter) Plant(_ context.Context, _ string, f FollowUp) error {
	if p.err != nil {
		return p.err
	}
	p.planted = append(p.planted, f)
	return nil
}

// syncBus is a synchronous event bus for deterministic assertions (the real bus
// dispatches handlers on goroutines).
type syncBus struct {
	mu     sync.Mutex
	events []agent.Event
}

func (b *syncBus) Publish(e agent.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}
func (b *syncBus) Subscribe(func(agent.Event)) agent.Subscription { return nil }

func (b *syncBus) count(eventType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, e := range b.events {
		if e.EventType() == eventType {
			n++
		}
	}
	return n
}

// TestCloseTimerFollowUpPlanted verifies a timer follow-up reaches the planter.
func TestCloseTimerFollowUpPlanted(t *testing.T) {
	planter := &fakePlanter{}
	provider := &episodeTestProvider{streams: []providerResponse{{
		text: "scheduling",
		toolCalls: []mind.ToolUseBlock{closeCall(
			`{"status":"handed_off","summary":"Will resume later.","follow_ups":[{"kind":"timer","detail":"30m","goal":"Resume the deploy"}]}`)},
	}}}
	runner, _ := testRunner(t, provider)
	runner.SetPlanter(planter)

	if _, err := runner.Execute(context.Background(), chatRequest("Deploy", "deploy")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(planter.planted) != 1 {
		t.Fatalf("planted = %d, want 1", len(planter.planted))
	}
	if planter.planted[0].Goal != "Resume the deploy" || planter.planted[0].Detail != "30m" {
		t.Fatalf("planted follow-up = %#v", planter.planted[0])
	}
}

// TestCloseWatchFollowUpBecomesCommitment verifies watch/check follow-ups persist
// as commitments rather than reaching the planter.
func TestCloseWatchFollowUpBecomesCommitment(t *testing.T) {
	planter := &fakePlanter{}
	provider := &episodeTestProvider{streams: []providerResponse{{
		text: "watching",
		toolCalls: []mind.ToolUseBlock{closeCall(
			`{"status":"done","summary":"Set a watch.","follow_ups":[{"kind":"watch","detail":"PR #42 merged","goal":"Notify when PR merges"}]}`)},
	}}}
	runner, ws := testRunner(t, provider)
	runner.SetPlanter(planter)

	if _, err := runner.Execute(context.Background(), chatRequest("Watch PR", "watch")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(planter.planted) != 0 {
		t.Fatalf("watch follow-up should not be planted, got %d", len(planter.planted))
	}
	commitments, err := ws.ListCommitments(context.Background(), []string{"active"}, "")
	if err != nil {
		t.Fatalf("ListCommitments() error = %v", err)
	}
	if len(commitments) != 1 || commitments[0].Kind != "watch" || commitments[0].Title != "PR #42 merged" {
		t.Fatalf("commitments = %#v", commitments)
	}
}

// TestTimerFollowUpDroppedWithoutPlanter verifies a nil planter drops timer
// follow-ups without failing the episode.
func TestTimerFollowUpDroppedWithoutPlanter(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text: "no planter",
		toolCalls: []mind.ToolUseBlock{closeCall(
			`{"status":"handed_off","summary":"queued","follow_ups":[{"kind":"timer","detail":"1h","goal":"later"}]}`)},
	}}}
	runner, _ := testRunner(t, provider) // planter left nil

	out, err := runner.Execute(context.Background(), chatRequest("g", "t"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != "handed_off" {
		t.Fatalf("status = %q, want handed_off", out.Status)
	}
}

// TestSalvageMarksOutcomeAndJournal verifies a salvaged outcome publishes the
// metric event and records the salvaged marker in the journal detail.
func TestSalvageMarksOutcomeAndJournal(t *testing.T) {
	streams := make([]providerResponse, defaultMaxIterations)
	for i := range streams {
		streams[i] = providerResponse{text: "still working, no close call"}
	}
	provider := &episodeTestProvider{streams: streams, complete: providerResponse{text: "not json"}}

	bus := &syncBus{}
	runner, ws := testRunner(t, provider)
	runner.bus = bus

	out, err := runner.Execute(context.Background(), chatRequest("Long task", "go"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status == "failed" {
		t.Fatalf("unexpected failed status: %#v", out)
	}
	if bus.count("replay.episode_salvaged") != 1 {
		t.Fatalf("EpisodeSalvaged events = %d, want 1", bus.count("replay.episode_salvaged"))
	}
	journal, err := ws.ListJournal(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	if len(journal) != 1 || !strings.Contains(journal[0].Detail, "salvaged=true") {
		t.Fatalf("journal = %#v, want salvaged marker", journal)
	}
}

// TestHappyPathNotSalvaged verifies a compliant episode_close is not flagged
// salvaged.
func TestHappyPathNotSalvaged(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "answer",
		toolCalls: []mind.ToolUseBlock{closeCall(`{"status":"done","summary":"ok"}`)},
	}}}
	bus := &syncBus{}
	runner, ws := testRunner(t, provider)
	runner.bus = bus

	if _, err := runner.Execute(context.Background(), chatRequest("g", "t")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if bus.count("replay.episode_salvaged") != 0 {
		t.Fatalf("EpisodeSalvaged events = %d, want 0", bus.count("replay.episode_salvaged"))
	}
	journal, _ := ws.ListJournal(context.Background(), "", 10)
	if len(journal) != 1 || strings.Contains(journal[0].Detail, "salvaged") {
		t.Fatalf("journal = %#v, want no salvaged marker", journal)
	}
}
