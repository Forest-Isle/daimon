# REFLECT Phase - Quick Reference Guide

## What is REFLECT?

The 5th phase in the cognitive loop: **PERCEIVE → PLAN → ACT → OBSERVE → REFLECT**

A post-execution evaluation step that:
- Calls LLM with goal + observations to generate JSON score
- Determines if replanning is needed based on confidence threshold
- Saves experience to memory.md
- Returns final answer or triggers replan loop

---

## Core Files

| File | Purpose |
|------|---------|
| `internal/agent/reflect.go` | Reflector implementation + parsing |
| `internal/agent/cognitive_prompts.go` | System and user message templates |
| `internal/agent/cognitive_types.go` | Reflection and related data structures |
| `internal/agent/cognitive.go` | Main loop + replan decision logic |
| `internal/agent/observe.go` | Observation aggregation |
| `internal/agent/rl_helpers.go` | RL reward + state updates |
| `internal/config/config.go` | Configuration schema |

---

## The LLM Call

```
System: "You are a reflective agent that evaluates task outcomes..."
User: "ORIGINAL GOAL: {goal}
       PLAN SUMMARY: {summary}
       EXECUTION OBSERVATIONS: {observations}
       STATISTICS: {stats}"
       
Max Tokens: 1024 (default, configurable)
```

**Returns JSON:**
```json
{
  "overall_confidence": 0.85,
  "succeeded": true,
  "lessons_learned": ["..."],
  "suggested_adjustment": "...",
  "final_answer": "...",
  "needs_replan": false,
  "replan_reason": ""
}
```

---

## JSON Parsing (3 Fallbacks)

1. **Direct Parse**: `json.Unmarshal(raw)`
2. **Markdown Code Block**: Extract from ` ```json {...}``` `
3. **First JSON Object**: Extract first `{...}` block

**If all fail**: Use defaults
- `overall_confidence = 0.5`
- `succeeded = hasAnySucess`
- `final_answer = "Task completed with partial results."`

---

## Replan Trigger Logic

```go
// Both conditions must be TRUE
if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
    // Ask user for decision
}
```

**Thresholds:**
- `confidenceThreshold`: Default **0.6** (60%)
  - Config: `agent.cognitive.confidence_threshold`
  - If ≤ 0: defaults to 0.6
  
- `maxReplans`: Default **2** (3 total attempts)
  - Config: `agent.cognitive.max_replan_attempts`
  - If ≤ 0: defaults to 2

---

## User Approval Options

When confidence < threshold AND needs_replan:

```
┌─ ReplanAbort ────────────────────────────────────────┐
│ Stop immediately, return current final_answer      │
└───────────────────────────────────────────────────────┘

┌─ ReplanContinue ──────────────────────────────────────┐
│ Accept current result, skip replanning              │
└───────────────────────────────────────────────────────┘

┌─ ReplanAdjust ────────────────────────────────────────┐
│ Adjust user message:                                 │
│ new_msg = suggested_adjustment + "\nOriginal: " + msg
│ Loop back to PLAN phase                             │
└───────────────────────────────────────────────────────┘
```

If channel doesn't support interactive prompt: **Auto-continue**

---

## What Goes Into the LLM Prompt

| Field | Source | Example |
|-------|--------|---------|
| GOAL | state.Goal.Raw | "Find documentation for library X" |
| PLAN_SUMMARY | plan.Summary | "1. Search web, 2. Read docs, 3. Return summary" |
| OBSERVATIONS | From all tool executions | "- t1 [web_search]: SUCCESS\n  Output: Found X docs..." |
| STATS | Computed by OBSERVE phase | "Success: 2, Failures: 0, Denied: 0, Progress: 100%, Error patterns: none" |
| PERSONALITY | From Soul.md (if configured) | "Be concise and technical" |
| PERSISTENT_RULES | From Memory.md (if configured) | "Always cite sources" |

---

## Observation Statistics (OBSERVE Phase)

Computed from all tool execution results:

```
SuccessCount = number of successful tool executions
FailureCount = number of failed tool executions
DeniedCount = number of denied executions

OverallProgress = SuccessCount / (TotalSubtasks - SkippedCount)
                [Range: 0.0 to 1.0]

ErrorPatterns = ["all_denied", "permission_error", "network_error", "tool_not_found"]
                (can have multiple patterns)
```

---

## Confidence Score Grounding

**What 0.6 means:**
- Core objectives were met
- Some concerns or ambiguity about completeness
- Not confident enough to skip replanning if needs_replan=true

**What 0.85 means:**
- Strong success
- Confident in outcome quality
- Replan unlikely needed

**What 0.3 means:**
- Significant gaps in outcome
- Major uncertainties
- Replan likely recommended if needs_replan=true

---

## Configuration Examples

### Require higher confidence (less replanning)
```yaml
agent:
  cognitive:
    confidence_threshold: 0.75  # was 0.6
    max_replan_attempts: 1      # was 2
```

### Allow more reflection (more replanning)
```yaml
agent:
  cognitive:
    confidence_threshold: 0.5   # was 0.6
    max_replan_attempts: 3      # was 2
    reflect_max_tokens: 2048    # was 1024
```

### Use different model for reflection
```yaml
agent:
  cognitive:
    reflect_model: "claude-opus-4-20250514"  # instead of default
    plan_model: "claude-sonnet-4-20250514"   # instead of default
```

---

## RL Integration

If RL policy is enabled:

**Before ACT:**
```
PPO Policy adjusts plan.OverallConfidence
confidence_adj = SelectPlanStrategy(rlState).ConfidenceAdj
plan.OverallConfidence += confidence_adj
```

**After REFLECT:**
```
rlState.ReflectionConfidence = reflection.OverallConfidence
if reflection.NeedsReplan:
    dqnAction = SelectReplanAction(rlState)  // DQN suggests action
```

**Episode Reward:**
```
reward = 0
if succeeded: reward += 1.0  else: reward -= 1.0
reward += OverallProgress * 0.5

Range: -1.5 to +1.5
```

---

## Memory Operations

After REFLECT completes (async, background):

**Path 1: Fact Extraction**
- Extract facts from: goal + final_answer
- Apply lifecycle: ADD/UPDATE/DELETE/NOOP
- Send summary notification

**Path 2: Raw Experience**
- Build entry with GOAL + PLAN + OUTCOME + LESSONS
- Save to memories table
- Extract graph entities

---

## Common Issues & Solutions

| Issue | Cause | Fix |
|-------|-------|-----|
| JSON parse fails | LLM returns prose | Uses 0.5 fallback; check system prompt |
| Never replans | confidence >= threshold | Lower threshold or check needs_replan logic |
| Too many replans | threshold too low | Increase to 0.7+; reduce max_replan_attempts |
| No personality applied | Soul.md missing | Create Soul.md with persona, restart |
| Incorrect progress % | Skipped subtasks | Check observe.go logic; verify skip status |

---

## Debug Logging

Look for these log messages:

```
reflect complete (confidence=0.85, succeeded=true, needs_replan=false)
cognitive: replanning (attempt=1, max=2, session=xxx)
cognitive: DQN replan suggestion (action=2)
cognitive: replan skipped (continue)
cognitive: adjusting and replanning
reflect: parse failed, using fallback (err=...)
reflect: fact extraction failed (err=...)
```

---

## Data Flow Summary

```
┌─────────────────────────────────┐
│ OBSERVE Phase                   │
│ Computes:                       │
│ - Success/Failure/Denial counts │
│ - Overall Progress (0-1)        │
│ - Error Patterns               │
└────────┬────────────────────────┘
         │
         v
┌─────────────────────────────────┐
│ REFLECT Phase                   │
│ Sends to LLM:                   │
│ - Goal, Plan, Observations      │
│ - Statistics (from OBSERVE)     │
│ - Personality, Rules            │
└────────┬────────────────────────┘
         │
         v
┌─────────────────────────────────┐
│ LLM Returns JSON:               │
│ - overall_confidence            │
│ - succeeded                     │
│ - needs_replan                  │
│ - suggested_adjustment          │
│ - final_answer                  │
│ - lessons_learned              │
└────────┬────────────────────────┘
         │
         v
┌─────────────────────────────────┐
│ Decision Logic                  │
│ Check: confidence < 0.6 &&      │
│        needs_replan == true?    │
└────────┬────────────────────────┘
         │
    ┌────┴────────────────────────┐
    │                             │
   YES                           NO
    │                             │
    v                             v
Ask User              Persist & Return
(Abort/Continue/      Final Answer
 Adjust)
```

---

## Key Takeaways

1. **Two-dimensional scoring**: `succeeded` (boolean) + `confidence` (0-1)
2. **LLM-driven**: Score comes from LLM evaluation, not hardcoded logic
3. **Conditional replanning**: Only triggered if BOTH conditions met
4. **Graceful fallback**: 50% default confidence if parsing fails
5. **Async memory**: Experience saved in background, doesn't block
6. **RL-aware**: Can integrate with PPO/DQN policies
7. **Customizable**: Threshold, max attempts, max tokens all configurable

