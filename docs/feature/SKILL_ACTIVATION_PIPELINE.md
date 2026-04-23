# Skill Activation Pipeline — Safety Gates, Progressive Loading & Auto-Promotion

## Overview

Transforms the skill synthesis system from generating static drafts into a closed-loop pipeline where skills are automatically validated, promoted, and activated. Skills now progress through 7 safety gates before becoming available, and are loaded progressively (3 levels) to minimize context cost.

## Problem

The previous `Synthesizer` generated skill drafts to `~/.IronClaw/skills/SKILL_*.md` but they remained inert — no validation, no activation, no integration into the cognitive loop. Users had to manually review and enable skills.

## Architecture

### Closed-Loop Pipeline

```
Task Execution → Trajectory Recording → Pattern Detection
    → Skill Draft Generation → 7 Safety Gates
    → Pass: Move to activeDir (status: active)
    → Fail: Remain in draftsDir (logged with reason)
    
Active Skills → 3-Level Progressive Loading → PERCEIVE injection
```

### 7 Safety Gates (`internal/evolution/safety_gates.go`)

Every draft must pass all 7 gates in order:

| # | Gate | Condition | Rationale |
|---|------|-----------|-----------|
| 1 | `FrequencyGate` | OccurrenceCount >= 5 | Pattern must be recurring, not one-off |
| 2 | `RewardGate` | AvgReward >= 0.7 | Pattern must correlate with successful outcomes |
| 3 | `DestructiveToolGate` | No destructive tools | Blocks rm, drop, force-push, reset --hard, delete, truncate, destroy |
| 4 | `UserConsentGate` | !UserRejected | Respects explicit user rejection of the pattern |
| 5 | `SemanticGate` | Description > 20 chars | Ensures meaningful description (prevents gibberish skills) |
| 6 | `SandboxTestGate` | (placeholder — always passes) | TODO: sandbox simulation execution |
| 7 | `ConflictGate` | No conflicting skills | Prevents duplicate/overlapping active skills |

```go
// Usage
allPassed, failedGate, reason := RunGates(draft, DefaultGates())
```

Gates are composable — `DefaultGates()` returns the standard set, but custom gate lists can be passed to `SkillActivator`.

### 3-Level Progressive Loading (`internal/evolution/skill_loader.go`)

| Level | What's Loaded | When | Context Cost |
|-------|--------------|------|-------------|
| **Level 1: Index** | Name + description + trigger keywords | Every PERCEIVE phase | ~10 tokens/skill |
| **Level 2: Partial** | Index + first section (key steps) | Task matches keywords | ~100 tokens/skill |
| **Level 3: Full** | Complete skill content | Confirmed execution | Full content |

**Keyword Matching**: `MatchSkills(goal, complexity)` scans all skill indexes and returns those whose keywords appear in the goal text. Optional complexity filter restricts to matching complexity level.

**File Format**: Skills are Markdown files with YAML frontmatter:
```yaml
---
name: git-pr-workflow
description: Standard PR creation workflow
status: active
trigger_keywords: [pull request, PR, merge request]
trigger_complexity: moderate
---
## Steps
1. Create branch from main...
```

### Skill Activator (`internal/evolution/skill_activator.go`)

Manages the draft → active lifecycle:

- `PromoteDraft(draft)` — Runs all safety gates; returns (promoted, failedGate, reason)
- `ScanAndPromote()` — Scans draftsDir for eligible `.md` files, parses frontmatter into `SkillDraft`, runs gates, moves passing drafts to activeDir with `status: active`

### Synthesizer Integration

`Synthesizer.OnEpisodeComplete` now calls `activator.PromoteDraft()` after generating a draft. If the activator is nil (not configured), behavior is unchanged (draft-only mode).

## Files

| File | Lines | Description |
|---|---|---|
| `internal/evolution/safety_gates.go` | 183 | 7 safety gates + SkillDraft + RunGates + DefaultGates |
| `internal/evolution/skill_loader.go` | 227 | 3-level progressive loading + keyword matching |
| `internal/evolution/skill_activator.go` | 153 | Draft promotion lifecycle + ScanAndPromote |
| `internal/evolution/synthesizer.go` | +26 | Activator integration into episode handling |
| `internal/evolution/safety_gates_test.go` | 248 | 25 tests covering every gate |
| `internal/evolution/skill_loader_test.go` | 245 | 14 tests for loading + matching |
| `internal/evolution/skill_activator_test.go` | 263 | 10 tests for promotion + scanning |

## Testing

```bash
go test ./internal/evolution/...
# 44 new tests + all existing tests pass
```
