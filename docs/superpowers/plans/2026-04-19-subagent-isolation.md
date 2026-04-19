# Sub-Agent Isolation & Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify sub-agent execution behind a `SubAgentManager` with true context isolation, structured result aggregation, configurable failure strategies, and Markdown agent definition support.

**Architecture:** Extract common sub-agent lifecycle (session creation, scoped tools, model override, result extraction) into `SubAgentManager`. `AgentTool` and `TeamCoordinator` both delegate to it. Each invocation gets a unique ephemeral session for context isolation.

**Tech Stack:** Go, SQLite (existing), YAML frontmatter parsing

**Spec:** `docs/superpowers/specs/2026-04-19-subagent-isolation-design.md`

---

### Task 1: Add FailureStrategy to AgentSpec

**Files:**
- Modify: `internal/agent/spec.go:60-145`
- Test: `internal/agent/spec_test.go` (create if not exists)

- [ ] **Step 1: Write failing test for FailureStrategy validation**

```go
// internal/agent/spec_test.go
package agent

import "testing"

func TestAgentSpec_Validate_FailureStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy FailureStrategy
		wantErr  bool
	}{
		{"empty defaults to best_effort", "", false},
		{"best_effort valid", StrategyBestEffort, false},
		{"fail_fast valid", StrategyFailFast, false},
		{"invalid strategy", FailureStrategy("invalid"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &AgentSpec{
				Name:            "test",
				Description:     "test agent",
				FailureStrategy: tt.strategy,
			}
			err := spec.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && spec.FailureStrategy == "" {
				if spec.FailureStrategy != StrategyBestEffort {
					t.Errorf("expected default FailureStrategy = best_effort, got %q", spec.FailureStrategy)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestAgentSpec_Validate_FailureStrategy ./internal/agent/ -v`
Expected: FAIL — `FailureStrategy` type and constants not defined

- [ ] **Step 3: Implement FailureStrategy type and validation**

In `internal/agent/spec.go`, add after the `PermissionMode` block (after line 58):

```go
type FailureStrategy string

const (
	StrategyBestEffort FailureStrategy = "best_effort"
	StrategyFailFast   FailureStrategy = "fail_fast"
)
```

Add field to `AgentSpec` struct (after `PermissionMode` field, around line 76):

```go
FailureStrategy FailureStrategy `yaml:"failure_strategy"` // "best_effort" (default) | "fail_fast"
```

In `Validate()`, add default and validation after the `PermissionMode` switch (around line 134):

```go
if s.FailureStrategy == "" {
	s.FailureStrategy = StrategyBestEffort
}
switch s.FailureStrategy {
case StrategyBestEffort, StrategyFailFast:
	// valid
default:
	return fmt.Errorf("agent spec %q: invalid failure_strategy %q", s.Name, s.FailureStrategy)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestAgentSpec_Validate_FailureStrategy ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/spec.go internal/agent/spec_test.go
git commit -m "feat(agent): add FailureStrategy to AgentSpec with validation"
```

---

### Task 2: Add session.Manager.Delete for ephemeral cleanup

**Files:**
- Modify: `internal/session/manager.go:138-157`
- Test: `internal/session/manager_test.go` (create if not exists)

- [ ] **Step 1: Write failing test for Delete**

```go
// internal/session/manager_test.go
package session

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestManager_Delete(t *testing.T) {
	db := setupTestDB(t)
	mgr := NewManager(db)
	ctx := context.Background()

	// Create a session
	sess, err := mgr.Get(ctx, "subagent", "test_123")
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("expected session to be created")
	}

	// Verify it exists
	sess2, err := mgr.Get(ctx, "subagent", "test_123")
	if err != nil {
		t.Fatal(err)
	}
	if sess2.ID != sess.ID {
		t.Fatal("expected same session from cache")
	}

	// Delete it
	if err := mgr.Delete(ctx, "subagent", "test_123"); err != nil {
		t.Fatal(err)
	}

	// Next Get should create a new session with different ID
	sess3, err := mgr.Get(ctx, "subagent", "test_123")
	if err != nil {
		t.Fatal(err)
	}
	if sess3.ID == sess.ID {
		t.Errorf("expected new session after Delete, got same ID %s", sess3.ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestManager_Delete ./internal/session/ -v`
Expected: FAIL — `Delete` method not defined

- [ ] **Step 3: Implement Delete method**

In `internal/session/manager.go`, add after the `Reset` method (after line 157). `Delete` is a lightweight version of `Reset` that skips DB deletion for ephemeral sessions that were never persisted:

```go
// Delete removes an ephemeral session from the in-memory cache and DB.
// Used by SubAgentManager to clean up short-lived sub-agent sessions.
func (m *Manager) Delete(ctx context.Context, channel, channelID string) error {
	key := sessionKey(channel, channelID)

	if v, ok := m.sessions.Load(key); ok {
		sess := v.(*Session)
		_, _ = m.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sess.ID)
		_, _ = m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID)
	}

	m.sessions.Delete(key)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestManager_Delete ./internal/session/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/session/manager.go internal/session/manager_test.go
git commit -m "feat(session): add Delete method for ephemeral sub-agent session cleanup"
```

---

### Task 3: Create SubAgentResult types and extraction logic

**Files:**
- Create: `internal/agent/subagent_result.go`
- Test: `internal/agent/subagent_result_test.go`

- [ ] **Step 1: Write failing tests for result extraction**

```go
// internal/agent/subagent_result_test.go
package agent

import (
	"strings"
	"testing"
)

func TestExtractStructuredResult_ValidXML(t *testing.T) {
	raw := `Here is what I did.

<result>
<status>success</status>
<summary>Created the user authentication module with JWT support.</summary>
<artifacts>/src/auth.go, /src/auth_test.go</artifacts>
</result>`

	result := extractStructuredResult(raw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != StatusSuccess {
		t.Errorf("status = %q, want %q", result.Status, StatusSuccess)
	}
	if result.Summary != "Created the user authentication module with JWT support." {
		t.Errorf("summary = %q", result.Summary)
	}
	if len(result.Artifacts) != 2 {
		t.Errorf("artifacts len = %d, want 2", len(result.Artifacts))
	}
}

func TestExtractStructuredResult_NoBlock(t *testing.T) {
	raw := "Just some plain text output without any structured block."
	result := extractStructuredResult(raw)
	if result != nil {
		t.Errorf("expected nil for missing block, got %+v", result)
	}
}

func TestExtractStructuredResult_ErrorStatus(t *testing.T) {
	raw := `<result>
<status>error</status>
<summary>Failed to compile: missing dependency.</summary>
<artifacts></artifacts>
</result>`

	result := extractStructuredResult(raw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != StatusError {
		t.Errorf("status = %q, want %q", result.Status, StatusError)
	}
	if len(result.Artifacts) != 0 {
		t.Errorf("artifacts should be empty, got %v", result.Artifacts)
	}
}

func TestExtractStructuredResult_NoArtifacts(t *testing.T) {
	raw := `<result>
<status>success</status>
<summary>Reviewed the code and found no issues.</summary>
</result>`

	result := extractStructuredResult(raw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != StatusSuccess {
		t.Errorf("status = %q, want %q", result.Status, StatusSuccess)
	}
	if len(result.Artifacts) != 0 {
		t.Errorf("artifacts should be empty, got %v", result.Artifacts)
	}
}

func TestFormatResultForParent(t *testing.T) {
	r := &SubAgentResult{
		AgentName: "reviewer",
		Status:    StatusSuccess,
		Summary:   "Found 3 issues.",
		Artifacts: []string{"/src/fix.go"},
	}
	out := formatResultForParent(r)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "reviewer") {
		t.Error("output should contain agent name")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestExtractStructuredResult ./internal/agent/ -v`
Expected: FAIL — types and functions not defined

- [ ] **Step 3: Implement SubAgentResult and extraction**

Create `internal/agent/subagent_result.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type SubAgentStatus string

const (
	StatusSuccess    SubAgentStatus = "success"
	StatusError      SubAgentStatus = "error"
	StatusTimeout    SubAgentStatus = "timeout"
	StatusBackground SubAgentStatus = "background"
)

type SubAgentResult struct {
	AgentName  string         `json:"agent_name"`
	Status     SubAgentStatus `json:"status"`
	Summary    string         `json:"summary"`
	Output     string         `json:"output"`
	Artifacts  []string       `json:"artifacts,omitempty"`
	Duration   time.Duration  `json:"duration"`
	TokensUsed int            `json:"tokens_used"`
	Error      string         `json:"error,omitempty"`
}

const subagentOutputInstruction = `

When you have completed the task, output your final response in this format:

<result>
<status>success|error</status>
<summary>One paragraph summary of what was accomplished</summary>
<artifacts>Comma-separated list of file paths, URLs, or key outputs (if any)</artifacts>
</result>
`

var (
	resultBlockRe  = regexp.MustCompile(`(?s)<result>\s*(.*?)\s*</result>`)
	statusRe       = regexp.MustCompile(`(?s)<status>\s*(.*?)\s*</status>`)
	summaryRe      = regexp.MustCompile(`(?s)<summary>\s*(.*?)\s*</summary>`)
	artifactsRe    = regexp.MustCompile(`(?s)<artifacts>\s*(.*?)\s*</artifacts>`)
)

func extractStructuredResult(raw string) *SubAgentResult {
	block := resultBlockRe.FindStringSubmatch(raw)
	if len(block) < 2 {
		return nil
	}
	inner := block[1]

	result := &SubAgentResult{}

	if m := statusRe.FindStringSubmatch(inner); len(m) >= 2 {
		s := strings.TrimSpace(m[1])
		switch SubAgentStatus(s) {
		case StatusSuccess, StatusError:
			result.Status = SubAgentStatus(s)
		default:
			result.Status = StatusSuccess
		}
	} else {
		return nil
	}

	if m := summaryRe.FindStringSubmatch(inner); len(m) >= 2 {
		result.Summary = strings.TrimSpace(m[1])
	}

	if m := artifactsRe.FindStringSubmatch(inner); len(m) >= 2 {
		raw := strings.TrimSpace(m[1])
		if raw != "" {
			for _, a := range strings.Split(raw, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					result.Artifacts = append(result.Artifacts, a)
				}
			}
		}
	}

	return result
}

func summarizeWithLLM(ctx context.Context, provider Provider, model string, agentName string, rawOutput string) (*SubAgentResult, error) {
	truncated := rawOutput
	if len(truncated) > 4000 {
		truncated = truncated[:4000] + "\n...(truncated)"
	}

	prompt := fmt.Sprintf(
		"Summarize this agent output into JSON with fields: status (\"success\" or \"error\"), summary (1 paragraph), artifacts (array of file paths or URLs).\n\nAgent: %s\nOutput:\n%s",
		agentName, truncated)

	req := CompletionRequest{
		Model:     model,
		System:    "You extract structured summaries from agent outputs. Respond with JSON only.",
		Messages:  []CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 256,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Status    string   `json:"status"`
		Summary   string   `json:"summary"`
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
		return nil, fmt.Errorf("parse LLM summary: %w", err)
	}

	status := StatusSuccess
	if parsed.Status == "error" {
		status = StatusError
	}

	return &SubAgentResult{
		AgentName: agentName,
		Status:    status,
		Summary:   parsed.Summary,
		Artifacts: parsed.Artifacts,
	}, nil
}

func formatResultForParent(r *SubAgentResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Agent: %s | Status: %s | Duration: %s\n", r.AgentName, r.Status, r.Duration.Round(time.Millisecond))
	fmt.Fprintf(&sb, "Summary: %s\n", r.Summary)
	if len(r.Artifacts) > 0 {
		fmt.Fprintf(&sb, "Artifacts: %s\n", strings.Join(r.Artifacts, ", "))
	}
	if r.Error != "" {
		fmt.Fprintf(&sb, "Error: %s\n", r.Error)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestExtractStructuredResult|TestFormatResultForParent" ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/subagent_result.go internal/agent/subagent_result_test.go
git commit -m "feat(agent): add SubAgentResult types with template extraction and LLM fallback"
```

---

### Task 4: Create SubAgentManager core (Spawn)

**Files:**
- Create: `internal/agent/subagent.go`
- Test: `internal/agent/subagent_test.go`

- [ ] **Step 1: Write failing test for Spawn with mock provider**

```go
// internal/agent/subagent_test.go
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

type mockProvider struct {
	response string
}

func (m *mockProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	return &CompletionResponse{Text: m.response}, nil
}

func (m *mockProvider) Stream(_ context.Context, req CompletionRequest) (StreamIterator, error) {
	return nil, nil
}

func TestSubAgentManager_Spawn_IndependentSession(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sessions := session.NewManager(db)
	tools := tool.NewRegistry()

	mgr := NewSubAgentManager(
		&mockProvider{response: "<result>\n<status>success</status>\n<summary>Task done.</summary>\n</result>"},
		sessions, db, nil, tools,
		config.AgentConfig{MaxIterations: 2},
		config.LLMConfig{Model: "test-model", MaxTokens: 100},
	)

	spec := &AgentSpec{
		Name:        "test-agent",
		Description: "test",
	}
	_ = spec.Validate()

	ctx := context.Background()

	// Two spawns of the same agent should get different sessions
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
}

func TestSubAgentManager_Spawn_ModelOverride(t *testing.T) {
	var capturedModel string
	provider := &capturingProvider{
		response: "plain output",
		onComplete: func(req CompletionRequest) {
			capturedModel = req.Model
		},
	}

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr := NewSubAgentManager(
		provider, session.NewManager(db), db, nil, tool.NewRegistry(),
		config.AgentConfig{MaxIterations: 1},
		config.LLMConfig{Model: "default-model", MaxTokens: 100},
	)

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

type capturingProvider struct {
	response   string
	onComplete func(CompletionRequest)
}

func (p *capturingProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if p.onComplete != nil {
		p.onComplete(req)
	}
	return &CompletionResponse{Text: p.response}, nil
}

func (p *capturingProvider) Stream(_ context.Context, req CompletionRequest) (StreamIterator, error) {
	return nil, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestSubAgentManager_Spawn" ./internal/agent/ -v`
Expected: FAIL — `SubAgentManager`, `NewSubAgentManager`, `SpawnRequest` not defined

- [ ] **Step 3: Implement SubAgentManager with Spawn**

Create `internal/agent/subagent.go`. This file contains:
- `SubAgentManager` struct and constructor
- `SpawnRequest` type
- `Spawn()` method — creates unique session, builds scoped tools, creates Runtime, runs HandleMessage, extracts result
- `buildSubConfig()` — builds sub-agent config/LLM overrides
- `buildScopedRegistry()` — moved from `agent_tool.go`, made a standalone function
- `captureChannel` and `captureUpdater` — moved from `agent_tool.go`

```go
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

type SubAgentManager struct {
	provider  Provider
	sessions  *session.Manager
	db        *store.DB
	memStore  memory.Store
	tools     *tool.Registry
	cfg       config.AgentConfig
	llmCfg    config.LLMConfig
	bgManager *BackgroundManager
	agentMCP  *AgentMCPManager
}

func NewSubAgentManager(
	provider Provider,
	sessions *session.Manager,
	db *store.DB,
	memStore memory.Store,
	tools *tool.Registry,
	cfg config.AgentConfig,
	llmCfg config.LLMConfig,
) *SubAgentManager {
	return &SubAgentManager{
		provider: provider,
		sessions: sessions,
		db:       db,
		memStore: memStore,
		tools:    tools,
		cfg:      cfg,
		llmCfg:   llmCfg,
	}
}

func (m *SubAgentManager) SetBackgroundManager(bm *BackgroundManager) { m.bgManager = bm }
func (m *SubAgentManager) SetAgentMCPManager(mgr *AgentMCPManager)    { m.agentMCP = mgr }

type SpawnRequest struct {
	Spec        *AgentSpec
	Task        string
	TaskContext string
	ParentID    string
	ParentDepth int
	ChainID     string
}

func (m *SubAgentManager) Spawn(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
	start := time.Now()

	if req.Spec.ExecutionMode == ExecModeBackground {
		return m.spawnBackground(ctx, req)
	}

	sessionID := fmt.Sprintf("subagent_%s_%s", req.Spec.Name, uuid.New().String()[:8])

	scopedTools := buildScopedRegistry(m.tools, req.Spec.Tools)
	subCfg, subLLMCfg := m.buildSubConfig(req.Spec)

	subRuntime := NewRuntime(m.provider, scopedTools, m.sessions, m.db, subCfg, subLLMCfg)
	if m.memStore != nil {
		subRuntime.SetMemoryStore(m.memStore)
	}

	agentID := uuid.New().String()
	chainID := req.ChainID
	if chainID == "" {
		chainID = uuid.New().String()
	}
	subRuntime.SetAgentID(agentID)
	subRuntime.SetParentID(req.ParentID)
	subRuntime.SetDepth(req.ParentDepth + 1)
	subRuntime.SetChainID(chainID)

	userText := req.Task
	if req.TaskContext != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", req.TaskContext, req.Task)
	}

	capture := newCaptureChannel()
	msg := channel.InboundMessage{
		Channel:   "subagent",
		ChannelID: sessionID,
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	execErr := subRuntime.HandleMessage(ctx, capture, msg)

	_ = m.sessions.Delete(ctx, "subagent", sessionID)

	return m.buildResult(ctx, req.Spec.Name, capture, start, execErr)
}

func (m *SubAgentManager) spawnBackground(ctx context.Context, req SpawnRequest) (*SubAgentResult, error) {
	if m.bgManager == nil {
		req.Spec.ExecutionMode = ExecModeSpawn
		return m.Spawn(ctx, req)
	}

	runner := func(bgCtx context.Context) (*AgentResult, error) {
		spawnReq := req
		spawnReq.Spec = copySpec(req.Spec)
		spawnReq.Spec.ExecutionMode = ExecModeSpawn
		result, err := m.Spawn(bgCtx, spawnReq)
		if err != nil {
			return &AgentResult{AgentName: req.Spec.Name, Error: err}, nil
		}
		return &AgentResult{AgentName: req.Spec.Name, Output: result.Summary}, nil
	}

	agentID := m.bgManager.Spawn(ctx, req.Spec, runner)

	return &SubAgentResult{
		AgentName: req.Spec.Name,
		Status:    StatusBackground,
		Summary:   fmt.Sprintf("Background agent started: %s (ID: %s)", req.Spec.Name, agentID),
	}, nil
}

func copySpec(s *AgentSpec) *AgentSpec {
	cp := *s
	return &cp
}

func (m *SubAgentManager) buildSubConfig(spec *AgentSpec) (config.AgentConfig, config.LLMConfig) {
	subCfg := m.cfg
	if spec.MaxIterations > 0 {
		subCfg.MaxIterations = spec.MaxIterations
	}
	if spec.SystemPrompt != "" {
		subCfg.SystemPrompt = spec.SystemPrompt + subagentOutputInstruction
	} else {
		subCfg.SystemPrompt = m.cfg.SystemPrompt + subagentOutputInstruction
	}

	subLLMCfg := m.llmCfg
	if spec.Model != "" {
		subLLMCfg.Model = spec.Model
	}
	if spec.MaxTokens > 0 {
		subLLMCfg.MaxTokens = spec.MaxTokens
	}

	return subCfg, subLLMCfg
}

func (m *SubAgentManager) buildResult(ctx context.Context, name string, capture *captureChannel, start time.Time, execErr error) (*SubAgentResult, error) {
	raw := capture.Collect()
	dur := time.Since(start)

	if execErr != nil {
		return &SubAgentResult{
			AgentName: name,
			Status:    StatusError,
			Output:    raw,
			Error:     execErr.Error(),
			Duration:  dur,
		}, nil
	}

	if result := extractStructuredResult(raw); result != nil {
		result.AgentName = name
		result.Duration = dur
		result.Output = raw
		return result, nil
	}

	result, err := summarizeWithLLM(ctx, m.provider, m.llmCfg.Model, name, raw)
	if err != nil {
		slog.Debug("subagent: LLM summary fallback failed", "agent", name, "err", err)
		summary := raw
		if len(summary) > 500 {
			summary = summary[:500] + "..."
		}
		return &SubAgentResult{
			AgentName: name,
			Status:    StatusSuccess,
			Summary:   summary,
			Output:    raw,
			Duration:  dur,
		}, nil
	}

	result.AgentName = name
	result.Output = raw
	result.Duration = dur
	return result, nil
}

func buildScopedRegistry(parent *tool.Registry, whitelist []string) *tool.Registry {
	scoped := tool.NewRegistry()
	for _, t := range parent.All() {
		name := t.Name()
		if strings.HasPrefix(name, "agent_") {
			continue
		}
		if len(whitelist) > 0 {
			found := false
			for _, w := range whitelist {
				if w == name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		scoped.Register(t)
	}
	return scoped
}

// captureChannel implements channel.Channel by recording outbound messages in memory.
type captureChannel struct {
	mu       sync.Mutex
	messages []string
}

func newCaptureChannel() *captureChannel {
	return &captureChannel{}
}

func (c *captureChannel) Name() string { return "capture" }

func (c *captureChannel) Start(_ context.Context, _ channel.InboundHandler) error {
	return nil
}

func (c *captureChannel) Send(_ context.Context, msg channel.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.Text != "" {
		c.messages = append(c.messages, msg.Text)
	}
	return nil
}

func (c *captureChannel) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return &captureUpdater{ch: c}, nil
}

func (c *captureChannel) Stop(_ context.Context) error { return nil }

func (c *captureChannel) Collect() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return ""
	}
	return c.messages[len(c.messages)-1]
}

type captureUpdater struct {
	ch *captureChannel
}

func (u *captureUpdater) Update(_ string) error { return nil }
func (u *captureUpdater) Finish(text string) error {
	u.ch.mu.Lock()
	defer u.ch.mu.Unlock()
	if text != "" {
		u.ch.messages = append(u.ch.messages, text)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestSubAgentManager_Spawn" ./internal/agent/ -v`
Expected: PASS (may need adjustments based on Runtime dependencies — if HandleMessage requires more wiring, the mock provider should return a simple response that triggers no tool calls)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/subagent.go internal/agent/subagent_test.go
git commit -m "feat(agent): add SubAgentManager with Spawn and context isolation"
```

---

### Task 5: Add SpawnParallel with failure strategies

**Files:**
- Modify: `internal/agent/subagent.go`
- Test: `internal/agent/subagent_test.go`

- [ ] **Step 1: Write failing tests for SpawnParallel**

```go
// append to internal/agent/subagent_test.go

func TestSubAgentManager_SpawnParallel_BestEffort(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr := NewSubAgentManager(
		&mockProvider{response: "<result>\n<status>success</status>\n<summary>Done.</summary>\n</result>"},
		session.NewManager(db), db, nil, tool.NewRegistry(),
		config.AgentConfig{MaxIterations: 1},
		config.LLMConfig{Model: "test", MaxTokens: 100},
	)

	specs := make([]*AgentSpec, 3)
	reqs := make([]SpawnRequest, 3)
	for i := range 3 {
		specs[i] = &AgentSpec{Name: fmt.Sprintf("agent-%d", i), Description: "test"}
		_ = specs[i].Validate()
		reqs[i] = SpawnRequest{Spec: specs[i], Task: fmt.Sprintf("task %d", i)}
	}

	results, err := mgr.SpawnParallel(context.Background(), reqs, StrategyBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSubAgentManager_SpawnParallel ./internal/agent/ -v`
Expected: FAIL — `SpawnParallel` not defined

- [ ] **Step 3: Implement SpawnParallel**

Add to `internal/agent/subagent.go`:

```go
func (m *SubAgentManager) SpawnParallel(ctx context.Context, reqs []SpawnRequest, strategy FailureStrategy) ([]*SubAgentResult, error) {
	results := make([]*SubAgentResult, len(reqs))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, req := range reqs {
		wg.Add(1)
		go func(idx int, r SpawnRequest) {
			defer wg.Done()

			result, err := m.Spawn(ctx, r)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				results[idx] = &SubAgentResult{
					AgentName: r.Spec.Name,
					Status:    StatusError,
					Error:     err.Error(),
				}
				if strategy == StrategyFailFast && firstErr == nil {
					firstErr = fmt.Errorf("sub-agent %s failed: %w", r.Spec.Name, err)
					cancel()
				}
				return
			}

			results[idx] = result

			if result.Status == StatusError && strategy == StrategyFailFast && firstErr == nil {
				firstErr = fmt.Errorf("sub-agent %s failed: %s", r.Spec.Name, result.Error)
				cancel()
			}
		}(i, req)
	}

	wg.Wait()

	if strategy == StrategyFailFast && firstErr != nil {
		return results, firstErr
	}
	return results, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSubAgentManager_SpawnParallel ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/subagent.go internal/agent/subagent_test.go
git commit -m "feat(agent): add SpawnParallel with best_effort and fail_fast strategies"
```

---

### Task 6: Add Markdown agent spec loader

**Files:**
- Modify: `internal/agent/agent_manager.go:106-141,205-221`
- Test: `internal/agent/agent_manager_test.go` (create if not exists)

- [ ] **Step 1: Write failing tests for Markdown loading**

```go
// internal/agent/agent_manager_test.go
package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMarkdownAgentSpec(t *testing.T) {
	content := `---
name: "test-reviewer"
description: "Reviews code for issues."
model: haiku-model
max_iterations: 3
tools:
  - bash
  - file
timeout: "60s"
failure_strategy: fail_fast
tags:
  - review
---

You are a code reviewer.

Focus on correctness and security.
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test-reviewer.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	spec, err := loadMarkdownAgentSpec(path)
	if err != nil {
		t.Fatal(err)
	}

	if spec.Name != "test-reviewer" {
		t.Errorf("name = %q", spec.Name)
	}
	if spec.Model != "haiku-model" {
		t.Errorf("model = %q", spec.Model)
	}
	if spec.MaxIterations != 3 {
		t.Errorf("max_iterations = %d", spec.MaxIterations)
	}
	if len(spec.Tools) != 2 {
		t.Errorf("tools = %v", spec.Tools)
	}
	if spec.FailureStrategy != StrategyFailFast {
		t.Errorf("failure_strategy = %q", spec.FailureStrategy)
	}
	if spec.SystemPrompt != "You are a code reviewer.\n\nFocus on correctness and security." {
		t.Errorf("system_prompt = %q", spec.SystemPrompt)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantYAML  string
		wantBody  string
		wantErr   bool
	}{
		{
			"valid",
			"---\nname: test\n---\nBody text.",
			"name: test",
			"Body text.",
			false,
		},
		{
			"no frontmatter",
			"Just plain text.",
			"",
			"",
			true,
		},
		{
			"unclosed frontmatter",
			"---\nname: test\nno closing",
			"",
			"",
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml, body, err := splitFrontmatter(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if yaml != tt.wantYAML {
					t.Errorf("yaml = %q, want %q", yaml, tt.wantYAML)
				}
				if body != tt.wantBody {
					t.Errorf("body = %q, want %q", body, tt.wantBody)
				}
			}
		})
	}
}

func TestLoadDir_MixedFormats(t *testing.T) {
	dir := t.TempDir()

	yamlContent := "name: yaml-agent\ndescription: yaml agent\n"
	if err := os.WriteFile(filepath.Join(dir, "agent1.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	mdContent := "---\nname: md-agent\ndescription: markdown agent\n---\nYou are helpful.\n"
	if err := os.WriteFile(filepath.Join(dir, "agent2.md"), []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := &AgentManager{}
	if err := mgr.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	specs := mgr.All()
	if len(specs) != 2 {
		t.Errorf("expected 2 specs, got %d", len(specs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestLoadMarkdownAgentSpec|TestSplitFrontmatter|TestLoadDir_MixedFormats" ./internal/agent/ -v`
Expected: FAIL — `loadMarkdownAgentSpec` and `splitFrontmatter` not defined

- [ ] **Step 3: Implement Markdown loader and update LoadDir**

Add to `internal/agent/agent_manager.go` (after `loadAgentSpec` at the end of file):

```go
func loadMarkdownAgentSpec(path string) (*AgentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	data = config.ExpandEnv(data)
	content := string(data)

	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter %s: %w", path, err)
	}

	var spec AgentSpec
	if err := yaml.Unmarshal([]byte(frontmatter), &spec); err != nil {
		return nil, fmt.Errorf("parse yaml %s: %w", path, err)
	}

	spec.SystemPrompt = strings.TrimSpace(body)
	return &spec, nil
}

func splitFrontmatter(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", "", fmt.Errorf("no frontmatter found")
	}
	rest := content[3:]
	if i := strings.Index(rest, "\n"); i >= 0 {
		rest = rest[i+1:]
	}
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return "", "", fmt.Errorf("unclosed frontmatter")
	}
	return strings.TrimSpace(rest[:idx]), strings.TrimSpace(rest[idx+3:]), nil
}
```

Update `LoadDir` method to handle `.md` files:

Replace the existing file extension check in `LoadDir` (line 121):

```go
// Replace:
//   if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
//       continue
//   }
// With:
switch {
case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
	spec, err = loadAgentSpec(path)
case strings.HasSuffix(name, ".md"):
	spec, err = loadMarkdownAgentSpec(path)
default:
	continue
}
```

Restructure the loop body from separate `loadAgentSpec` call to using the switch:

```go
for _, e := range entries {
	if e.IsDir() {
		continue
	}
	name := e.Name()
	path := filepath.Join(dir, name)

	var spec *AgentSpec
	var loadErr error

	switch {
	case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
		spec, loadErr = loadAgentSpec(path)
	case strings.HasSuffix(name, ".md"):
		spec, loadErr = loadMarkdownAgentSpec(path)
	default:
		continue
	}

	if loadErr != nil {
		slog.Warn("agent_manager: skip invalid spec", "file", name, "err", loadErr)
		continue
	}

	if err := m.Add(spec); err != nil {
		slog.Warn("agent_manager: skip invalid spec", "file", name, "err", err)
		continue
	}
}
```

Also add `SetSubAgentManager` method and a `subAgentMgr` field to `AgentManager`:

```go
// Add field to AgentManager struct:
subAgentMgr *SubAgentManager

// Add method:
func (m *AgentManager) SetSubAgentManager(mgr *SubAgentManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subAgentMgr = mgr
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestLoadMarkdownAgentSpec|TestSplitFrontmatter|TestLoadDir_MixedFormats" ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent_manager.go internal/agent/agent_manager_test.go
git commit -m "feat(agent): add Markdown agent spec loader (.md with YAML frontmatter)"
```

---

### Task 7: Simplify AgentTool to delegate to SubAgentManager

**Files:**
- Modify: `internal/agent/agent_tool.go`
- Test: Run existing tests to verify no regression

- [ ] **Step 1: Refactor AgentTool struct**

Replace the struct and constructor in `internal/agent/agent_tool.go` (lines 29-65). The new `AgentTool` holds only `spec`, `manager`, and `breaker`:

```go
type AgentTool struct {
	spec    *AgentSpec
	manager *SubAgentManager
	breaker *CircuitBreaker
}

func NewAgentTool(spec *AgentSpec, manager *SubAgentManager) *AgentTool {
	return &AgentTool{
		spec:    spec,
		manager: manager,
		breaker: NewCircuitBreaker(),
	}
}
```

Remove the `SetBackgroundManager` and `SetAgentMCPManager` methods (lines 68-71) — these are now managed by `SubAgentManager`.

- [ ] **Step 2: Replace Execute method**

Replace the `Execute`, `executeSpawn`, `executeAgentCore`, `executeFork`, `executeBackground` methods (lines 103-478) with:

```go
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if err := a.breaker.Allow(); err != nil {
		return tool.Result{Error: err.Error()}, nil
	}

	var in agentToolInput
	if err := json.Unmarshal(input, &in); err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "invalid input: " + err.Error()}, nil
	}

	if in.Task == "" {
		a.breaker.RecordFailure()
		return tool.Result{Error: "task field is required"}, nil
	}

	timeout := a.spec.Timeout.Duration()
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("agent_tool: executing sub-agent",
		"agent", a.spec.Name,
		"task_len", len(in.Task),
		"timeout", timeout,
		"mode", a.spec.ExecutionMode,
	)

	parentRT := RuntimeFromContext(ctx)
	var parentID string
	var parentDepth int
	var chainID string
	if parentRT != nil {
		parentID = parentRT.AgentID()
		parentDepth = parentRT.Depth()
		chainID = parentRT.ChainID()
	}

	result, err := a.manager.Spawn(ctx, SpawnRequest{
		Spec:        a.spec,
		Task:        in.Task,
		TaskContext:  in.Context,
		ParentID:    parentID,
		ParentDepth: parentDepth,
		ChainID:     chainID,
	})

	if err != nil {
		a.breaker.RecordFailure()
		return tool.Result{Error: "sub-agent error: " + err.Error()}, nil
	}

	if result.Status == StatusError {
		a.breaker.RecordFailure()
		return tool.Result{Error: result.Error}, nil
	}

	a.breaker.RecordSuccess()
	slog.Info("agent_tool: sub-agent completed",
		"agent", a.spec.Name,
		"status", result.Status,
		"duration", result.Duration,
	)

	return tool.Result{Output: formatResultForParent(result)}, nil
}
```

- [ ] **Step 3: Remove moved code**

Remove `buildScopedRegistry` method and `contains` helper (lines 482-514) — moved to `subagent.go`.

Remove `captureChannel`, `newCaptureChannel`, `captureUpdater` types (lines 518-580) — moved to `subagent.go`.

Keep `Name()`, `Description()`, `InputSchema()`, `RequiresApproval()` methods and `agentToolInput` type unchanged.

- [ ] **Step 4: Update AgentManager.RegisterAll**

In `internal/agent/agent_manager.go`, update `RegisterAll` (line 144-163) to use the new `NewAgentTool(spec, manager)`:

```go
func (m *AgentManager) RegisterAll(registry *tool.Registry) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, spec := range m.specs {
		mgr := m.subAgentMgr
		if mgr == nil {
			slog.Warn("agent_manager: no SubAgentManager set, skipping registration", "name", spec.Name)
			continue
		}

		at := NewAgentTool(spec, mgr)
		registry.Register(at)
		slog.Info("agent_manager: registered agent tool",
			"name", at.Name(),
			"tools", spec.Tools,
			"max_iterations", spec.MaxIterations,
		)
	}
}
```

- [ ] **Step 5: Build to verify compilation**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./...`
Expected: Compilation succeeds

- [ ] **Step 6: Run all agent tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v -count=1`
Expected: All tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/agent/agent_tool.go internal/agent/agent_manager.go
git commit -m "refactor(agent): simplify AgentTool to delegate to SubAgentManager"
```

---

### Task 8: Wire SubAgentManager into Gateway

**Files:**
- Modify: `internal/gateway/gateway.go:33-61`
- Modify: `internal/gateway/init_multiagent.go:12-68`

- [ ] **Step 1: Add subAgentMgr field to Gateway struct**

In `internal/gateway/gateway.go`, add field to the `Gateway` struct (after line 57, near `teamCoordinator`):

```go
subAgentMgr     *agent.SubAgentManager
```

- [ ] **Step 2: Create SubAgentManager in initMultiAgent**

In `internal/gateway/init_multiagent.go`, add after `agentMgr` creation (after line 15):

```go
subAgentMgr := agent.NewSubAgentManager(gw.provider, gw.sessions, gw.db, gw.memStore, gw.tools, gw.cfg.Agent, gw.cfg.LLM)
gw.subAgentMgr = subAgentMgr
```

Wire it into `agentMgr` before `RegisterAll` (replace line 27 `agentMgr.RegisterAll(gw.tools)`):

```go
agentMgr.SetSubAgentManager(subAgentMgr)
agentMgr.RegisterAll(gw.tools)
```

After `bgManager` is created (line 41-43), also wire into SubAgentManager:

```go
subAgentMgr.SetBackgroundManager(bgManager)
```

After `agentMCPMgr` is created (line 50-52), wire into SubAgentManager:

```go
subAgentMgr.SetAgentMCPManager(agentMCPMgr)
```

- [ ] **Step 3: Update TeamCoordinator executor**

In `internal/gateway/gateway.go`, replace the `executeTeamTask` single-shot executor (lines 104-115) with:

```go
if cfg.Agent.Team.Enabled {
	maxWorkers := cfg.Agent.Team.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 3
	}
	tc := taskledger.NewTeamCoordinator(gw.taskLedger, maxWorkers)
	tc.SetExecutor(func(ctx context.Context, task taskledger.Task) (string, error) {
		if gw.subAgentMgr == nil {
			return gw.executeTeamTask(ctx, task)
		}
		spec := &agent.AgentSpec{
			Name:          fmt.Sprintf("team_%s", task.ID[:8]),
			Description:   "Team task worker",
			SystemPrompt:  "You are an agent executing a specific task. Be concise and focused.",
			MaxIterations: 10,
		}
		if gw.cfg.Agent.Team.Model != "" {
			spec.Model = gw.cfg.Agent.Team.Model
		}
		_ = spec.Validate()
		result, err := gw.subAgentMgr.Spawn(ctx, agent.SpawnRequest{
			Spec: spec,
			Task: task.Description,
		})
		if err != nil {
			return "", err
		}
		if result.Status == agent.StatusError {
			return "", fmt.Errorf("task failed: %s", result.Error)
		}
		return result.Summary, nil
	})
	gw.teamCoordinator = tc
}
```

Keep the old `executeTeamTask` method as fallback (when `subAgentMgr` is nil, e.g., agents not enabled).

- [ ] **Step 4: Build to verify compilation**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./...`
Expected: Compilation succeeds

- [ ] **Step 5: Run full test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/init_multiagent.go
git commit -m "feat(gateway): wire SubAgentManager into Gateway, upgrade TeamCoordinator executor"
```

---

### Task 9: Integration test — full pipeline

**Files:**
- Create: `internal/agent/subagent_integration_test.go`

- [ ] **Step 1: Write integration test for AgentTool → SubAgentManager → Result pipeline**

```go
// internal/agent/subagent_integration_test.go
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/session"
	"github.com/Forest-Isle/IronClaw/internal/store"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

func TestAgentTool_SubAgentManager_Integration(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sessions := session.NewManager(db)
	tools := tool.NewRegistry()

	mgr := NewSubAgentManager(
		&mockProvider{response: "<result>\n<status>success</status>\n<summary>Integration test passed.</summary>\n<artifacts>/tmp/output.txt</artifacts>\n</result>"},
		sessions, db, nil, tools,
		config.AgentConfig{MaxIterations: 1, SystemPrompt: "You are helpful."},
		config.LLMConfig{Model: "test-model", MaxTokens: 100},
	)

	spec := &AgentSpec{
		Name:        "integration-agent",
		Description: "An agent for integration testing",
	}
	_ = spec.Validate()

	at := NewAgentTool(spec, mgr)

	if at.Name() != "agent_integration-agent" {
		t.Errorf("name = %q", at.Name())
	}

	input, _ := json.Marshal(agentToolInput{Task: "run the integration test"})
	result, err := at.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}

	t.Logf("Output: %s", result.Output)
}
```

- [ ] **Step 2: Run integration test**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestAgentTool_SubAgentManager_Integration ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/subagent_integration_test.go
git commit -m "test(agent): add integration test for AgentTool → SubAgentManager pipeline"
```

---

### Task 10: Update CLAUDE.md with new architecture notes

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add SubAgentManager to Key Packages section**

Add under the `internal/agent/` entry in CLAUDE.md:

```markdown
Sub-agent subsystem: `subagent.go` (SubAgentManager — unified sub-agent lifecycle with context isolation), `subagent_result.go` (structured result extraction with template + LLM fallback). AgentTool delegates to SubAgentManager. TeamCoordinator executor uses SubAgentManager.Spawn() for full agent loops.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md with SubAgentManager architecture notes"
```
