# IronClaw Self-Evolution Engine Design

**Date:** 2026-04-10
**Status:** Approved
**Approach:** Plan C — Incremental Enhancement (3 phases)

## Overview

Add a self-evolution capability to IronClaw so the agent gets smarter with use. Three feedback loops, implemented incrementally, each building on existing infrastructure.

**Constraints:**
- Single-user local deployment, privacy-first
- RL system not yet active — design must not depend on it
- Lightweight LLM calls allowed for reflection/summarization
- Event-driven, non-blocking — never slow down user responses

## Architecture

New package `internal/evolution` acts as an orchestration layer. It listens to events from existing modules (REFLECT phase, episode recordings, memory lifecycle) and drives three loops.

```
Evolution Engine (internal/evolution)
├── PreferenceLearner   (Loop 1 — Phase 1)
├── SkillSynthesizer    (Loop 2 — Phase 1)
├── StrategyOptimizer   (Loop 3 — Phase 1)
├── TrajectoryRecorder  (Phase 2)
├── InsightsEngine      (Phase 2)
└── ModelRouter         (Phase 3)
```

All loops run as background goroutines, triggered by events from existing hooks.

## Phase 1: Three Feedback Loops (2 weeks)

### Loop 1: Preference Learner

**Goal:** Remember user coding style, tool preferences, working habits.

**Trigger:** After every REFLECT phase completes (via `agent_hooks.go` PostReflect hook).

**Mechanism:**
1. Intercept the `Reflection` struct after `Reflector.Run()` returns
2. Extract preference signals: which tools the user approved/denied, replan decisions, lesson patterns
3. Use lightweight LLM to classify into preference categories (coding_style, tool_preference, communication_style, domain_expertise)
4. Store as preference memories with scope=user, metadata type=preference
5. On next PERCEIVE phase, inject top-k relevant preferences into system prompt context

**New types:**
```go
// internal/evolution/preference.go
type PreferenceLearner struct {
    memStore   memory.Store
    completer  memory.Completer
    cfg        EvolutionConfig
}

type PreferenceEntry struct {
    Category   string    // coding_style, tool_preference, etc.
    Key        string    // e.g., "prefers_verbose_output"
    Value      string    // e.g., "true"
    Confidence float64   // 0-1, increases with repeated observation
    ObservedAt time.Time
    Count      int       // number of times observed
}
```

**Integration point:** New hook in `agent_hooks.go`:
```go
type EvolutionHook interface {
    OnReflectionComplete(ctx context.Context, state *CognitiveState, reflection *Reflection)
    OnEpisodeComplete(ctx context.Context, params rl.EpisodeParams, reward float64)
    OnToolExecuted(ctx context.Context, toolName string, succeeded bool, durationMs int64)
}
```

### Loop 2: Skill Synthesizer

**Goal:** Detect repeated tool-use patterns and auto-generate SKILL.md drafts.

**Trigger:** After every episode recording (via `rl.Trainer.RecordEpisode` callback).

**Mechanism:**
1. Maintain a rolling window of the last 50 episodes' tool sequences
2. Run pattern detection: find tool combinations used successfully 3+ times
3. When a pattern is detected, use LLM to generate a SKILL.md draft
4. Write to `~/.IronClaw/skills/drafts/{pattern-name}.md` with `status: draft` in frontmatter
5. On next session start, notify user: "I noticed you frequently do X. I drafted a skill for it."

**New types:**
```go
// internal/evolution/synthesizer.go
type SkillSynthesizer struct {
    skillMgr   *skill.Manager
    memStore   memory.Store
    completer  memory.Completer
    cfg        EvolutionConfig
    patterns   *PatternTracker
}

type ToolPattern struct {
    Tools      []string  // ordered tool sequence
    AvgReward  float64
    Count      int
    FirstSeen  time.Time
    LastSeen   time.Time
    GoalTypes  []string  // what kinds of goals triggered this pattern
}
```

**Pattern detection algorithm:**
- Sliding window over tool sequences per episode
- Normalize sequences (ignore order for parallel tools)
- Group by Jaccard similarity > 0.7
- Promote to "candidate skill" when count >= 3 and avg_reward > 0.5

### Loop 3: Strategy Optimizer

**Goal:** Tune cognitive agent parameters based on success/failure statistics.

**Trigger:** Periodically (every 10 episodes) via background ticker.

**Mechanism:**
1. Query episode storage for recent performance metrics
2. Compute statistics: success rate by complexity, replan effectiveness, tool failure rate
3. Adjust cognitive config parameters within safe bounds:
   - `replan_confidence_threshold`: if replans frequently succeed, lower threshold (replan earlier)
   - `max_subtasks`: if complex tasks fail, reduce parallelism
   - Tool priority scores: boost tools with high success rate for similar goals
4. Write adjustments to `~/.IronClaw/evolution/strategy.yaml`
5. Gateway reloads on next session start

**Adjustable parameters (with bounds):**
```yaml
# ~/.IronClaw/evolution/strategy.yaml
replan_confidence_threshold:
  value: 0.6
  min: 0.3
  max: 0.9
  adjusted_at: "2026-04-10T10:00:00Z"
  reason: "Replans succeeded 80% of the time, lowering threshold"

tool_priorities:
  bash: 1.2      # boosted: 95% success rate
  http: 0.8      # reduced: 60% success rate on recent tasks
```

**Safety rails:**
- Maximum adjustment per cycle: 10% of current value
- Revert if success rate drops 15%+ after adjustment
- All changes logged with reason for auditability

## Phase 2: Trajectory + Insights (2-4 weeks)

### Trajectory Recorder

**Goal:** Record structured interaction histories for analysis and future RL training.

**Mechanism:**
1. Hook into the cognitive loop start/end
2. Record: goal, plan, tool calls with args/results, reflection outcome, user feedback
3. Store as JSONL files in `~/.IronClaw/evolution/trajectories/`
4. Compress old trajectories (>7 days) using lightweight LLM summarization
5. Export capability for RL training data (ShareGPT format)

**Storage format:**
```jsonl
{"session_id":"...","goal":"...","complexity":"medium","tools":[{"name":"bash","args":"...","result":"...","succeeded":true}],"reflection":{"confidence":0.8,"succeeded":true},"user_feedback":1.0,"timestamp":"..."}
```

### Insights Engine

**Goal:** Analyze historical data and surface actionable optimization opportunities.

**Mechanism:**
1. Periodic analysis (daily or on-demand via `ironclaw insights` CLI)
2. Dimensions: task success rate by type, tool effectiveness, common failure patterns, preference drift
3. Output: Markdown report at `~/.IronClaw/evolution/insights/YYYY-MM-DD.md`
4. Feed insights back to Strategy Optimizer for data-driven adjustments

## Phase 3: Model Routing + RL Activation (1-2 months)

### Smart Model Router

**Goal:** Route simple tasks to cheap models, complex tasks to strong models.

**Mechanism:**
1. In PERCEIVE phase, classify task complexity (already exists as `Goal.Complexity`)
2. Map complexity to model: simple→cheap, medium→default, complex→strong
3. Track cost and success per route for optimization
4. Configuration in `ironclaw.yaml` under `evolution.model_routing`

### RL Activation

**Goal:** Activate existing PPO/DQN trainer with real data from trajectory recorder.

**Mechanism:**
1. Convert trajectory records to RL experiences
2. Feed into existing `rl.Trainer.AddExperience()`
3. Enable background training loop
4. A/B test: run 10% of sessions with RL policy, compare metrics

## Configuration

```yaml
# In ironclaw.yaml
evolution:
  enabled: true
  
  preference_learner:
    enabled: true
    max_preferences: 100
    min_confidence: 0.3
    llm_model: ""  # empty = use reflect_model from cognitive config
  
  skill_synthesizer:
    enabled: true
    pattern_threshold: 3        # min occurrences to trigger
    reward_threshold: 0.5       # min avg reward
    drafts_dir: "skills/drafts"
    auto_notify: true
  
  strategy_optimizer:
    enabled: true
    update_interval: 10         # every N episodes
    max_adjustment_percent: 10
    revert_threshold: 0.15      # revert if success drops 15%
  
  trajectory:
    enabled: false              # Phase 2
    storage_dir: "evolution/trajectories"
    compress_after_days: 7
  
  insights:
    enabled: false              # Phase 2
    schedule: "daily"
  
  model_routing:
    enabled: false              # Phase 3
    cheap_model: ""
    strong_model: ""
```

## File Structure

```
internal/evolution/
├── engine.go           # EvolutionEngine — lifecycle, event routing
├── config.go           # EvolutionConfig struct
├── preference.go       # PreferenceLearner (Loop 1)
├── synthesizer.go      # SkillSynthesizer (Loop 2)
├── pattern.go          # PatternTracker — tool sequence analysis
├── optimizer.go        # StrategyOptimizer (Loop 3)
├── trajectory.go       # TrajectoryRecorder (Phase 2)
├── insights.go         # InsightsEngine (Phase 2)
├── router.go           # ModelRouter (Phase 3)
└── evolution_test.go   # Tests
```

## Integration Points (existing code changes)

1. **`internal/gateway/gateway.go`** — Initialize EvolutionEngine after cognitive agent, wire hooks
2. **`internal/agent/agent_hooks.go`** — Add `EvolutionHook` interface to hook chain
3. **`internal/agent/reflect.go`** — Call evolution hook after `saveExperience()`
4. **`internal/config/config.go`** — Add `EvolutionConfig` struct
5. **`internal/skill/manager.go`** — Add `LoadDrafts()` method for synthesized skills
6. **`cmd/ironclaw/main.go`** — Add `insights` subcommand (Phase 2)

## Testing Strategy

- Unit tests for each loop in isolation (mock memory store, mock completer)
- Integration test: simulate 10 episodes, verify preference extraction and pattern detection
- Strategy optimizer: test bounds enforcement and revert logic
- No flaky tests: all LLM calls mocked in tests

## Success Metrics

| Metric | Baseline | Phase 1 Target |
|--------|----------|----------------|
| Preference recall accuracy | 0% (no preferences) | >70% on repeated interactions |
| Pattern detection precision | N/A | >80% (detected patterns are real) |
| Replan decision quality | Current default thresholds | 10%+ improvement in task success |
| User intervention rate | Current baseline | 15% reduction |
