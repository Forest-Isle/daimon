# REFLECT Phase Scoring Mechanism - Comprehensive Analysis

## Overview

The REFLECT phase in the IronClaw cognitive agent is a **post-execution evaluation step** that:
1. Sends an LLM prompt requesting JSON evaluation of task outcome
2. Parses the returned JSON to extract a confidence score and replan decision
3. Uses the confidence score + replan decision to determine if replanning is needed
4. Saves execution experience to memory.md for future retrieval

---

## Phase Sequence

The cognitive loop follows: **PERCEIVE → PLAN → ACT → OBSERVE → REFLECT**

After REFLECT, the agent decides whether to:
- **Continue**: Accept the outcome and return final answer to user
- **Replan**: Go back to PLAN phase with adjusted user message
- **Abort**: Stop and return current final answer

---

## LLM Prompts for Scoring

### System Prompt: `ReflectSystemPrompt`

Located in: `/internal/agent/cognitive_prompts.go`

```
You are a reflective agent that evaluates task outcomes and extracts lessons. 
Given a goal, plan, and observations, produce a JSON reflection.

IMPORTANT RULES:
1. Output ONLY valid JSON, no prose before or after.
2. "final_answer" is the user-visible response summarizing what was accomplished.
3. "overall_confidence" (0.0–1.0) reflects confidence in the outcome.
4. "succeeded" is true only if core objectives were met.
5. "lessons_learned" must be concrete and actionable (used for future retrieval).
6. "needs_replan" is true if significant failures suggest the plan should be revised.
7. "suggested_adjustment" is a revised user message for replanning (only when needs_replan=true).

OUTPUT FORMAT:
{
  "overall_confidence": 0.85,
  "succeeded": true,
  "lessons_learned": ["<specific lesson>"],
  "suggested_adjustment": "",
  "final_answer": "<user-visible summary>",
  "needs_replan": false,
  "replan_reason": ""
}
```

### User Message Template: `ReflectUserPromptTemplate`

```
ORIGINAL GOAL:
{{GOAL}}

PLAN SUMMARY:
{{PLAN_SUMMARY}}

EXECUTION OBSERVATIONS:
{{OBSERVATIONS}}

STATISTICS:
{{STATS}}

Produce the JSON reflection now.
```

---

## Prompt Construction in Code

**File**: `internal/agent/reflect.go`, function `buildReflectUserMessage()`

### Observations Section
Builds a formatted list of all tool executions:

```
For each observation:
- SubTask {id} [{tool_name}]:
  Status: [SUCCESS|FAILED|DENIED]
  Output: [truncated to 500 chars if needed]
  Error: [error message if failed]
```

**Special case**: If no observations (no tools ran), shows "(no tool executions)"

### Statistics Section
Aggregates execution results:

```
Success: {count}, Failures: {count}, Denied: {count}, 
Progress: {percentage}%, Error patterns: {comma-separated list}
```

The statistics are computed by the OBSERVE phase (see details below).

### Additional Context

If available, appends to system prompt:
- **Personality** (from Soul.md): Applied to final_answer tone
- **Persistent Rules** (from Memory.md): Rules all phases must follow

---

## Score Parsing: Three-Fallback Strategy

**File**: `internal/agent/reflect.go`, function `parseReflectResponse()`

Tries to extract JSON in this order:

1. **Direct JSON Parse**
   ```go
   json.Unmarshal([]byte(raw), &rj)
   ```
   Expects pure JSON with no surrounding text.

2. **Markdown Code Block Extraction**
   ```go
   jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
   ```
   Looks for: ` ```json { ... } ``` ` or ` ``` { ... } ``` `

3. **First JSON Object Extraction**
   ```go
   jsonObjectRe = regexp.MustCompile(`(?s)\{.*\}`)
   ```
   Extracts the first `{ ... }` block found anywhere in response.

### Fallback Behavior on Parse Failure

**File**: `internal/agent/reflect.go`, line 119-125

If all three parsing attempts fail:
```go
reflection = &Reflection{
    OverallConfidence: 0.5,          // Default: 50% confidence
    Succeeded:         obsResult.SuccessCount > 0,  // True if ANY tool succeeded
    FinalAnswer:       "Task completed with partial results.",
}
```

This is a **defensive fallback** ensuring the agent never crashes on malformed LLM output.

---

## JSON Structure and Deserialization

**File**: `internal/agent/cognitive_types.go`

### Input Type: `reflectJSON`
```go
type reflectJSON struct {
    OverallConfidence   float64  `json:"overall_confidence"`
    Succeeded           bool     `json:"succeeded"`
    LessonsLearned      []string `json:"lessons_learned"`
    SuggestedAdjustment string   `json:"suggested_adjustment"`
    FinalAnswer         string   `json:"final_answer"`
    NeedsReplan         bool     `json:"needs_replan"`
    ReplanReason        string   `json:"replan_reason"`
}
```

### Output Type: `Reflection`
Direct mapping via `reflectJSONToReflection()`:
```go
type Reflection struct {
    OverallConfidence   float64
    Succeeded           bool
    LessonsLearned      []string
    SuggestedAdjustment string
    FinalAnswer         string
    NeedsReplan         bool
    ReplanReason        string
}
```

---

## Replan Decision Logic

**File**: `internal/agent/cognitive.go`, line 330-350

Two conditions trigger replan approval request:
```go
if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
    decision, _ := reflector.RequestReplanApproval(ctx, ch, target, reflection)
```

### Thresholds

**Confidence Threshold** (line 221-224):
- Default: **0.6** (60%)
- Configured via: `config.CognitiveConfig.ConfidenceThreshold`
- If set to ≤ 0: uses 0.6
- **Both conditions must be true**:
  1. `confidence < 0.6` AND
  2. `needs_replan == true`

### Approval Decision Outcomes

**File**: `internal/agent/reflect.go`, line 142-171

User can choose via interactive prompt (if channel supports it):

```go
switch decision {
case ReplanAbort:
    // Stop and return current final answer
    goto persist
case ReplanContinue:
    // Continue without replanning (skip to persist)
    goto persist
case ReplanAdjust:
    // Adjust user message with suggested adjustment and loop
    state.UserMessage = reflection.SuggestedAdjustment + "\nOriginal: " + state.UserMessage
    continue  // next attempt in replan loop
}
```

### Max Replan Attempts

**File**: `internal/agent/cognitive.go`, line 225-228

- Default: **2 retries** (3 total attempts including initial)
- Configured via: `config.CognitiveConfig.MaxReplanAttempts`
- If set to ≤ 0: uses 2
- Constant defined: `const MaxReplanAttempts = 2`

---

## Observation Result Computation

**File**: `internal/agent/observe.go`, function `Run()`

The OBSERVE phase computes statistics that inform REFLECT scoring:

### Success/Failure/Denial Counts

For each observation:
- **Success**: No error, not denied
- **Failure**: Has error field
- **Denied**: Permission/policy denied

### Overall Progress Calculation

```go
effective := total - skippedCount  // Total subtasks minus skipped ones

if effective > 0 {
    result.OverallProgress = float64(result.SuccessCount) / float64(effective)
} else {
    result.OverallProgress = 0
}
```

**Range**: 0.0 to 1.0
- 1.0 = All executed tasks succeeded
- 0.0 = No tasks succeeded
- 0.5 = Half the tasks succeeded

### Error Pattern Detection

Classifies errors into patterns (multiple patterns can exist):
- `"all_denied"`: Every observation was denied
- `"permission_error"`: Errors mentioning permission/denied/unauthorized/forbidden
- `"network_error"`: Errors mentioning network/connection/timeout/dial
- `"tool_not_found"`: Tool referenced doesn't exist in registry

---

## RL Integration: Confidence Adjustment

**File**: `internal/agent/cognitive.go`, line 263-270

If RL is enabled, PPO policy can adjust plan confidence before ACT:

```go
if rlEnabled && rlState != nil {
    ppoStrategy := rlPolicy.SelectPlanStrategy(rlState)
    if ppoStrategy != nil {
        plan.OverallConfidence = clampRL(
            plan.OverallConfidence + ppoStrategy.ConfidenceAdj,
            0, 1)
    }
}
```

Also after REFLECT (line 321-328):
```go
if rlEnabled && rlState != nil && reflection != nil {
    rlState.ReflectionConfidence = clampRL(reflection.OverallConfidence, 0, 1)
    if reflection.NeedsReplan {
        dqnAction := rlPolicy.SelectReplanAction(rlState)
        slog.Info("cognitive: DQN replan suggestion", "action", dqnAction.String())
    }
}
```

---

## Reward Computation for RL Training

**File**: `internal/agent/rl_helpers.go`, function `computeSimpleEpisodeReward()`

Simple episode reward calculation:

```go
func computeSimpleEpisodeReward(reflection *Reflection, obs *ObservationResult) float64 {
    if reflection == nil {
        return -0.5
    }
    reward := 0.0
    if reflection.Succeeded {
        reward += 1.0      // Success bonus
    } else {
        reward -= 1.0      // Failure penalty
    }
    if obs != nil {
        reward += obs.OverallProgress * 0.5  // Progress bonus (0 to 0.5)
    }
    return reward
}
```

**Range**: -1.5 to 1.5
- **+1.5**: Succeeded + 100% progress
- **+1.0**: Succeeded + 0% progress
- **+0.5**: Failed but 100% progress (partial credit)
- **-1.0**: Failed + 0% progress
- **-1.5**: Failed + no observation results

---

## Configuration Schema

**File**: `internal/config/config.go`

### CognitiveConfig Structure
```go
type CognitiveConfig struct {
    PlanModel              string  `yaml:"plan_model"`
    ReflectModel           string  `yaml:"reflect_model"`
    ConfidenceThreshold    float64 `yaml:"confidence_threshold"`     // default 0.6
    MaxParallelTools       int     `yaml:"max_parallel_tools"`       // default 3
    MaxReplanAttempts      int     `yaml:"max_replan_attempts"`      // default 2
    PlanMaxTokens          int     `yaml:"plan_max_tokens"`          // default 2048
    ReflectMaxTokens       int     `yaml:"reflect_max_tokens"`       // default 1024
    ApprovalTimeoutSeconds int     `yaml:"approval_timeout_seconds"` // default 120
}
```

### Default Configuration
```yaml
agent:
  cognitive:
    confidence_threshold: 0.6
    max_replan_attempts: 2
    reflect_max_tokens: 1024
```

### Recommended Config Values

| Parameter | Default | Range | Notes |
|-----------|---------|-------|-------|
| `confidence_threshold` | 0.6 | 0.0-1.0 | Lower = more replanning |
| `max_replan_attempts` | 2 | 1-5 | Total attempts = max_replan_attempts + 1 |
| `reflect_max_tokens` | 1024 | 512-2048 | Controls reflection verbosity |
| `approval_timeout_seconds` | 120 | 30-600 | How long to wait for user input |

---

## Context Provided to LLM for Scoring

When REFLECT phase calls the LLM, it provides:

1. **Original Goal** (`{{GOAL}}`):
   - Raw user request
   - Parsed intent
   - Complexity classification (simple/moderate/complex)

2. **Plan Summary** (`{{PLAN_SUMMARY}}`):
   - High-level execution strategy
   - Number and type of subtasks
   - Overall plan confidence

3. **Observations** (`{{OBSERVATIONS}}`):
   - Status of each tool execution
   - Truncated output (max 500 chars per observation)
   - Error messages if failed
   - Denial indicators

4. **Statistics** (`{{STATS}}`):
   - Success count
   - Failure count  
   - Denial count
   - Overall progress percentage (0-100%)
   - Error patterns

5. **Optional Personality** (Soul.md):
   - Persona and tone guidance for final_answer

6. **Optional Persistent Rules** (Memory.md):
   - Rules all reflections must follow

---

## Calibration and Grounding Mechanisms

### 1. Confidence Score Grounding

The LLM is explicitly instructed that `overall_confidence` is:
- **0.0**: Complete failure, no work accomplished
- **0.5**: Partial success, mixed outcomes
- **1.0**: Complete success, all objectives met

Grounding is provided through:
- Observation outcomes (success/failure counts)
- Overall progress metric (0-1)
- Error pattern information

### 2. Succeeded vs. Confidence

Two independent signals:
- **`succeeded`** (boolean): Did we accomplish core objectives?
- **`overall_confidence`** (0-1): How confident are we in the outcome?

Example interpretations:
- `succeeded=true, confidence=0.95`: Success with high confidence
- `succeeded=true, confidence=0.6`: Success but with concerns
- `succeeded=false, confidence=0.3`: Failed, low confidence in process
- `succeeded=false, confidence=0.8`: Failed despite high confidence (plan issue)

### 3. Lessons Learned Format

LLM is instructed: "must be concrete and actionable (used for future retrieval)"

Examples of good lessons:
- "When searching for documentation, first check official GitHub repos"
- "API calls fail without proxy on corporate network"
- "Tool X requires authentication even for public queries"

### 4. Suggested Adjustment Constraint

Only populated when `needs_replan=true`.

Used to build new user message: `new_message = adjustment + "\nOriginal: " + old_message`

Adjustment typically rephrases the request or adds constraints based on observations.

### 5. Error Pattern Correlation

The LLM sees error patterns (permission_error, network_error, etc.) which help it:
- Correctly attribute failure causes
- Suggest systemic vs. transient adjustments
- Set appropriate `needs_replan` value

Example:
- All observations denied → likely permission issue → suggest "retry with different approach"
- Network errors → transient → suggest "retry same plan"

---

## Fallback and Resilience

### Parse Failure Fallback

If JSON cannot be extracted:
```go
Reflection{
    OverallConfidence: 0.5,
    Succeeded: obsResult.SuccessCount > 0,
    FinalAnswer: "Task completed with partial results.",
    NeedsReplan: false,
    ReplanReason: "",
    LessonsLearned: [],
    SuggestedAdjustment: "",
}
```

This ensures:
- No crash on malformed output
- Conservative confidence (50%)
- Conservative decision (no replan)
- At least one accurate field (Succeeded)

### Approval Request Fallback

If channel doesn't support interactive approval:
```go
if !ok {  // channel doesn't implement ReflectionSender
    return ReplanContinue, nil  // auto-continue
}
```

All channels that don't support reflection default to continuing with current result.

---

## Memory Operations

**File**: `internal/agent/reflect.go`, function `saveExperience()`

After reflection completes, the agent saves experience:

### Fact Extraction Path (if enabled)
1. Extract distilled facts from: goal + final_answer pair
2. Apply lifecycle management (ADD/UPDATE/DELETE/NOOP decisions)
3. Send memory operation summary notification

### Fallback Path
1. Build raw cognitive experience string with:
   - GOAL
   - PLAN summary
   - OUTCOME (succeeded flag + confidence)
   - LESSONS (if any)
2. Save to memories table with metadata
3. Extract graph entities for knowledge graph

### Graph Entity Extraction
Uses goal + final_answer to populate knowledge graph relationships.

---

## Execution Flow Diagram

```
┌─ User Message ─────────────┐
│                             │
├──────────────────────────────┤
│ PERCEIVE: Build CognitiveState
│  (Goal, Memories, Context)
└────────────┬──────────────────┘
             │
             ├──────────────────────────────────────┐
             │                                      │
             v                                      │
┌──────────────────────────┐                        │
│ PLAN: Generate TaskPlan  │                        │
│ (SubTasks, Confidence)   │                        │
└────────────┬─────────────┘                        │
             │                                      │
             v                                      │
┌──────────────────────────┐    RL Adjustment       │
│ ACT: Execute SubTasks    │◄───(if enabled)        │
│ (Observations)           │                        │
└────────────┬─────────────┘                        │
             │                                      │
             v                                      │
┌──────────────────────────┐                        │
│ OBSERVE: Compute Stats   │                        │
│ (Success, Progress,      │                        │
│  Error Patterns)         │                        │
└────────────┬─────────────┘                        │
             │                                      │
             v                                      │
    ╔═══════════════════════╗                        │
    ║ REFLECT: LLM Scoring  ║◄───────────────────────┘
    ║ (Parse JSON)          ║
    ║ Returns:              ║
    ║ - overall_confidence  ║
    ║ - succeeded           ║
    ║ - needs_replan        ║
    ║ - final_answer        ║
    ║ - suggested_adjustment║
    ║ - lessons_learned     ║
    ╚═══════════┬───────════╝
                │
         ┌──────┴──────┐
         │             │
    Check Decision   Stream Answer
    confidence <     to User
    threshold?
         │
    ┌────┴────┐
    YES       NO
    │         │
    v         v
  Ask User  Accept
  (approve/  (go to
   adjust/   persist)
   abort)
    │
    └─────────┐
              v
         ┌─────────────────┐
         │ PERSIST:        │
         │ Save experience │
         │ to memory.md    │
         └─────────────────┘
```

---

## Summary Table

| Aspect | Value | Source |
|--------|-------|--------|
| **Phase** | REFLECT | cognitive.go line 305 |
| **LLM Prompt** | System + User template | cognitive_prompts.go |
| **Default Max Tokens** | 1024 | config.go line 394 |
| **JSON Parsing Attempts** | 3 fallbacks | reflect.go line 304-330 |
| **Confidence Range** | 0.0 - 1.0 | cognitive_types.go |
| **Default Threshold** | 0.6 | cognitive.go line 223 |
| **Max Replan Attempts** | 2 (3 total) | cognitive.go line 227 |
| **Replan Trigger** | confidence < threshold && needs_replan | cognitive.go line 331 |
| **Parse Failure Default Confidence** | 0.5 | reflect.go line 120 |
| **Progress Calculation** | success_count / effective_subtasks | observe.go line 40 |
| **RL Episode Reward** | -1.5 to +1.5 | rl_helpers.go line 42-56 |

---

## Key Insights

1. **Scoring is Two-Dimensional**
   - `succeeded` (boolean) addresses outcome
   - `overall_confidence` (float) addresses certainty
   - Both inform replanning decision

2. **LLM Provides Suggested Adjustment**
   - Not computed by agent logic
   - Used to construct new goal for next attempt
   - Appended to original goal to preserve context

3. **Careful Fallback Design**
   - Three-level JSON parsing (direct → markdown → regex)
   - 50% confidence fallback on parse failure
   - Conservative replan policy (no replan on fallback)

4. **Progress-Based Grounding**
   - OBSERVE computes actual execution metrics
   - REFLECT receives these metrics in prompt
   - Confidence score should correlate with progress

5. **Async Memory Operations**
   - Reflection completes immediately
   - Memory write happens in background
   - Prevents blocking on slow I/O

6. **RL Integration Points**
   - PPO adjusts plan confidence before execution
   - DQN suggests replan actions
   - Episode reward captures outcome quality

---

End of Analysis
