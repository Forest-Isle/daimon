package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/mind"
	"github.com/Forest-Isle/daimon/internal/session"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/tool"
)

// fixedStreamIterator yields a single text response then signals done.
type fixedStreamIterator struct {
	text    string
	yielded bool
}

func (it *fixedStreamIterator) Next() (mind.StreamDelta, error) {
	if !it.yielded {
		it.yielded = true
		return mind.StreamDelta{Text: it.text}, nil
	}
	return mind.StreamDelta{Done: true, StopReason: mind.StopEndTurn}, nil
}

func (it *fixedStreamIterator) Close() {}

// mockSubagentProvider returns a fixed streaming response.
type mockSubagentProvider struct {
	response string
}

func (m *mockSubagentProvider) Complete(_ context.Context, _ mind.CompletionRequest) (*mind.CompletionResponse, error) {
	return &mind.CompletionResponse{Text: m.response}, nil
}

func (m *mockSubagentProvider) Capabilities() mind.Caps { return mind.Caps{} }

func (m *mockSubagentProvider) Stream(_ context.Context, _ mind.CompletionRequest) (mind.StreamIterator, error) {
	return &fixedStreamIterator{text: m.response}, nil
}

// capturingSubagentProvider records the model from the CompletionRequest.
type capturingSubagentProvider struct {
	response   string
	onComplete func(mind.CompletionRequest)
	onStream   func(mind.CompletionRequest)
}

func (p *capturingSubagentProvider) Complete(_ context.Context, req mind.CompletionRequest) (*mind.CompletionResponse, error) {
	if p.onComplete != nil {
		p.onComplete(req)
	}
	return &mind.CompletionResponse{Text: p.response}, nil
}

func (p *capturingSubagentProvider) Capabilities() mind.Caps { return mind.Caps{} }

func (p *capturingSubagentProvider) Stream(_ context.Context, req mind.CompletionRequest) (mind.StreamIterator, error) {
	if p.onStream != nil {
		p.onStream(req)
	}
	return &fixedStreamIterator{text: p.response}, nil
}

type stubCognitiveKernel struct {
	mu              sync.Mutex
	calls           int
	activityClass   string
	parentEpisodeID string
	outcome         CognitiveOutcome
}

func (s *stubCognitiveKernel) Execute(_ context.Context, req CognitiveRequest) (CognitiveOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.activityClass = req.ActivityClass
	s.parentEpisodeID = req.ParentEpisodeID
	return s.outcome, nil
}

func (s *stubCognitiveKernel) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubCognitiveKernel) ActivityClass() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activityClass
}

func (s *stubCognitiveKernel) ParentEpisodeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parentEpisodeID
}

func TestSubAgentManager_Spawn_IndependentSession(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	sessions := session.NewManager(db)
	tools := tool.NewRegistry()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "<result>\n<status>success</status>\n<summary>Task done.</summary>\n</result>"},
			Sessions: sessions,
			DB:       db,
			Tools:    tools,
			Cfg:      config.AgentConfig{MaxIterations: 2},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())

	spec := &AgentSpec{
		Name:        "test-agent",
		Description: "test",
	}
	_ = spec.Validate()

	ctx := context.Background()

	r1, err := mgr.Spawn(ctx, SpawnRequest{Spec: spec, Task: "task 1"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := mgr.Spawn(ctx, SpawnRequest{Spec: spec, Task: "task 2"})
	if err != nil {
		t.Fatal(err)
	}

	if r1.Status != StatusSuccess {
		t.Errorf("r1 status = %q, want success", r1.Status)
	}
	if r2.Status != StatusSuccess {
		t.Errorf("r2 status = %q, want success", r2.Status)
	}
	if r1.Summary == "" {
		t.Error("r1 summary should not be empty")
	}
}

func TestSubAgentManager_Spawn_ModelOverride(t *testing.T) {
	var capturedModel string
	provider := &capturingSubagentProvider{
		response: "plain output",
		onStream: func(req mind.CompletionRequest) {
			capturedModel = req.Model
		},
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: provider,
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "default-model", MaxTokens: 100},
		},
	}.WithDefaults())

	spec := &AgentSpec{
		Name:        "fast-agent",
		Description: "test",
		Model:       "haiku-model",
	}
	_ = spec.Validate()

	_, err = mgr.Spawn(context.Background(), SpawnRequest{Spec: spec, Task: "quick task"})
	if err != nil {
		t.Fatal(err)
	}

	if capturedModel != "haiku-model" {
		t.Errorf("model = %q, want haiku-model", capturedModel)
	}
}

func TestSubAgentManager_Spawn_EpisodeKernelEnabled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())
	kernel := &stubCognitiveKernel{
		outcome: CognitiveOutcome{Status: "done", Summary: "episode subagent complete"},
	}
	mgr.SetEpisodeKernel(kernel, true)

	spec := &AgentSpec{Name: "episode-agent", Description: "test"}
	_ = spec.Validate()

	result, err := mgr.Spawn(context.Background(), SpawnRequest{Spec: spec, Task: "run as episode"})
	if err != nil {
		t.Fatal(err)
	}
	if got := kernel.CallCount(); got != 1 {
		t.Fatalf("kernel calls = %d, want 1", got)
	}
	if got := kernel.ActivityClass(); got != "subagent" {
		t.Errorf("activity class = %q, want subagent", got)
	}
	if !strings.Contains(result.Output, "episode subagent complete") {
		t.Errorf("output = %q, want kernel summary", result.Output)
	}
}

func TestSubAgentManager_SpawnParallel_EpisodeKernelEnabled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())
	kernel := &stubCognitiveKernel{
		outcome: CognitiveOutcome{Status: "done", Summary: "parallel episode subagent complete"},
	}
	mgr.SetEpisodeKernel(kernel, true)

	const n = 4
	reqs := make([]SpawnRequest, n)
	for i := range n {
		spec := &AgentSpec{Name: fmt.Sprintf("episode-agent-%d", i), Description: "test"}
		_ = spec.Validate()
		reqs[i] = SpawnRequest{Spec: spec, Task: fmt.Sprintf("run episode %d", i)}
	}

	results, err := mgr.SpawnParallel(context.Background(), reqs, StrategyBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		if result == nil {
			t.Fatalf("result[%d] is nil", i)
		}
		if !strings.Contains(result.Output, "parallel episode subagent complete") {
			t.Errorf("result[%d].Output = %q, want kernel summary", i, result.Output)
		}
	}
	if got := kernel.CallCount(); got != n {
		t.Fatalf("kernel calls = %d, want %d", got, n)
	}
}

func TestSubAgentManager_Spawn_EpisodeKernelFailedIsTerminal(t *testing.T) {
	var legacyStreams int
	provider := &capturingSubagentProvider{
		response: "legacy fallback should not run",
		onStream: func(mind.CompletionRequest) {
			legacyStreams++
		},
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: provider,
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())
	kernel := &stubCognitiveKernel{
		outcome: CognitiveOutcome{Status: "failed", Summary: "episode failure summary"},
	}
	mgr.SetEpisodeKernel(kernel, true)

	spec := &AgentSpec{Name: "failed-episode-agent", Description: "test"}
	_ = spec.Validate()

	result, err := mgr.Spawn(context.Background(), SpawnRequest{Spec: spec, Task: "fail as episode"})
	if err != nil {
		t.Fatal(err)
	}
	if got := kernel.CallCount(); got != 1 {
		t.Fatalf("kernel calls = %d, want 1", got)
	}
	if legacyStreams != 0 {
		t.Fatalf("legacy stream calls = %d, want 0", legacyStreams)
	}
	if !strings.Contains(result.Output, "episode failure summary") {
		t.Errorf("output = %q, want failed kernel summary", result.Output)
	}
	// A failed episode must not read as success to the parent (status propagation),
	// and must carry a meaningful error (not blank) derived from its summary.
	if result.Status != StatusError {
		t.Errorf("status = %q, want error for a failed episode", result.Status)
	}
	if result.EpisodeStatus != "failed" {
		t.Errorf("episode status = %q, want failed", result.EpisodeStatus)
	}
	if !strings.Contains(result.Error, "episode failure summary") {
		t.Errorf("error = %q, want it to surface the failure summary", result.Error)
	}
}

func TestSubAgentManager_Spawn_EpisodeStatusPropagation(t *testing.T) {
	for _, tc := range []struct {
		episodeStatus string
		wantCoarse    SubAgentStatus
	}{
		{"done", StatusSuccess},
		{"blocked", StatusSuccess},
		{"handed_off", StatusSuccess},
		{"failed", StatusError},
	} {
		t.Run(tc.episodeStatus, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()

			mgr := NewSubAgentManager(AgentDeps{
				Core: CoreDeps{
					Sessions: session.NewManager(db),
					DB:       db,
					Tools:    tool.NewRegistry(),
					Cfg:      config.AgentConfig{MaxIterations: 1},
					LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
				},
			}.WithDefaults())
			kernel := &stubCognitiveKernel{
				outcome: CognitiveOutcome{Status: tc.episodeStatus, Summary: "episode " + tc.episodeStatus},
			}
			mgr.SetEpisodeKernel(kernel, true)

			spec := &AgentSpec{Name: "status-agent", Description: "test"}
			_ = spec.Validate()

			result, err := mgr.Spawn(context.Background(), SpawnRequest{Spec: spec, Task: "run"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.wantCoarse {
				t.Errorf("coarse status = %q, want %q", result.Status, tc.wantCoarse)
			}
			if result.EpisodeStatus != tc.episodeStatus {
				t.Errorf("episode status = %q, want %q", result.EpisodeStatus, tc.episodeStatus)
			}
		})
	}
}

func TestSubAgentManager_Spawn_EpisodeKernelDisabled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "legacy subagent complete"},
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())
	kernel := &stubCognitiveKernel{
		outcome: CognitiveOutcome{Status: "done", Summary: "should not run"},
	}
	mgr.SetEpisodeKernel(kernel, false)

	spec := &AgentSpec{Name: "legacy-agent", Description: "test"}
	_ = spec.Validate()

	_, err = mgr.Spawn(context.Background(), SpawnRequest{Spec: spec, Task: "run legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := kernel.CallCount(); got != 0 {
		t.Fatalf("kernel calls = %d, want 0", got)
	}
}

func TestAgent_HandleMessage_KernelActivityClass(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func(context.Context) context.Context
		want string
	}{
		{
			name: "chat",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			want: "chat",
		},
		{
			name: "subagent",
			ctx: func(ctx context.Context) context.Context {
				return SubagentContextToCtx(ctx, &SubagentContext{AgentID: "subagent-test"})
			},
			want: "subagent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()

			deps := AgentDeps{
				Core: CoreDeps{
					Sessions: session.NewManager(db),
					DB:       db,
					Tools:    tool.NewRegistry(),
					Cfg:      config.AgentConfig{MaxIterations: 1},
					LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
				},
			}.WithDefaults()
			agent := NewAgent(&deps, &LinearLoop{}, NewEventBus())
			kernel := &stubCognitiveKernel{
				outcome: CognitiveOutcome{Status: "done", Summary: "ok"},
			}
			agent.SetKernel(kernel, true)

			capture := newSubagentCapture()
			msg := channel.InboundMessage{
				Channel:   "test",
				ChannelID: tc.name,
				UserID:    "user",
				UserName:  "user",
				Text:      "hello",
			}
			if err := agent.HandleMessage(tc.ctx(context.Background()), capture, msg); err != nil {
				t.Fatal(err)
			}
			if got := kernel.CallCount(); got != 1 {
				t.Fatalf("kernel calls = %d, want 1", got)
			}
			if got := kernel.ActivityClass(); got != tc.want {
				t.Errorf("activity class = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAgent_HandleMessage_KernelParentEpisodeID verifies runKernel reads the
// enclosing episode's id from ctx (installed by a parent episode via
// EpisodeIDToCtx) into CognitiveRequest.ParentEpisodeID, and reads "" when there
// is no parent — the read side of §4.3 parent linkage.
func TestAgent_HandleMessage_KernelParentEpisodeID(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func(context.Context) context.Context
		want string
	}{
		{
			name: "no parent",
			ctx:  func(ctx context.Context) context.Context { return ctx },
			want: "",
		},
		{
			name: "with parent",
			ctx: func(ctx context.Context) context.Context {
				return EpisodeIDToCtx(ctx, "ep_parent_abc")
			},
			want: "ep_parent_abc",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()

			deps := AgentDeps{
				Core: CoreDeps{
					Sessions: session.NewManager(db),
					DB:       db,
					Tools:    tool.NewRegistry(),
					Cfg:      config.AgentConfig{MaxIterations: 1},
					LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
				},
			}.WithDefaults()
			agent := NewAgent(&deps, &LinearLoop{}, NewEventBus())
			kernel := &stubCognitiveKernel{outcome: CognitiveOutcome{Status: "done", Summary: "ok"}}
			agent.SetKernel(kernel, true)

			msg := channel.InboundMessage{Channel: "test", ChannelID: tc.name, UserID: "u", UserName: "u", Text: "hi"}
			if err := agent.HandleMessage(tc.ctx(context.Background()), newSubagentCapture(), msg); err != nil {
				t.Fatal(err)
			}
			if got := kernel.ParentEpisodeID(); got != tc.want {
				t.Errorf("parent episode id = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSubAgentManager_Spawn_BackgroundFallback(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "done"},
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test-model", MaxTokens: 100},
		},
	}.WithDefaults())

	spec := &AgentSpec{
		Name:          "bg-agent",
		Description:   "test background",
		ExecutionMode: ExecModeBackground,
	}
	_ = spec.Validate()

	result, err := mgr.Spawn(context.Background(), SpawnRequest{Spec: spec, Task: "bg task"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("status = %q, want success (should fallback to sync without BackgroundManager)", result.Status)
	}
}

func TestBuildScopedRegistryStandalone(t *testing.T) {
	parent := tool.NewRegistry()
	parent.Register(&dummyTool{name: "bash"})
	parent.Register(&dummyTool{name: "file_read"})
	parent.Register(&dummyTool{name: "agent_coder"})

	t.Run("no whitelist excludes agent_ tools", func(t *testing.T) {
		scoped := buildScopedRegistryStandalone(parent, nil)
		names := toolNames(scoped)
		if containsStr(names, "agent_coder") {
			t.Error("agent_coder should be excluded")
		}
		if !containsStr(names, "bash") || !containsStr(names, "file_read") {
			t.Error("bash and file_read should be included")
		}
	})

	t.Run("whitelist filters to listed tools", func(t *testing.T) {
		scoped := buildScopedRegistryStandalone(parent, []string{"bash"})
		names := toolNames(scoped)
		if !containsStr(names, "bash") {
			t.Error("bash should be included")
		}
		if containsStr(names, "file_read") {
			t.Error("file_read should be excluded by whitelist")
		}
	})
}

type dummyTool struct {
	name string
}

func (d *dummyTool) Name() string                { return d.name }
func (d *dummyTool) Description() string         { return "dummy" }
func (d *dummyTool) InputSchema() map[string]any { return nil }
func (d *dummyTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	return tool.Result{}, nil
}
func (d *dummyTool) RequiresApproval() bool { return false }

func toolNames(r *tool.Registry) []string {
	var names []string
	for _, t := range r.All() {
		names = append(names, t.Name())
	}
	return names
}

func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func TestSubAgentManager_SpawnParallel_BestEffort(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mgr := NewSubAgentManager(AgentDeps{
		Core: CoreDeps{
			Provider: &mockSubagentProvider{response: "<result>\n<status>success</status>\n<summary>Done.</summary>\n</result>"},
			Sessions: session.NewManager(db),
			DB:       db,
			Tools:    tool.NewRegistry(),
			Cfg:      config.AgentConfig{MaxIterations: 1},
			LLMCfg:   config.LLMConfig{Model: "test", MaxTokens: 100},
		},
	}.WithDefaults())

	reqs := make([]SpawnRequest, 3)
	for i := range 3 {
		spec := &AgentSpec{Name: fmt.Sprintf("agent-%d", i), Description: "test"}
		_ = spec.Validate()
		reqs[i] = SpawnRequest{Spec: spec, Task: fmt.Sprintf("task %d", i)}
	}

	results, err := mgr.SpawnParallel(context.Background(), reqs, StrategyBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r == nil {
			t.Errorf("result[%d] is nil", i)
		}
	}
}
