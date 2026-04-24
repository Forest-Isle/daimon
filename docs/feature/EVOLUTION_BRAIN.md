# Evolution Brain — Unified Self-Evolution System

## Overview

Unifies the three independent evolution loops (Preference Learner, Strategy Optimizer, Skill Synthesizer) into a single coordinated `Brain` with cross-loop feedback, preference decay, and trajectory cleanup.

## Problem

The three loops operated in isolation:
- **PreferenceLearner** tracked tool/complexity preferences but never decayed old entries
- **StrategyOptimizer** tuned replan thresholds but didn't inform skill synthesis
- **SkillSynthesizer** detected patterns but didn't consider strategy priorities
- **Trajectories** accumulated indefinitely with no retention policy

## Architecture

```
                    ┌─────────────────────┐
                    │   Evolution Brain    │
                    │  (unified coordinator)│
                    └───────┬─────────────┘
                            │
            ┌───────────────┼───────────────┐
            │               │               │
     ┌──────▼──────┐ ┌─────▼──────┐ ┌─────▼──────┐
     │  Preference  │ │  Strategy  │ │   Skill    │
     │   Learner    │ │  Optimizer │ │ Synthesizer │
     └──────┬──────┘ └─────┬──────┘ └─────┬──────┘
            │               │               │
            └───── cross-feedback ──────────┘
```

### Cross-Loop Feedback

Two feedback channels connect the loops:

1. **Skill → Preference** (`skillToPreference`): When a skill is activated, its tool sequence boosts preference confidence for those tools
2. **Strategy → Skill** (`strategyToSkill`): Strategy optimizer's tool priorities inform skill synthesis about which tools are currently favored

`DrainFeedback()` processes pending cross-loop messages (called during insights cycle).

### Brain Metrics

```go
type BrainMetrics struct {
    TotalEpisodes         int64
    PreferenceUpdates     int64
    StrategyOptimizations int64
    SkillsActivated       int64
    InsightCycles         int64
    LastInsightAt         time.Time
    HealthScore           float64  // 0.0-1.0
}
```

### Preference Decay (`preference_decay.go`)

Exponential time-based decay using half-life formula:

```
confidence *= 2^(-age / halfLife)
```

- Preferences not seen within the decay window lose confidence exponentially
- Entries below 0.05 confidence are removed entirely
- Prevents stale preferences from persisting indefinitely

### Trajectory Cleanup (`trajectory_cleanup.go`)

- `CleanupTrajectories(dir, retention)` — Removes JSONL files older than retention period
- `CompactTrajectories(dir, detailDays)` — Keeps detailed data for recent days, removes older files
- Parses dates from filenames (`YYYY-MM-DD.jsonl`)

### Helper Methods (`brain_helpers.go`)

Added to PreferenceLearner and StrategyOptimizer:
- `PreferenceLearner.BoostTool(name, reward)` — Cross-loop boost from skill activation
- `StrategyOptimizer.GetToolPriorities()` — Export current priorities for skill feedback
- `StrategyOptimizer.GetReplanThreshold()` — Export current threshold
- `Synthesizer.SetToolPriorities(map)` — Accept strategy feedback

## Files

| File | Lines | Description |
|---|---|---|
| `internal/evolution/brain.go` | 154 | Unified Brain coordinator |
| `internal/evolution/brain_helpers.go` | 62 | Cross-loop getter/setter methods |
| `internal/evolution/preference_decay.go` | 40 | Exponential confidence decay |
| `internal/evolution/trajectory_cleanup.go` | 53 | Trajectory retention + cleanup |
| `internal/evolution/brain_test.go` | 153 | Brain coordination tests |
| `internal/evolution/preference_decay_test.go` | 137 | Decay formula + threshold tests |
| `internal/evolution/trajectory_cleanup_test.go` | 102 | File cleanup tests |

## Testing

```bash
go test ./internal/evolution/...
```
