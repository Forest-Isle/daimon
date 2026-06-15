package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/hook"
	"github.com/Forest-Isle/daimon/internal/memory"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/tool"
)

type capturePromptProvider struct {
	system string
}

// Capabilities declares a cache breakpoint so the prompt assembly inserts the
// dynamic-boundary marker (these tests assert on its placement).
func (p *capturePromptProvider) Capabilities() mind.Caps { return mind.Caps{CacheBreakpoints: 1} }

func (p *capturePromptProvider) Complete(_ context.Context, req mind.CompletionRequest) (*mind.CompletionResponse, error) {
	p.system = req.System
	return &mind.CompletionResponse{Text: "ok", StopReason: mind.StopEndTurn}, nil
}

func (p *capturePromptProvider) Stream(_ context.Context, req mind.CompletionRequest) (mind.StreamIterator, error) {
	p.system = req.System
	return &testStream{text: "ok"}, nil
}

func TestHandleMessage_PromptFrameKeepsHookContext(t *testing.T) {
	db := newTestDB(t)
	hookMgr := hook.NewManager()
	hookMgr.RegisterOnUserMessage(&contextInjectorHandler{
		context: "worktree: feature/super-agent-runtime",
	})

	provider := &capturePromptProvider{}
	deps := AgentDeps{
		Core: CoreDeps{
			Provider: provider,
			Tools:    tool.NewRegistry(),
			Sessions: session.NewManager(db),
			DB:       db,
			Cfg: config.AgentConfig{
				SystemPrompt:  "You are IronClaw.",
				MaxIterations: 1,
			},
			LLMCfg: config.LLMConfig{Model: "test", MaxTokens: 128},
		},
		Security: SecurityDeps{HookMgr: hookMgr},
	}.WithDefaults()

	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())
	err := a.HandleMessage(context.Background(), &testChannel{}, channel.InboundMessage{
		Channel: "test", ChannelID: "ch1", UserID: "u1", Text: "inspect context",
	})
	if err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	if !strings.Contains(provider.system, "## Environment Context") {
		t.Fatalf("provider system prompt missing environment context: %s", provider.system)
	}
	if !strings.Contains(provider.system, "worktree: feature/super-agent-runtime") {
		t.Fatalf("provider system prompt missing injected hook context: %s", provider.system)
	}
}

func TestPromptFrameLayerOrdering(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			// Caching provider (CacheBreakpoints=1) so the dynamic-boundary layer is placed.
			Provider: &capturePromptProvider{},
			Cfg: config.AgentConfig{
				Personality:     "steady",
				SystemPrompt:    "You are IronClaw.",
				PersistentRules: "Never bypass policy.",
			},
			Tools: tool.NewRegistry(),
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	frame := a.buildPromptFrame(context.Background(), "hello")
	if len(frame.Layers) < 4 {
		t.Fatalf("expected static prompt layers, got %d", len(frame.Layers))
	}

	gotKeys := make([]string, 0, len(frame.Layers))
	for _, layer := range frame.Layers {
		gotKeys = append(gotKeys, layer.Key)
	}
	wantPrefix := []string{
		"static.personality",
		"static.system",
		"static.rules",
		"static.dynamic_boundary",
	}
	for i, want := range wantPrefix {
		if gotKeys[i] != want {
			t.Fatalf("layer %d key = %q, want %q (all keys: %v)", i, gotKeys[i], want, gotKeys)
		}
	}
	for _, layer := range frame.Layers[:4] {
		if layer.Scope != PromptScopeStatic {
			t.Fatalf("layer %s scope = %q, want static", layer.Key, layer.Scope)
		}
	}

	rendered := renderPromptLayers(frame.Layers)
	if strings.Index(rendered, "## Personality") > strings.Index(rendered, "You are IronClaw.") {
		t.Fatalf("personality should render before system prompt: %s", rendered)
	}
	if strings.Index(rendered, mind.DynamicContextMarker) < strings.Index(rendered, "Never bypass policy.") {
		t.Fatalf("dynamic boundary should render after persistent rules: %s", rendered)
	}
}

func TestPromptFrameOmitsBoundaryWhenProviderHasNoCacheBreakpoint(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			// testProvider reports CacheBreakpoints=0 (no caller-placed caching),
			// so the cache marker must not be placed — it would leak as literal text.
			Provider: &testProvider{},
			Cfg: config.AgentConfig{
				SystemPrompt:    "You are IronClaw.",
				PersistentRules: "Never bypass policy.",
			},
			Tools: tool.NewRegistry(),
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	frame := a.buildPromptFrame(context.Background(), "hello")
	for _, layer := range frame.Layers {
		if layer.Key == "static.dynamic_boundary" {
			t.Fatal("dynamic-boundary layer must be omitted for a non-caching provider")
		}
	}
	if strings.Contains(renderPromptLayers(frame.Layers), mind.DynamicContextMarker) {
		t.Fatal("cache marker leaked into prompt for a non-caching provider")
	}
}

func TestPromptFrameIgnoresLegacyPlanMetadata(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg:   config.AgentConfig{SystemPrompt: "You are IronClaw."},
			Tools: tool.NewRegistry(),
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	frame := a.buildPromptFrame(context.Background(), "work on task")
	sess := &session.Session{Metadata: map[string]string{}}
	first := a.renderPromptFrame(context.Background(), frame, sess)
	if strings.Contains(first, "Current Plan") {
		t.Fatalf("unexpected plan in first render: %s", first)
	}

	sess.SetMetadata("plan", `{"goal":"Ship PromptFrame","steps":[{"id":"1","status":"in_progress"}]}`)
	second := a.renderPromptFrame(context.Background(), frame, sess)
	if strings.Contains(second, "Current Plan") || strings.Contains(second, "Ship PromptFrame") {
		t.Fatalf("legacy plan metadata should not be injected: %s", second)
	}

	for _, layer := range frame.Layers {
		if layer.Key == "iteration.current_plan" {
			t.Fatal("iteration plan layer should not mutate the per-turn frame")
		}
	}
}

func TestPromptFrameUsesUnifiedMemoryRetriever(t *testing.T) {
	store := &promptFrameMemoryStore{
		results: []memory.SearchResult{
			{
				Entry: memory.Entry{
					ID:      "mem_semantic",
					Content: "The project test command requires -tags fts5.",
					Metadata: map[string]string{
						"type": string(memory.Semantic),
					},
				},
				Score: 1,
			},
		},
		proceduralResults: []memory.SearchResult{
			{
				Entry: memory.Entry{
					ID:      "strat_1",
					Content: `{"TaskPattern":"verified Go runtime changes","ToolSequence":["go test -tags fts5 ./..."],"ContextHints":["run vet after tests"],"SuccessRate":1,"LastUsed":"2026-06-11T00:00:00Z"}`,
				},
				Score: 1,
			},
		},
	}
	cortex := memory.NewUnifiedRetriever(store, memory.NewProceduralStore(store, nil), nil)
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg:   config.AgentConfig{SystemPrompt: "You are IronClaw."},
			Tools: tool.NewRegistry(),
		},
		Memory: MemoryDeps{
			Store:  store,
			Cortex: cortex,
		},
	}.WithDefaults()
	a := NewAgent(&deps, &LinearLoop{}, NewEventBus())

	prompt := a.buildSystemPrompt(context.Background(), nil, "verify runtime changes")
	if !strings.Contains(prompt, "## Knowledge Context") {
		t.Fatalf("prompt missing unified semantic section: %s", prompt)
	}
	if !strings.Contains(prompt, "The project test command requires -tags fts5.") {
		t.Fatalf("prompt missing semantic memory: %s", prompt)
	}
	if !strings.Contains(prompt, "## Past Successful Strategies") {
		t.Fatalf("prompt missing procedural section: %s", prompt)
	}
	if !strings.Contains(prompt, "verified Go runtime changes") {
		t.Fatalf("prompt missing procedural strategy: %s", prompt)
	}
}

type promptFrameMemoryStore struct {
	results           []memory.SearchResult
	proceduralResults []memory.SearchResult
}

func (s *promptFrameMemoryStore) Save(context.Context, memory.Entry) error { return nil }

func (s *promptFrameMemoryStore) Search(_ context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
	if query.TypeFilter == "procedural" {
		return s.proceduralResults, nil
	}
	return s.results, nil
}

func (s *promptFrameMemoryStore) ListByScope(context.Context, memory.MemoryScope, string) ([]memory.Entry, error) {
	return nil, nil
}

func (s *promptFrameMemoryStore) Update(context.Context, string, string, int) error { return nil }
func (s *promptFrameMemoryStore) Delete(context.Context, string) error              { return nil }

func TestPromptFrameRenderedEventIncludesMetrics(t *testing.T) {
	deps := AgentDeps{
		Core: CoreDeps{
			Cfg: config.AgentConfig{
				SystemPrompt: "You are IronClaw.",
				Compression: config.CompressionConfig{
					TokenEstimateRatio: 0.5,
				},
			},
			Tools: tool.NewRegistry(),
		},
	}.WithDefaults()

	bus := NewEventBus()
	events := make(chan PromptFrameRendered, 1)
	sub := bus.Subscribe(func(event Event) {
		if rendered, ok := event.(PromptFrameRendered); ok {
			events <- rendered
		}
	})
	defer sub.Unsubscribe()

	a := NewAgent(&deps, &LinearLoop{}, bus)
	frame := a.buildPromptFrame(context.Background(), "work on metrics")
	frame.AddLayer(PromptLayer{
		Key:      "turn.test_context",
		Scope:    PromptScopeTurn,
		Priority: promptPriorityHooks,
		Content:  "## Environment Context\nbranch: prompt-frame-metrics",
	})
	sess := &session.Session{
		ID:       "sess_metrics",
		Metadata: map[string]string{},
	}

	rendered := a.renderPromptFrameForIteration(context.Background(), frame, sess, 2)
	event := waitPromptFrameRendered(t, events)

	if event.SessionID != "sess_metrics" {
		t.Fatalf("SessionID = %q, want sess_metrics", event.SessionID)
	}
	if event.Iteration != 2 {
		t.Fatalf("Iteration = %d, want 2", event.Iteration)
	}
	if event.LayerCount != len(event.LayerKeys) {
		t.Fatalf("LayerCount = %d, len(LayerKeys) = %d", event.LayerCount, len(event.LayerKeys))
	}
	if event.ScopeCounts[PromptScopeStatic] == 0 {
		t.Fatalf("expected static scope count, got %#v", event.ScopeCounts)
	}
	if event.ScopeCounts[PromptScopeTurn] != 1 {
		t.Fatalf("turn scope count = %d, want 1", event.ScopeCounts[PromptScopeTurn])
	}
	if event.ScopeCounts[PromptScopeIteration] != 0 {
		t.Fatalf("iteration scope count = %d, want 0", event.ScopeCounts[PromptScopeIteration])
	}
	if event.CharacterCount != len(rendered) {
		t.Fatalf("CharacterCount = %d, want %d", event.CharacterCount, len(rendered))
	}
	if event.EstimatedTokens != int(float64(len(rendered))*0.5) {
		t.Fatalf("EstimatedTokens = %d, want %d", event.EstimatedTokens, int(float64(len(rendered))*0.5))
	}
	if containsString(event.LayerKeys, "iteration.current_plan") {
		t.Fatalf("LayerKeys should not include iteration.current_plan: %#v", event.LayerKeys)
	}
}

func waitPromptFrameRendered(t *testing.T, events <-chan PromptFrameRendered) PromptFrameRendered {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PromptFrameRendered event")
	}
	return PromptFrameRendered{}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
