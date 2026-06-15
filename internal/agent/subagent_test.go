package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

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
