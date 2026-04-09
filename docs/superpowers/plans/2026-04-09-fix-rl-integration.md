# Fix RL Integration Gaps — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close four integration gaps in the RL system so that DQN replan decisions actually influence behavior, memory lifecycle events produce RL reward signals, reflection lessons contribute to reward shaping, and user feedback flows from Telegram/TUI into episode rewards.

**Architecture:** Event-driven bridge between memory and RL (new `RLEventHandler` interface in `memory` package, concrete handler in `rl` package). DQN replan output merged with LLM confidence using a configurable weight. User feedback collected via a new `FeedbackSender` channel interface that follows the existing `ApprovalSender`/`ReflectionSender` pattern. All changes are additive — existing behavior preserved when RL is disabled.

**Tech Stack:** Go 1.22+, CGO_ENABLED=1, `-tags fts5`, Telegram Bot API (`go-telegram-bot-api/telegram-bot-api`), Bubble Tea (TUI).

**Worktree:** `/Users/wuqisen/learning/IronClaw/.worktrees/fix-rl-integration`

**Test command:** `CGO_ENABLED=1 go test -tags "fts5" -run <TestName> ./<package>/... -v`

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/memory/rl_event.go` | `RLEventHandler` interface definition |
| Create | `internal/memory/rl_event_test.go` | Interface contract test (mock handler) |
| Create | `internal/rl/memory_handler.go` | `rl.MemoryRLHandler` implementing `memory.RLEventHandler` |
| Create | `internal/rl/memory_handler_test.go` | Unit tests for reward computation and experience creation |
| Modify | `internal/memory/lifecycle.go` | Inject `RLEventHandler`; emit events on ADD/UPDATE/DELETE/conflict |
| Modify | `internal/memory/lifecycle_test.go` | Add tests verifying event emission |
| Modify | `internal/gateway/init_cognitive.go` | Wire `MemoryRLHandler` into `LifecycleManager` |
| Modify | `internal/agent/cognitive.go:321-347` | Use DQN replan output to adjust confidence |
| Modify | `internal/agent/cognitive.go:483-566` | Add reflection bonus to episode reward |
| Modify | `internal/agent/rl_helpers.go` | Add `computeReflectionBonus` helper |
| Modify | `internal/agent/rl_helpers_test.go` | Tests for reflection bonus + DQN integration |
| Modify | `internal/config/config.go` | Add `DQNReplanWeight`, `MemoryRLRewards` config fields |
| Modify | `internal/channel/channel.go` | Add `FeedbackSender` interface |
| Modify | `internal/channel/telegram/adapter.go` | Implement `FeedbackSender` |
| Modify | `internal/channel/tui/adapter.go` | Implement `FeedbackSender` |
| Modify | `internal/agent/cognitive.go:304-319` | Collect user feedback after streaming answer |
| Modify | `internal/agent/cognitive_types.go` | Add `FeedbackCollector` interface |

---

### Task 1: DQN Replan Decision Integration

**Files:**
- Modify: `internal/config/config.go:189-199` (DQNConfig)
- Modify: `internal/agent/cognitive.go:321-347`
- Modify: `internal/agent/rl_helpers.go`
- Modify: `internal/agent/rl_helpers_test.go`

- [ ] **Step 1: Write failing test for DQN confidence adjustment**

In `internal/agent/rl_helpers_test.go`, add:

```go
func TestApplyDQNReplanAdjustment(t *testing.T) {
	tests := []struct {
		name           string
		llmConfidence  float64
		dqnAction      rl.ReplanActionType
		dqnWeight      float64
		wantConfidence float64
		wantAbort      bool
	}{
		{
			name:           "DQN continue boosts confidence",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionContinue,
			dqnWeight:      0.3,
			wantConfidence: 0.4*0.7 + 1.0*0.3, // 0.58
			wantAbort:      false,
		},
		{
			name:           "DQN adjust keeps confidence unchanged",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionAdjust,
			dqnWeight:      0.3,
			wantConfidence: 0.4*0.7 + 0.5*0.3, // 0.43
			wantAbort:      false,
		},
		{
			name:           "DQN abort signals abort",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionAbort,
			dqnWeight:      0.3,
			wantConfidence: 0.4, // unchanged
			wantAbort:      true,
		},
		{
			name:           "zero weight means DQN has no effect",
			llmConfidence:  0.4,
			dqnAction:      rl.ReplanActionContinue,
			dqnWeight:      0.0,
			wantConfidence: 0.4,
			wantAbort:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adj, abort := applyDQNReplanAdjustment(tc.llmConfidence, tc.dqnAction, tc.dqnWeight)
			if abort != tc.wantAbort {
				t.Errorf("abort: got %v, want %v", abort, tc.wantAbort)
			}
			if !abort {
				diff := adj - tc.wantConfidence
				if diff < -0.01 || diff > 0.01 {
					t.Errorf("confidence: got %.4f, want %.4f", adj, tc.wantConfidence)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestApplyDQNReplanAdjustment ./internal/agent/ -v`
Expected: FAIL — `applyDQNReplanAdjustment` undefined

- [ ] **Step 3: Add DQNReplanWeight to config**

In `internal/config/config.go`, add `DQNReplanWeight` to `DQNConfig`:

```go
// DQNConfig configures Deep Q-Network for replan decisions.
type DQNConfig struct {
	Enabled          bool    `yaml:"enabled"`
	LearningRate     float64 `yaml:"learning_rate"`
	Gamma            float64 `yaml:"gamma"`
	EpsilonStart     float64 `yaml:"epsilon_start"`
	EpsilonEnd       float64 `yaml:"epsilon_end"`
	EpsilonDecay     float64 `yaml:"epsilon_decay"`
	TargetUpdateFreq int     `yaml:"target_update_freq"`
	BufferSize       int     `yaml:"buffer_size"`
	ReplanWeight     float64 `yaml:"replan_weight"` // DQN influence on replan decision (0-1, default 0.3)
}
```

- [ ] **Step 4: Implement applyDQNReplanAdjustment in rl_helpers.go**

In `internal/agent/rl_helpers.go`, add:

```go
// applyDQNReplanAdjustment blends DQN replan output with LLM confidence.
// Returns the adjusted confidence and whether the DQN recommends aborting.
// When dqnWeight is 0, LLM confidence passes through unchanged.
func applyDQNReplanAdjustment(llmConfidence float64, dqnAction rl.ReplanActionType, dqnWeight float64) (adjustedConfidence float64, shouldAbort bool) {
	if dqnWeight <= 0 {
		return llmConfidence, false
	}
	if dqnWeight > 1 {
		dqnWeight = 1
	}

	switch dqnAction {
	case rl.ReplanActionAbort:
		return llmConfidence, true
	case rl.ReplanActionContinue:
		// DQN says "continue" → blend in a high-confidence signal (1.0)
		llmWeight := 1.0 - dqnWeight
		return clampRL(llmConfidence*llmWeight+1.0*dqnWeight, 0, 1), false
	case rl.ReplanActionAdjust:
		// DQN says "adjust" → blend in a neutral signal (0.5)
		llmWeight := 1.0 - dqnWeight
		return clampRL(llmConfidence*llmWeight+0.5*dqnWeight, 0, 1), false
	default:
		return llmConfidence, false
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestApplyDQNReplanAdjustment ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 6: Integrate DQN into cognitive loop**

In `internal/agent/cognitive.go`, replace the block at lines 321-328 (inside the `if rlEnabled && rlState != nil && reflection != nil` block):

**Before:**
```go
		// RL: update reflection confidence and record DQN suggestion
		if rlEnabled && rlState != nil && reflection != nil {
			rlState.ReflectionConfidence = clampRL(reflection.OverallConfidence, 0, 1)
			if reflection.NeedsReplan {
				dqnAction := ca.rlPolicy.SelectReplanAction(rlState)
				slog.Info("cognitive: DQN replan suggestion", "action", dqnAction.String())
			}
		}
```

**After:**
```go
		// RL: update reflection confidence and apply DQN replan adjustment
		if rlEnabled && rlState != nil && reflection != nil {
			rlState.ReflectionConfidence = clampRL(reflection.OverallConfidence, 0, 1)
			if reflection.NeedsReplan {
				dqnAction := ca.rlPolicy.SelectReplanAction(rlState)
				dqnWeight := ca.cfg.RL.DQN.ReplanWeight
				if dqnWeight <= 0 {
					dqnWeight = 0.3
				}
				adjConfidence, shouldAbort := applyDQNReplanAdjustment(
					reflection.OverallConfidence, dqnAction, dqnWeight,
				)
				slog.Info("cognitive: DQN replan adjustment",
					"action", dqnAction.String(),
					"original_confidence", reflection.OverallConfidence,
					"adjusted_confidence", adjConfidence,
					"should_abort", shouldAbort,
				)
				if shouldAbort {
					slog.Info("cognitive: DQN recommends abort", "session", sess.ID)
					goto persist
				}
				reflection.OverallConfidence = adjConfidence
			}
		}
```

- [ ] **Step 7: Run all agent tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v`
Expected: All tests PASS (including existing rl_helpers tests)

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/agent/rl_helpers.go internal/agent/rl_helpers_test.go internal/agent/cognitive.go
git commit -m "feat(rl): integrate DQN replan output into cognitive loop decision

DQN SelectReplanAction result now influences the actual replan decision
instead of only being logged. Uses configurable weight (default 0.3)
to blend DQN signal with LLM confidence. DQN abort recommendation
causes immediate termination of the replan loop."
```

---

### Task 2: Memory-RL Event Handler Interface

**Files:**
- Create: `internal/memory/rl_event.go`
- Create: `internal/memory/rl_event_test.go`

- [ ] **Step 1: Write failing test for event handler interface**

Create `internal/memory/rl_event_test.go`:

```go
package memory

import (
	"context"
	"testing"
)

// mockRLEventHandler records calls for testing.
type mockRLEventHandler struct {
	addCalls      []rlAddEvent
	updateCalls   []rlUpdateEvent
	deleteCalls   []rlDeleteEvent
	conflictCalls []rlConflictEvent
}

type rlAddEvent struct {
	factID, content string
	importance      int
}

type rlUpdateEvent struct {
	oldID, newID, content string
}

type rlDeleteEvent struct {
	factID string
}

type rlConflictEvent struct {
	factID      string
	conflictIDs []string
}

func (m *mockRLEventHandler) OnMemoryAdd(_ context.Context, factID, content string, importance int) {
	m.addCalls = append(m.addCalls, rlAddEvent{factID, content, importance})
}

func (m *mockRLEventHandler) OnMemoryUpdate(_ context.Context, oldID, newID, content string) {
	m.updateCalls = append(m.updateCalls, rlUpdateEvent{oldID, newID, content})
}

func (m *mockRLEventHandler) OnMemoryDelete(_ context.Context, factID string) {
	m.deleteCalls = append(m.deleteCalls, rlDeleteEvent{factID})
}

func (m *mockRLEventHandler) OnMemoryConflict(_ context.Context, factID string, conflictIDs []string) {
	m.conflictCalls = append(m.conflictCalls, rlConflictEvent{factID, conflictIDs})
}

func TestMockRLEventHandlerSatisfiesInterface(t *testing.T) {
	var h RLEventHandler = &mockRLEventHandler{}
	if h == nil {
		t.Fatal("mock does not implement RLEventHandler")
	}
	h.OnMemoryAdd(context.Background(), "id1", "test content", 5)
	h.OnMemoryUpdate(context.Background(), "id1", "id2", "updated content")
	h.OnMemoryDelete(context.Background(), "id1")
	h.OnMemoryConflict(context.Background(), "id1", []string{"id2", "id3"})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestMockRLEventHandlerSatisfiesInterface ./internal/memory/ -v`
Expected: FAIL — `RLEventHandler` type not found

- [ ] **Step 3: Create RLEventHandler interface**

Create `internal/memory/rl_event.go`:

```go
package memory

import "context"

// RLEventHandler receives notifications about memory lifecycle events.
// Implementations convert these events into RL reward signals.
// All methods are fire-and-forget — errors are logged internally, not propagated.
type RLEventHandler interface {
	// OnMemoryAdd is called after a new memory is successfully stored.
	OnMemoryAdd(ctx context.Context, factID, content string, importance int)

	// OnMemoryUpdate is called after an existing memory is archived and replaced.
	OnMemoryUpdate(ctx context.Context, oldID, newID, content string)

	// OnMemoryDelete is called after a memory is archived (invalidated).
	OnMemoryDelete(ctx context.Context, factID string)

	// OnMemoryConflict is called when a new fact conflicts with existing memories.
	OnMemoryConflict(ctx context.Context, factID string, conflictIDs []string)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestMockRLEventHandlerSatisfiesInterface ./internal/memory/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/rl_event.go internal/memory/rl_event_test.go
git commit -m "feat(memory): add RLEventHandler interface for memory-RL bridge

Defines the event-driven contract between memory lifecycle and RL system.
Four events: OnMemoryAdd, OnMemoryUpdate, OnMemoryDelete, OnMemoryConflict."
```

---

### Task 3: Emit RL Events from LifecycleManager

**Files:**
- Modify: `internal/memory/lifecycle.go`
- Modify: `internal/memory/lifecycle_test.go` (or create if needed)

- [ ] **Step 1: Write failing test for event emission**

Add to `internal/memory/rl_event_test.go`:

```go
func TestLifecycleManagerEmitsRLEvents(t *testing.T) {
	handler := &mockRLEventHandler{}
	lm := &LifecycleManager{}
	lm.SetRLEventHandler(handler)

	if lm.rlHandler != handler {
		t.Fatal("rlHandler not set")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestLifecycleManagerEmitsRLEvents ./internal/memory/ -v`
Expected: FAIL — `SetRLEventHandler` undefined, `rlHandler` unexported/missing

- [ ] **Step 3: Add RLEventHandler field and setter to LifecycleManager**

In `internal/memory/lifecycle.go`, add the field to the struct and the setter method:

Add field after `audit *AuditLogger`:
```go
	rlHandler RLEventHandler
```

Add setter method after `SetAuditLogger`:
```go
// SetRLEventHandler attaches an optional RL event handler to the lifecycle manager.
// This is called after construction because the RL system may be initialized after
// the lifecycle manager is created.
func (lm *LifecycleManager) SetRLEventHandler(h RLEventHandler) {
	lm.rlHandler = h
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestLifecycleManagerEmitsRLEvents ./internal/memory/ -v`
Expected: PASS

- [ ] **Step 5: Emit events in executeAdd, executeUpdate, executeDelete**

In `internal/memory/lifecycle.go`, add RL event emission at the end of each execute method.

In `executeAdd`, after the audit log block (after line 300), add:
```go
	// Notify RL event handler
	if lm.rlHandler != nil {
		lm.rlHandler.OnMemoryAdd(ctx, factID, fact.Content, fact.Importance)
	}
```

In `executeUpdate`, after the audit log block (after line 359), add:
```go
	// Notify RL event handler
	if lm.rlHandler != nil {
		lm.rlHandler.OnMemoryUpdate(ctx, targetID, newFactID, fact.Content)
	}
```

In `executeDelete`, after the audit log block (after line 379), add:
```go
	// Notify RL event handler
	if lm.rlHandler != nil {
		lm.rlHandler.OnMemoryDelete(ctx, targetID)
	}
```

In `Process`, after the LLM decision (after line 168, where decision is logged), add conflict event emission:
```go
	// Notify RL about detected conflicts
	if lm.rlHandler != nil && len(decision.ConflictingIDs) > 0 {
		lm.rlHandler.OnMemoryConflict(ctx, fact.Content, decision.ConflictingIDs)
	}
```

- [ ] **Step 6: Run all memory tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/memory/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/memory/lifecycle.go internal/memory/rl_event_test.go
git commit -m "feat(memory): emit RL events from lifecycle manager

LifecycleManager now fires OnMemoryAdd/Update/Delete/Conflict events
through the optional RLEventHandler. Events are emitted after the
memory operation completes successfully."
```

---

### Task 4: MemoryRLHandler — Concrete RL Event Consumer

**Files:**
- Create: `internal/rl/memory_handler.go`
- Create: `internal/rl/memory_handler_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/rl/memory_handler_test.go`:

```go
package rl

import (
	"context"
	"testing"
)

// mockTrainerBuffer collects added experiences for assertions.
type mockTrainerBuffer struct {
	experiences []Experience
}

func (m *mockTrainerBuffer) AddExperience(exp Experience) {
	m.experiences = append(m.experiences, exp)
}

func TestMemoryRLHandlerOnAdd(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryAdd(context.Background(), "fact1", "user likes Go", 7)

	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	exp := buf.experiences[0]
	if exp.Level != LevelBandit {
		t.Errorf("level: got %s, want %s", exp.Level, LevelBandit)
	}
	if exp.Reward != 0.1 {
		t.Errorf("reward: got %f, want 0.1", exp.Reward)
	}
	if !exp.Done {
		t.Error("expected done=true")
	}
}

func TestMemoryRLHandlerOnUpdate(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryUpdate(context.Background(), "old1", "new1", "updated info")

	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	if buf.experiences[0].Reward != 0.3 {
		t.Errorf("reward: got %f, want 0.3", buf.experiences[0].Reward)
	}
}

func TestMemoryRLHandlerOnDelete(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryDelete(context.Background(), "fact1")

	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	if buf.experiences[0].Reward != 0.2 {
		t.Errorf("reward: got %f, want 0.2", buf.experiences[0].Reward)
	}
}

func TestMemoryRLHandlerOnConflict(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryConflict(context.Background(), "fact1", []string{"c1", "c2"})

	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	if buf.experiences[0].Reward != -0.5 {
		t.Errorf("reward: got %f, want -0.5", buf.experiences[0].Reward)
	}
}

func TestMemoryRLHandlerNilBuffer(t *testing.T) {
	// Should not panic with nil buffer
	h := NewMemoryRLHandler(nil, DefaultMemoryRLRewards())
	h.OnMemoryAdd(context.Background(), "fact1", "content", 5)
	// No panic = pass
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestMemoryRLHandler ./internal/rl/ -v`
Expected: FAIL — `NewMemoryRLHandler`, `DefaultMemoryRLRewards` undefined

- [ ] **Step 3: Implement MemoryRLHandler**

Create `internal/rl/memory_handler.go`:

```go
package rl

import (
	"context"
	"log/slog"
)

// ExperienceAdder is the subset of Trainer that MemoryRLHandler needs.
// Decouples the handler from the full Trainer to simplify testing.
type ExperienceAdder interface {
	AddExperience(exp Experience)
}

// MemoryRLRewards configures the reward magnitudes for memory lifecycle events.
type MemoryRLRewards struct {
	AddReward      float64 // reward for storing new knowledge (default: +0.1)
	UpdateReward   float64 // reward for correcting outdated info (default: +0.3)
	DeleteReward   float64 // reward for removing invalid info (default: +0.2)
	ConflictReward float64 // penalty for information inconsistency (default: -0.5)
}

// DefaultMemoryRLRewards returns the default reward configuration.
func DefaultMemoryRLRewards() MemoryRLRewards {
	return MemoryRLRewards{
		AddReward:      0.1,
		UpdateReward:   0.3,
		DeleteReward:   0.2,
		ConflictReward: -0.5,
	}
}

// MemoryRLHandler implements memory.RLEventHandler, converting memory lifecycle
// events into RL experiences and feeding them to the trainer's replay buffer.
type MemoryRLHandler struct {
	adder   ExperienceAdder
	rewards MemoryRLRewards
}

// NewMemoryRLHandler creates a new handler. If adder is nil, events are silently dropped.
func NewMemoryRLHandler(adder ExperienceAdder, rewards MemoryRLRewards) *MemoryRLHandler {
	return &MemoryRLHandler{
		adder:   adder,
		rewards: rewards,
	}
}

// OnMemoryAdd emits a positive reward for acquiring new knowledge.
func (h *MemoryRLHandler) OnMemoryAdd(_ context.Context, factID, content string, importance int) {
	h.emit(h.rewards.AddReward, "memory_add", factID)
}

// OnMemoryUpdate emits a positive reward for correcting outdated information.
func (h *MemoryRLHandler) OnMemoryUpdate(_ context.Context, oldID, newID, content string) {
	h.emit(h.rewards.UpdateReward, "memory_update", newID)
}

// OnMemoryDelete emits a positive reward for cleaning up invalid information.
func (h *MemoryRLHandler) OnMemoryDelete(_ context.Context, factID string) {
	h.emit(h.rewards.DeleteReward, "memory_delete", factID)
}

// OnMemoryConflict emits a negative reward when information inconsistency is detected.
func (h *MemoryRLHandler) OnMemoryConflict(_ context.Context, factID string, conflictIDs []string) {
	h.emit(h.rewards.ConflictReward, "memory_conflict", factID)
	slog.Debug("rl: memory conflict detected", "fact", factID, "conflicts", conflictIDs)
}

// emit creates a minimal bandit-level experience and sends it to the trainer.
func (h *MemoryRLHandler) emit(reward float64, eventType, factID string) {
	if h.adder == nil {
		return
	}
	h.adder.AddExperience(Experience{
		State:  &RLState{}, // zero state — memory events lack cognitive context
		Action: []float64{reward},
		Reward: reward,
		Done:   true,
		Level:  LevelBandit,
	})
	slog.Debug("rl: memory event recorded", "type", eventType, "id", factID, "reward", reward)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestMemoryRLHandler ./internal/rl/ -v`
Expected: All 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rl/memory_handler.go internal/rl/memory_handler_test.go
git commit -m "feat(rl): add MemoryRLHandler to bridge memory lifecycle to RL

Converts memory ADD/UPDATE/DELETE/Conflict events into bandit-level
experiences. Configurable reward magnitudes with sensible defaults.
Decoupled from Trainer via ExperienceAdder interface for testability."
```

---

### Task 5: Wire Memory-RL Bridge in Gateway

**Files:**
- Modify: `internal/gateway/init_cognitive.go`

- [ ] **Step 1: Wire MemoryRLHandler after RL system init**

In `internal/gateway/init_cognitive.go`, inside the `if gw.cfg.Agent.RL.Enabled` block, after `slog.Info("RL system initialized")` (line 40), add:

```go
		// Bridge memory lifecycle events to RL system
		if gw.lifecycleMgr != nil {
			memoryRewards := rl.DefaultMemoryRLRewards()
			memRLHandler := rl.NewMemoryRLHandler(gw.rlTrainer, memoryRewards)
			gw.lifecycleMgr.SetRLEventHandler(memRLHandler)
			slog.Info("RL-memory bridge connected")
		}
```

- [ ] **Step 2: Run build to verify no compile errors**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 3: Run all agent and memory tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ ./internal/memory/ ./internal/rl/ -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add internal/gateway/init_cognitive.go
git commit -m "feat(gateway): wire memory-RL bridge in cognitive init

Connects MemoryRLHandler to LifecycleManager when RL is enabled,
completing the memory→RL feedback loop."
```

---

### Task 6: Reflection Bonus in Episode Reward

**Files:**
- Modify: `internal/agent/rl_helpers.go`
- Modify: `internal/agent/rl_helpers_test.go`
- Modify: `internal/agent/cognitive.go:483-495`

- [ ] **Step 1: Write failing test for computeReflectionBonus**

Add to `internal/agent/rl_helpers_test.go`:

```go
func TestComputeReflectionBonus(t *testing.T) {
	tests := []struct {
		name       string
		reflection *Reflection
		want       float64
	}{
		{
			name:       "nil reflection",
			reflection: nil,
			want:       0.0,
		},
		{
			name: "lessons learned gives bonus",
			reflection: &Reflection{
				LessonsLearned: []string{"avoid timeout with large files"},
			},
			want: 0.15,
		},
		{
			name: "suggested adjustment gives small bonus",
			reflection: &Reflection{
				SuggestedAdjustment: "try streaming approach",
			},
			want: 0.05,
		},
		{
			name: "all bonuses combined",
			reflection: &Reflection{
				LessonsLearned:      []string{"lesson 1", "lesson 2"},
				SuggestedAdjustment: "try different approach",
			},
			want: 0.20, // 0.15 + 0.05
		},
		{
			name:       "empty reflection gives no bonus",
			reflection: &Reflection{},
			want:       0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeReflectionBonus(tc.reflection)
			diff := got - tc.want
			if diff < -0.001 || diff > 0.001 {
				t.Errorf("got %f, want %f", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestComputeReflectionBonus ./internal/agent/ -v`
Expected: FAIL — `computeReflectionBonus` undefined

- [ ] **Step 3: Implement computeReflectionBonus**

Add to `internal/agent/rl_helpers.go`:

```go
// computeReflectionBonus returns an additional reward based on the richness
// of the reflection output. Richer reflections (with lessons, adjustments)
// indicate better self-awareness, which deserves positive reinforcement.
func computeReflectionBonus(reflection *Reflection) float64 {
	if reflection == nil {
		return 0.0
	}
	bonus := 0.0
	if len(reflection.LessonsLearned) > 0 {
		bonus += 0.15
	}
	if reflection.SuggestedAdjustment != "" {
		bonus += 0.05
	}
	return bonus
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestComputeReflectionBonus ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Integrate into recordRLEpisode**

In `internal/agent/cognitive.go`, in the `recordRLEpisode` method, change line 494:

**Before:**
```go
	episodeReward := computeSimpleEpisodeReward(reflection, obsResult)
```

**After:**
```go
	episodeReward := computeSimpleEpisodeReward(reflection, obsResult)
	episodeReward += computeReflectionBonus(reflection)
```

- [ ] **Step 6: Run all agent tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/rl_helpers.go internal/agent/rl_helpers_test.go internal/agent/cognitive.go
git commit -m "feat(rl): add reflection bonus to episode reward

Reflection outputs with LessonsLearned (+0.15) and SuggestedAdjustment
(+0.05) now contribute reward shaping bonuses. Richer self-reflection
is positively reinforced."
```

---

### Task 7: FeedbackSender Channel Interface

**Files:**
- Modify: `internal/channel/channel.go`
- Modify: `internal/agent/cognitive_types.go`

- [ ] **Step 1: Add FeedbackSender interface to channel package**

In `internal/channel/channel.go`, add after `NotificationSender`:

```go
// FeedbackSender is an optional interface for channels that support
// collecting user satisfaction feedback (e.g., 👍/👎 after a response).
// The call blocks until the user responds or a timeout is reached.
// Returns a value in [-1, 1]: -1 (negative), 0 (neutral/timeout), 1 (positive).
// Channels that do not implement this interface yield 0 (neutral).
type FeedbackSender interface {
	SendFeedbackRequest(ctx context.Context, target MessageTarget) (float64, error)
}
```

- [ ] **Step 2: Add FeedbackCollector to cognitive_types.go**

In `internal/agent/cognitive_types.go`, add after the `RLTrainer` interface:

```go
// FeedbackCollector collects user satisfaction feedback from the channel.
// Returns feedback in [-1, 1]: negative, neutral, or positive.
type FeedbackCollector func(ctx context.Context, ch channel.Channel, target channel.MessageTarget) float64
```

- [ ] **Step 3: Verify build**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
git add internal/channel/channel.go internal/agent/cognitive_types.go
git commit -m "feat(channel): add FeedbackSender interface for user satisfaction

New optional channel interface following ApprovalSender/ReflectionSender
pattern. Returns [-1, 1] satisfaction score. Channels that don't implement
it yield neutral (0)."
```

---

### Task 8: Telegram FeedbackSender Implementation

**Files:**
- Modify: `internal/channel/telegram/adapter.go`

- [ ] **Step 1: Add pending feedback sync.Map and implement SendFeedbackRequest**

In `internal/channel/telegram/adapter.go`, first check if the Adapter struct has a `pendingFeedback` field. If not, add one similar to `pendingApprovals`. Then add the implementation after the `ReflectionSender` section:

```go
// ---------- channel.FeedbackSender ----------

// SendFeedbackRequest sends a Telegram inline keyboard with 👍/👎 buttons
// and blocks until the user responds or timeout expires.
func (a *Adapter) SendFeedbackRequest(ctx context.Context, target channel.MessageTarget) (float64, error) {
	chatID, err := strconv.ParseInt(target.ChannelID, 10, 64)
	if err != nil || chatID == 0 {
		return 0, nil // neutral fallback
	}

	key := fmt.Sprintf("feedback_%s_%d", target.ChannelID, time.Now().UnixNano())

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👍", "feedback_pos:"+key),
			tgbotapi.NewInlineKeyboardButtonData("👎", "feedback_neg:"+key),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "Was this response helpful?")
	msg.ReplyMarkup = keyboard

	if _, err := a.bot.Send(msg); err != nil {
		slog.Warn("telegram: failed to send feedback request", "err", err)
		return 0, nil
	}

	resultCh := make(chan float64, 1)
	a.pendingFeedback.Store(key, resultCh)
	defer a.pendingFeedback.Delete(key)

	timeout := time.Duration(a.approvalTimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	select {
	case feedback := <-resultCh:
		return feedback, nil
	case <-time.After(timeout):
		return 0, nil // neutral on timeout
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
```

Also add `pendingFeedback sync.Map` to the Adapter struct if not present, and add callback dispatch handling in the existing callback handler (where `pendingApprovals` and `pendingReflections` are dispatched) to handle `feedback_pos:` and `feedback_neg:` prefixes:

```go
	// In the callback query handler:
	if strings.HasPrefix(data, "feedback_pos:") {
		key := strings.TrimPrefix(data, "feedback_pos:")
		if ch, ok := a.pendingFeedback.Load(key); ok {
			ch.(chan float64) <- 1.0
		}
	} else if strings.HasPrefix(data, "feedback_neg:") {
		key := strings.TrimPrefix(data, "feedback_neg:")
		if ch, ok := a.pendingFeedback.Load(key); ok {
			ch.(chan float64) <- -1.0
		}
	}
```

- [ ] **Step 2: Verify build**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add internal/channel/telegram/adapter.go
git commit -m "feat(telegram): implement FeedbackSender for user satisfaction

Sends 👍/👎 inline keyboard after responses. Positive→+1.0,
Negative→-1.0, Timeout→0.0 (neutral). Follows existing
ApprovalSender/ReflectionSender callback dispatch pattern."
```

---

### Task 9: TUI FeedbackSender Implementation

**Files:**
- Modify: `internal/channel/tui/adapter.go`

- [ ] **Step 1: Implement SendFeedbackRequest for TUI**

In `internal/channel/tui/adapter.go`, add after the `ReflectionSender` section:

```go
// ---------- channel.FeedbackSender ----------

func (a *Adapter) SendFeedbackRequest(ctx context.Context, target channel.MessageTarget) (float64, error) {
	if a.program == nil {
		return 0, nil
	}

	resultCh := make(chan float64, 1)
	a.program.Send(feedbackRequestMsg{
		resultCh: resultCh,
	})

	select {
	case feedback := <-resultCh:
		return feedback, nil
	case <-time.After(a.approvalTimeout):
		slog.Info("tui: feedback timed out, defaulting to neutral")
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
```

Also add the message type:

```go
type feedbackRequestMsg struct {
	resultCh chan float64
}
```

Handle it in the TUI model's `Update` method where `approvalRequestMsg` and `reflectionRequestMsg` are handled:

```go
	case feedbackRequestMsg:
		// Display simple yes/no prompt, send result to channel
		// y → +1.0, n → -1.0
```

(The exact Bubble Tea integration depends on the existing model structure — follow the pattern used for `approvalRequestMsg`.)

- [ ] **Step 2: Verify build**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add internal/channel/tui/adapter.go
git commit -m "feat(tui): implement FeedbackSender for user satisfaction

Shows y/n prompt after responses. y→+1.0, n→-1.0, timeout→0.0.
Follows existing approvalRequestMsg/reflectionRequestMsg pattern."
```

---

### Task 10: Collect Feedback in Cognitive Loop

**Files:**
- Modify: `internal/agent/cognitive.go`

- [ ] **Step 1: Add feedback collection after streaming the final answer**

In `internal/agent/cognitive.go`, in the `HandleMessage` method, modify the `persist` label section (around line 352-356). Add feedback collection between the final answer streaming and the RL episode recording:

**Before:**
```go
persist:
	// RL: record PPO/DQN experiences and episode
	if rlEnabled && episodeCollector != nil && ca.rlTrainer != nil {
		ca.recordRLEpisode(state, plan, obsResult, reflection, ppoStrategy, episodeCollector)
	}
```

**After:**
```go
persist:
	// RL: collect user feedback and record episode
	var userFeedback float64
	if rlEnabled && episodeCollector != nil && ca.rlTrainer != nil {
		// Attempt to collect user feedback from channel
		if sender, ok := ch.(channel.FeedbackSender); ok {
			fb, err := sender.SendFeedbackRequest(ctx, target)
			if err != nil {
				slog.Debug("cognitive: feedback collection failed", "err", err)
			} else {
				userFeedback = fb
			}
		}
		ca.recordRLEpisode(state, plan, obsResult, reflection, ppoStrategy, episodeCollector, userFeedback)
	}
```

- [ ] **Step 2: Update recordRLEpisode signature to accept userFeedback**

Change the `recordRLEpisode` method signature and pass feedback to `EpisodeParams`:

**Before (line 485-492):**
```go
func (ca *CognitiveAgent) recordRLEpisode(
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	ppoStrategy *rl.PlanStrategyAction,
	collector *EpisodeCollector,
) {
```

**After:**
```go
func (ca *CognitiveAgent) recordRLEpisode(
	state *CognitiveState,
	plan *TaskPlan,
	obsResult *ObservationResult,
	reflection *Reflection,
	ppoStrategy *rl.PlanStrategyAction,
	collector *EpisodeCollector,
	userFeedback float64,
) {
```

And in the `EpisodeParams` construction inside the goroutine, change `UserFeedback` from being omitted (zero) to:

**Before:**
```go
		if err := ca.rlTrainer.RecordEpisode(bgCtx, rl.EpisodeParams{
			...
			Experiences:   experiences,
		}); err != nil {
```

**After (add UserFeedback field):**
```go
		if err := ca.rlTrainer.RecordEpisode(bgCtx, rl.EpisodeParams{
			...
			UserFeedback:  userFeedback,
			Experiences:   experiences,
		}); err != nil {
```

- [ ] **Step 3: Run all tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ ./internal/memory/ ./internal/rl/ -v`
Expected: All PASS

- [ ] **Step 4: Verify full build**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add internal/agent/cognitive.go
git commit -m "feat(rl): collect user feedback and pass to episode recording

After streaming the final answer, attempts to collect 👍/👎 feedback
via FeedbackSender. Feedback flows into EpisodeParams.UserFeedback,
which weights the UserSatisfaction reward component."
```

---

### Task 11: Integration Test and Final Verification

**Files:**
- All modified files

- [ ] **Step 1: Run full test suite**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./... -v 2>&1 | tail -40`
Expected: All packages PASS

- [ ] **Step 2: Run build**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds with no warnings

- [ ] **Step 3: Run linter**

Run: `make lint`
Expected: No new lint errors

- [ ] **Step 4: Verify no unintended changes**

Run: `git diff --stat main`
Expected: Only the files listed in the File Map are modified

- [ ] **Step 5: Final commit (if any fixups needed)**

```bash
git add -A
git commit -m "chore: final lint and formatting fixes for RL integration"
```
