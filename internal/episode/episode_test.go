package episode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

type providerResponse struct {
	text      string
	toolCalls []agent.ToolUseBlock
	usage     agent.Usage
	err       error
}

type episodeTestProvider struct {
	streams  []providerResponse
	complete providerResponse
	requests []agent.CompletionRequest
}

func (p *episodeTestProvider) Complete(_ context.Context, req agent.CompletionRequest) (*agent.CompletionResponse, error) {
	p.requests = append(p.requests, req)
	if p.complete.err != nil {
		return nil, p.complete.err
	}
	return &agent.CompletionResponse{Text: p.complete.text, ToolCalls: p.complete.toolCalls, Usage: p.complete.usage}, nil
}

func (p *episodeTestProvider) Stream(_ context.Context, req agent.CompletionRequest) (agent.StreamIterator, error) {
	p.requests = append(p.requests, req)
	if len(p.streams) == 0 {
		return &episodeTestStream{response: providerResponse{text: "done"}}, nil
	}
	resp := p.streams[0]
	p.streams = p.streams[1:]
	if resp.err != nil {
		return nil, resp.err
	}
	return &episodeTestStream{response: resp}, nil
}

type episodeTestStream struct {
	response providerResponse
	done     bool
}

func (s *episodeTestStream) Next() (agent.StreamDelta, error) {
	if s.done {
		return agent.StreamDelta{Done: true}, nil
	}
	s.done = true
	if s.response.err != nil {
		return agent.StreamDelta{}, s.response.err
	}
	return agent.StreamDelta{
		Text:       s.response.text,
		ToolCalls:  s.response.toolCalls,
		Done:       true,
		StopReason: agent.StopToolUse,
		Usage:      s.response.usage,
	}, nil
}

func (s *episodeTestStream) Close() {}

func openEpisodeWorldTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "episode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testRunner(t *testing.T, p agent.Provider) (*Runner, *world.Store) {
	t.Helper()
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	id := &world.Identity{Dir: t.TempDir()}
	return NewRunner(p, ws, id, nil), ws
}

// countingInvoke records tool invocations and returns a fixed output.
func countingInvoke(counter *atomic.Int32) agent.ToolInvokeFunc {
	return func(_ context.Context, _ int, _ agent.ToolUseBlock) (string, bool) {
		counter.Add(1)
		return "counted", false
	}
}

func closeCall(input string) agent.ToolUseBlock {
	return agent.ToolUseBlock{ID: "close_1", Name: episodeCloseToolName, Input: input}
}

func chatRequest(goal, text string) agent.CognitiveRequest {
	return agent.CognitiveRequest{
		SessionID:  "sess_test",
		Goal:       goal,
		Trigger:    text,
		Transcript: []agent.CompletionMessage{{Role: "user", Content: text}},
	}
}

func TestExecuteBasicHappyPath(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "Here is your answer.",
		toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"Handled request."}`)},
	}}}
	runner, ws := testRunner(t, provider)

	out, err := runner.Execute(context.Background(), chatRequest("Handle request", "hello"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != "done" {
		t.Fatalf("status = %q, want done", out.Status)
	}
	if out.Reply != "Here is your answer." {
		t.Fatalf("reply = %q, want assistant text", out.Reply)
	}
	journal, err := ws.ListJournal(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	if len(journal) != 1 || journal[0].Summary != "Handled request." {
		t.Fatalf("journal = %#v", journal)
	}
}

// TestExecuteIdempotentReplaySkip verifies CF2: when a CognitiveRequest carries a
// deterministic EpisodeID whose outcome already committed, a re-delivery skips
// without re-running the model (heart's at-least-once replay must not double-run).
func TestExecuteIdempotentReplaySkip(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "done work",
		toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"did the thing"}`)},
	}}}
	runner, ws := testRunner(t, provider)

	req := chatRequest("Handle event", "trigger")
	req.EpisodeID = "evt-dedup-1"

	// First delivery: runs and commits an outcome.
	if _, err := runner.Execute(context.Background(), req); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	callsAfterFirst := len(provider.requests)
	if callsAfterFirst == 0 {
		t.Fatal("provider should have been called on first delivery")
	}

	// Second delivery of the same event id (at-least-once replay): must skip.
	out, err := runner.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if len(provider.requests) != callsAfterFirst {
		t.Fatalf("idempotent replay re-ran the provider: %d calls (want %d)", len(provider.requests), callsAfterFirst)
	}
	if !strings.Contains(out.Summary, "already handled") {
		t.Fatalf("expected idempotent-skip summary, got %q", out.Summary)
	}
	journal, err := ws.ListJournal(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := 0
	for _, e := range journal {
		if e.Kind == "outcome" {
			outcomes++
		}
	}
	if outcomes != 1 {
		t.Fatalf("expected exactly 1 outcome row after replay, got %d", outcomes)
	}
}

func TestExecuteMaxIterationsSalvage(t *testing.T) {
	streams := make([]providerResponse, defaultMaxIterations)
	for i := range streams {
		streams[i] = providerResponse{text: "I am blocked waiting for credentials."}
	}
	provider := &episodeTestProvider{streams: streams, complete: providerResponse{text: "not json"}}
	runner, _ := testRunner(t, provider)

	out, err := runner.Execute(context.Background(), chatRequest("Deploy service", "deploy now"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", out.Status)
	}
	if !strings.Contains(out.Summary, "Deploy service") {
		t.Fatalf("summary = %q, want goal context", out.Summary)
	}
}

func TestExecuteStreamError(t *testing.T) {
	streamErr := errors.New("stream failed")
	provider := &episodeTestProvider{streams: []providerResponse{{err: streamErr}}}
	runner, ws := testRunner(t, provider)

	req := chatRequest("", "hi")
	req.EpisodeID = "evt-streamfail-1"
	out, err := runner.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Execute() error = nil, want stream error")
	}
	if out.Status != "failed" {
		t.Fatalf("status = %q, want failed", out.Status)
	}

	// CF4 / invariant #3 (交账强制): a provider error mid-episode must still leave a
	// durable Outcome in the world, not vanish without a trace.
	journal, jerr := ws.ListJournal(context.Background(), "", 10)
	if jerr != nil {
		t.Fatalf("ListJournal: %v", jerr)
	}
	found := false
	for _, e := range journal {
		if e.ID == "journal_outcome_evt-streamfail-1" && e.Kind == "outcome" {
			found = true
			if !strings.Contains(e.Summary, "stream error") {
				t.Fatalf("outcome summary should record the failure, got %q", e.Summary)
			}
		}
	}
	if !found {
		t.Fatalf("stream-error episode left no outcome journal: %#v", journal)
	}
}

// TestParseOutcomeRejectsInvalidStatus verifies invariant #3 (schema-validated
// Outcome): episode_close must declare a status in the enum, so an out-of-enum
// value is rejected (forcing the model to retry) rather than silently propagated
// into the journal.
func TestParseOutcomeRejectsInvalidStatus(t *testing.T) {
	for _, status := range []string{"success", "partial", "DONE", "", "complete"} {
		raw := `{"status":"` + status + `","summary":"ok"}`
		if _, err := parseOutcome(raw); err == nil {
			t.Fatalf("parseOutcome accepted invalid status %q", status)
		}
	}
	for _, status := range []string{"done", "blocked", "handed_off", " done "} {
		raw := `{"status":"` + status + `","summary":"ok"}`
		out, err := parseOutcome(raw)
		if err != nil {
			t.Fatalf("parseOutcome rejected valid status %q: %v", status, err)
		}
		if out.Status != strings.TrimSpace(status) {
			t.Fatalf("status = %q, want trimmed %q", out.Status, strings.TrimSpace(status))
		}
	}
}

// TestExecuteWorldWriteFailureStillRecordsTrace verifies invariant #3 (交账强制):
// a malformed WorldWrite makes ApplyOutcome's transaction roll back, which would
// otherwise erase the episode's journal trace too. The runner must re-record the
// outcome with no writes so the episode is accounted for rather than vanishing.
func TestExecuteWorldWriteFailureStillRecordsTrace(t *testing.T) {
	bad := `{"status":"done","summary":"did work","world_writes":[{"op":"bogus.op","target":"x","body":{}}]}`
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "working",
		toolCalls: []agent.ToolUseBlock{closeCall(bad)},
	}}}
	runner, ws := testRunner(t, provider)

	req := chatRequest("Do a thing", "go")
	req.EpisodeID = "evt-badwrite-1"
	out, err := runner.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != "failed" {
		t.Fatalf("status = %q, want failed", out.Status)
	}

	journal, jerr := ws.ListJournal(context.Background(), "", 10)
	if jerr != nil {
		t.Fatalf("ListJournal: %v", jerr)
	}
	found := false
	for _, e := range journal {
		if e.ID == "journal_outcome_evt-badwrite-1" && e.Kind == "outcome" {
			found = true
			if !strings.Contains(e.Summary, "world write failed") {
				t.Fatalf("outcome summary should note the write failure, got %q", e.Summary)
			}
		}
	}
	if !found {
		t.Fatalf("bad-write episode left no outcome journal: %#v", journal)
	}
}

func TestExecuteToolDispatchBeforeClose(t *testing.T) {
	var calls atomic.Int32
	provider := &episodeTestProvider{streams: []providerResponse{
		{
			text:      "using a tool",
			toolCalls: []agent.ToolUseBlock{{ID: "call_1", Name: "count_tool", Input: `{}`}},
		},
		{
			text:      "closing",
			toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"Tool dispatched."}`)},
		},
	}}
	runner, _ := testRunner(t, provider)

	req := chatRequest("Use a tool", "use tool")
	req.Invoke = countingInvoke(&calls)
	out, err := runner.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != "done" {
		t.Fatalf("status = %q, want done", out.Status)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("tool invocations = %d, want 1", got)
	}
}

type captureRecorder struct {
	costs []EpisodeCost
}

func (c *captureRecorder) RecordEpisodeCost(_ context.Context, e EpisodeCost) error {
	c.costs = append(c.costs, e)
	return nil
}

// TestExecuteRecordsCost verifies the §4.11 economy hook: a completed episode
// records one cost row carrying the tokens it consumed plus model/provider/id.
func TestExecuteRecordsCost(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "answer",
		toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"Handled."}`)},
		usage:     agent.Usage{InputTokens: 100, OutputTokens: 40, CacheReadTokens: 25},
	}}}
	runner, _ := testRunner(t, provider)
	rec := &captureRecorder{}
	runner.SetCostRecorder(rec)

	req := chatRequest("Handle request", "hello")
	req.Model = "claude-x"
	req.Provider = "claude"
	if _, err := runner.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rec.costs) != 1 {
		t.Fatalf("want 1 cost row, got %d", len(rec.costs))
	}
	c := rec.costs[0]
	if want := (agent.Usage{InputTokens: 100, OutputTokens: 40, CacheReadTokens: 25}); c.Usage != want {
		t.Fatalf("usage = %+v, want %+v", c.Usage, want)
	}
	if c.Model != "claude-x" || c.Provider != "claude" || c.EpisodeID == "" {
		t.Fatalf("cost meta = %+v", c)
	}
}

// TestExecuteAccumulatesCostAcrossCalls verifies the per-episode total sums the
// usage of every provider call (a tool-dispatch turn plus the closing turn).
func TestExecuteAccumulatesCostAcrossCalls(t *testing.T) {
	var calls atomic.Int32
	provider := &episodeTestProvider{streams: []providerResponse{
		{text: "using a tool", toolCalls: []agent.ToolUseBlock{{ID: "call_1", Name: "count_tool", Input: `{}`}}, usage: agent.Usage{InputTokens: 50, OutputTokens: 10}},
		{text: "closing", toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"Tool dispatched."}`)}, usage: agent.Usage{InputTokens: 30, OutputTokens: 5}},
	}}
	runner, _ := testRunner(t, provider)
	rec := &captureRecorder{}
	runner.SetCostRecorder(rec)

	req := chatRequest("Use a tool", "use tool")
	req.Invoke = countingInvoke(&calls)
	if _, err := runner.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rec.costs) != 1 {
		t.Fatalf("want 1 cost row, got %d", len(rec.costs))
	}
	if want := (agent.Usage{InputTokens: 80, OutputTokens: 15}); rec.costs[0].Usage != want {
		t.Fatalf("accumulated usage = %+v, want %+v", rec.costs[0].Usage, want)
	}
}

// TestExecuteSkipsCostWhenZeroUsage verifies the guard: an episode whose provider
// reported no usage records no cost row (zero is "unknown", not a real $0 episode).
func TestExecuteSkipsCostWhenZeroUsage(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"Handled."}`)},
	}}}
	runner, _ := testRunner(t, provider)
	rec := &captureRecorder{}
	runner.SetCostRecorder(rec)
	if _, err := runner.Execute(context.Background(), chatRequest("g", "hi")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rec.costs) != 0 {
		t.Fatalf("zero-usage episode must record no cost, got %d", len(rec.costs))
	}
}

// TestExecuteSkipsCostOnIdempotentReplay verifies an at-least-once re-delivery
// (which skips before any provider call) does not double-charge the ledger.
func TestExecuteSkipsCostOnIdempotentReplay(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"did it"}`)},
		usage:     agent.Usage{InputTokens: 10, OutputTokens: 2},
	}}}
	runner, _ := testRunner(t, provider)
	rec := &captureRecorder{}
	runner.SetCostRecorder(rec)

	req := chatRequest("Handle", "trigger")
	req.EpisodeID = "evt-cost-dedup"
	if _, err := runner.Execute(context.Background(), req); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := runner.Execute(context.Background(), req); err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(rec.costs) != 1 {
		t.Fatalf("replay must not record a second cost row, got %d", len(rec.costs))
	}
}

// TestExecutePanicInToolDispatchStillRecordsTrace verifies invariant #3 (交账强制):
// a tool dispatch that panics must not let the episode vanish without a journal
// trace — the runner recovers, records a failed outcome, and surfaces the panic.
func TestExecutePanicInToolDispatchStillRecordsTrace(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "using a tool",
		toolCalls: []agent.ToolUseBlock{{ID: "call_1", Name: "boom", Input: `{}`}},
	}}}
	runner, ws := testRunner(t, provider)

	req := chatRequest("Use a tool", "use tool")
	req.EpisodeID = "evt-panic-1"
	req.Invoke = func(_ context.Context, _ int, _ agent.ToolUseBlock) (string, bool) {
		panic("tool exploded")
	}

	out, err := runner.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("a panicking tool dispatch must surface as an error")
	}
	if out.Status != "failed" {
		t.Fatalf("status = %q, want failed", out.Status)
	}

	journal, jerr := ws.ListJournal(context.Background(), "", 10)
	if jerr != nil {
		t.Fatalf("ListJournal: %v", jerr)
	}
	found := false
	for _, e := range journal {
		if e.ID == "journal_outcome_evt-panic-1" && e.Kind == "outcome" {
			found = true
		}
	}
	if !found {
		t.Fatalf("panicked episode left no outcome journal: %#v", journal)
	}
}

func TestParseOutcomeRejectsBlankSummary(t *testing.T) {
	for _, summary := range []string{"", " ", "\t\n"} {
		raw := `{"status":"done","summary":"` + summary + `"}`
		if _, err := parseOutcome(raw); err == nil {
			t.Fatalf("parseOutcome accepted blank summary %q", summary)
		}
	}
	// A real summary still parses.
	if _, err := parseOutcome(`{"status":"done","summary":"did the thing"}`); err != nil {
		t.Fatalf("parseOutcome rejected a valid outcome: %v", err)
	}
}

func TestComposeSystemContent(t *testing.T) {
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	ctx := context.Background()
	if err := ws.CreateCommitment(ctx, world.Commitment{
		ID:    "commit_episode_prompt",
		Kind:  "project",
		Title: "Ship episode kernel",
		State: "active",
	}); err != nil {
		t.Fatalf("CreateCommitment() error = %v", err)
	}
	id := &world.Identity{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(id.Dir, "digest.md"), []byte("name: Test Daimon\n"), 0o644); err != nil {
		t.Fatalf("write digest: %v", err)
	}

	system := composeSystem(ctx, agent.CognitiveRequest{
		Goal:     "Handle the chat",
		Persona:  "You are friendly.",
		Rules:    "Never reveal secrets.",
		Memories: "User prefers concise answers.",
	}, ws, id, nil)

	for _, want := range []string{
		"name: Test Daimon",
		"project/Ship episode kernel/active/no due",
		"You are friendly.",
		"Never reveal secrets.",
		"User prefers concise answers.",
		"Handle the chat",
		"episode_close",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
}

// TestComposeSystemUsesWorldMemories verifies the strangler switch: when the
// world model has entries relevant to the goal, the Relevant Memories section is
// sourced from world.Retrieve, and the legacy req.Memories is not used.
func TestComposeSystemUsesWorldMemories(t *testing.T) {
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	ctx := context.Background()
	if err := ws.AppendJournal(ctx, world.JournalEntry{
		ID: "j_storage", Kind: "decision", Summary: "chose SQLite for local storage",
	}); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	system := composeSystem(ctx, agent.CognitiveRequest{
		Goal:     "what storage engine did we choose",
		Memories: "LEGACY_MEMORY_SENTINEL",
	}, ws, &world.Identity{Dir: t.TempDir()}, nil)

	if !strings.Contains(system, "chose SQLite for local storage") {
		t.Fatalf("expected world journal hit in memories section:\n%s", system)
	}
	if strings.Contains(system, "LEGACY_MEMORY_SENTINEL") {
		t.Fatalf("legacy memories should be superseded when world has hits:\n%s", system)
	}
	if !strings.Contains(system, "[decision]") {
		t.Fatalf("expected kind label in memories section:\n%s", system)
	}
}

// stubDigester is a fixed value digester for the composer test.
type stubDigester struct{ digest string }

func (s stubDigester) Digest() string { return s.digest }

// TestComposeSystemInjectsValues verifies the high-confidence values digest is
// rendered as its own section when a digester is wired, and omitted otherwise.
func TestComposeSystemInjectsValues(t *testing.T) {
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	ctx := context.Background()
	id := &world.Identity{Dir: t.TempDir()}
	req := agent.CognitiveRequest{Goal: "do a thing"}

	withValues := composeSystem(ctx, req, ws, id, stubDigester{digest: "- [travel] no red-eye flights (confidence 0.90)"})
	if !strings.Contains(withValues, "## Values") || !strings.Contains(withValues, "no red-eye flights") {
		t.Fatalf("values section missing:\n%s", withValues)
	}

	without := composeSystem(ctx, req, ws, id, stubDigester{digest: "   "})
	if strings.Contains(without, "## Values") {
		t.Fatalf("empty digest should omit the values section:\n%s", without)
	}

	nilCase := composeSystem(ctx, req, ws, id, nil)
	if strings.Contains(nilCase, "## Values") {
		t.Fatalf("nil digester should omit the values section:\n%s", nilCase)
	}
}
