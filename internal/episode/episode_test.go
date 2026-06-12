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
	return &agent.CompletionResponse{Text: p.complete.text, ToolCalls: p.complete.toolCalls}, nil
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
	runner, _ := testRunner(t, provider)

	out, err := runner.Execute(context.Background(), chatRequest("", "hi"))
	if err == nil {
		t.Fatal("Execute() error = nil, want stream error")
	}
	if out.Status != "failed" {
		t.Fatalf("status = %q, want failed", out.Status)
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
	}, ws, id)

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
