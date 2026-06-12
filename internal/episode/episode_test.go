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
	"github.com/Forest-Isle/daimon/internal/tool"
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

type countingTool struct {
	count atomic.Int32
}

func (t *countingTool) Name() string                { return "count_tool" }
func (t *countingTool) Description() string         { return "Count tool calls." }
func (t *countingTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *countingTool) RequiresApproval() bool      { return false }
func (t *countingTool) Execute(context.Context, []byte) (tool.Result, error) {
	t.count.Add(1)
	return tool.Result{Output: "counted"}, nil
}

func openEpisodeWorldTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "episode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testRunner(t *testing.T, p agent.Provider, registry *tool.Registry) (*Runner, *world.Store) {
	t.Helper()
	db := openEpisodeWorldTestDB(t)
	ws := world.NewStore(db.DB)
	if registry == nil {
		registry = tool.NewRegistry()
	}
	id := &world.Identity{Dir: t.TempDir()}
	return NewRunner(p, registry, ws, id), ws
}

func closeCall(input string) agent.ToolUseBlock {
	return agent.ToolUseBlock{ID: "close_1", Name: episodeCloseToolName, Input: input}
}

func TestRunnerBasicHappyPath(t *testing.T) {
	provider := &episodeTestProvider{streams: []providerResponse{{
		text:      "closing",
		toolCalls: []agent.ToolUseBlock{closeCall(`{"status":"done","summary":"Handled request."}`)},
	}}}
	runner, ws := testRunner(t, provider, nil)

	out, err := runner.Run(context.Background(), State{
		ID:      "episode_happy",
		Goal:    "Handle request",
		Trigger: "chat: hello",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Status != "done" || out.Salvaged {
		t.Fatalf("outcome = %#v", out)
	}
	journal, err := ws.ListJournal(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	if len(journal) != 1 || journal[0].EpisodeID != "episode_happy" || journal[0].Summary != "Handled request." {
		t.Fatalf("journal = %#v", journal)
	}
}

func TestRunnerMaxIterationsSalvage(t *testing.T) {
	provider := &episodeTestProvider{
		streams: []providerResponse{
			{text: "I am blocked waiting for credentials."},
			{text: "Still blocked without credentials."},
		},
		complete: providerResponse{text: "not json"},
	}
	runner, _ := testRunner(t, provider, nil)

	out, err := runner.Run(context.Background(), State{
		ID:      "episode_salvage",
		Goal:    "Deploy service",
		Trigger: "chat: deploy now",
		Budget:  Budget{MaxIterations: 2},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !out.Salvaged || out.Status != "blocked" {
		t.Fatalf("outcome = %#v, want salvaged blocked", out)
	}
	if !strings.Contains(out.Summary, "Deploy service") {
		t.Fatalf("summary = %q, want goal context", out.Summary)
	}
}

func TestRunnerStreamError(t *testing.T) {
	streamErr := errors.New("stream failed")
	provider := &episodeTestProvider{streams: []providerResponse{{err: streamErr}}}
	runner, _ := testRunner(t, provider, nil)

	out, err := runner.Run(context.Background(), State{ID: "episode_error", Trigger: "chat: hi"})
	if err == nil {
		t.Fatal("Run() error = nil, want stream error")
	}
	if !out.Salvaged || out.Status != "failed" {
		t.Fatalf("outcome = %#v, want salvaged failed", out)
	}
}

func TestRunnerToolDispatchBeforeClose(t *testing.T) {
	ct := &countingTool{}
	registry := tool.NewRegistry()
	registry.Register(ct)
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
	runner, _ := testRunner(t, provider, registry)

	out, err := runner.Run(context.Background(), State{
		ID:      "episode_tool",
		Goal:    "Use a tool",
		Trigger: "chat: use tool",
		Budget:  Budget{MaxIterations: 3},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Status != "done" || out.Salvaged {
		t.Fatalf("outcome = %#v", out)
	}
	if got := ct.count.Load(); got != 1 {
		t.Fatalf("tool executions = %d, want 1", got)
	}
}

func TestComposePromptContent(t *testing.T) {
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

	system, messages := composePrompt(ctx, State{
		Goal:    "Handle the chat",
		Trigger: "chat: hello",
	}, ws, id)
	if !strings.Contains(system, "name: Test Daimon") {
		t.Fatalf("system prompt missing identity digest:\n%s", system)
	}
	if !strings.Contains(system, "project/Ship episode kernel/active/no due") {
		t.Fatalf("system prompt missing commitments digest:\n%s", system)
	}
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("messages = %#v", messages)
	}
	if !strings.Contains(messages[0].Content, "## Goal\nHandle the chat") {
		t.Fatalf("user message content = %q", messages[0].Content)
	}
}
