# REFLECT Phase - Code Walkthrough & Examples

## File Structure

```
internal/agent/
├── reflect.go              # Main REFLECT phase implementation
├── cognitive.go            # Main loop + replan logic
├── cognitive_prompts.go    # Prompt templates
├── cognitive_types.go      # Data structures
├── observe.go              # OBSERVE phase (input to REFLECT)
├── rl_helpers.go           # RL integration
└── ...
internal/config/
└── config.go               # Configuration schema
```

---

## Part 1: Running REFLECT Phase

### Entry Point: `Reflector.Run()`

**File**: `reflect.go:81-137`

```go
func (r *Reflector) Run(
    ctx context.Context,
    ch channel.Channel,
    target channel.MessageTarget,
    state *CognitiveState,
    plan *TaskPlan,
    obsResult *ObservationResult,
) (*Reflection, error) {
    // 1. Build user message from template + observations
    userMsg := buildReflectUserMessage(state, plan, obsResult)
    
    // 2. Get max tokens (default 1024)
    maxTokens := r.cfg.ReflectMaxTokens
    if maxTokens <= 0 {
        maxTokens = 1024
    }
    
    // 3. Build system prompt with optional personality + rules
    system := ReflectSystemPrompt
    if state.Personality != "" {
        system += "\n\nPERSONALITY (apply to final_answer tone):\n" + state.Personality
    }
    if state.PersistentRules != "" {
        system += "\n\nADDITIONAL RULES (must follow):\n" + state.PersistentRules
    }
    
    // 4. Create LLM request
    req := CompletionRequest{
        Model:     r.llmModel,
        System:    system,
        Messages:  []CompletionMessage{{Role: "user", Content: userMsg}},
        Tools:     nil,  // NO TOOLS IN REFLECT
        MaxTokens: maxTokens,
    }
    
    // 5. Call LLM provider
    resp, err := r.provider.Complete(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("reflect llm call: %w", err)
    }
    
    // 6. Parse response
    reflection, err := parseReflectResponse(resp.Text)
    if err != nil {
        slog.Warn("reflect: parse failed, using fallback", "err", err)
        // FALLBACK: Default values
        reflection = &Reflection{
            OverallConfidence: 0.5,
            Succeeded:         obsResult.SuccessCount > 0,
            FinalAnswer:       "Task completed with partial results.",
        }
    }
    
    // 7. Log result
    slog.Info("reflect complete",
        "confidence", reflection.OverallConfidence,
        "succeeded", reflection.Succeeded,
        "needs_replan", reflection.NeedsReplan,
    )
    
    // 8. Save experience (async, background)
    r.saveExperience(ctx, ch, target, state, plan, reflection)
    
    return reflection, nil
}
```

---

## Part 2: Building User Message

### Function: `buildReflectUserMessage()`

**File**: `reflect.go:264-302`

```go
func buildReflectUserMessage(state *CognitiveState, plan *TaskPlan, obsResult *ObservationResult) string {
    // 1. Format observations
    var obsSB strings.Builder
    if len(obsResult.Observations) == 0 {
        obsSB.WriteString("(no tool executions)")
    } else {
        for _, obs := range obsResult.Observations {
            // SubTask {id} [{tool_name}]:
            fmt.Fprintf(&obsSB, "- SubTask %s [%s]:\n", obs.SubTaskID, obs.ToolName)
            
            if obs.Denied {
                obsSB.WriteString("  Status: DENIED\n")
            } else if obs.Error != "" {
                fmt.Fprintf(&obsSB, "  Status: FAILED\n  Error: %s\n", obs.Error)
            } else {
                // SUCCESS: show output (max 500 chars)
                output := obs.Output
                if len(output) > 500 {
                    output = output[:500] + "...[truncated]"
                }
                fmt.Fprintf(&obsSB, "  Status: SUCCESS\n  Output: %s\n", output)
            }
        }
    }
    
    // 2. Build statistics line
    stats := fmt.Sprintf(
        "Success: %d, Failures: %d, Denied: %d, Progress: %.0f%%, Error patterns: %s",
        obsResult.SuccessCount,
        obsResult.FailureCount,
        obsResult.DeniedCount,
        obsResult.OverallProgress*100,  // 0-100%
        strings.Join(obsResult.ErrorPatterns, ", "),
    )
    
    // 3. Fill template with all values
    msg := ReflectUserPromptTemplate
    msg = strings.ReplaceAll(msg, "{{GOAL}}", state.Goal.Raw)
    msg = strings.ReplaceAll(msg, "{{PLAN_SUMMARY}}", plan.Summary)
    msg = strings.ReplaceAll(msg, "{{OBSERVATIONS}}", obsSB.String())
    msg = strings.ReplaceAll(msg, "{{STATS}}", stats)
    
    return msg
}
```

**Example Output:**
```
ORIGINAL GOAL:
Find documentation for library X and summarize key features

PLAN SUMMARY:
1. Search GitHub repo, 2. Read README, 3. Extract key features, 4. Synthesize summary

EXECUTION OBSERVATIONS:
- SubTask t1 [web_search]:
  Status: SUCCESS
  Output: Found repo at github.com/foo/bar with 5k stars...

- SubTask t2 [read_file]:
  Status: SUCCESS
  Output: README contains: Features include A, B, C...

- SubTask t3 [synthesis]:
  Status: SUCCESS
  Output: Key features: A (async), B (type-safe), C (fast)...

STATISTICS:
Success: 3, Failures: 0, Denied: 0, Progress: 100%, Error patterns: none

Produce the JSON reflection now.
```

---

## Part 3: JSON Parsing with Fallbacks

### Function: `parseReflectResponse()`

**File**: `reflect.go:304-342`

```go
// Three regex patterns defined at package level (line 193-194 in plan.go)
var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
var jsonObjectRe = regexp.MustCompile(`(?s)\{.*\}`)

func parseReflectResponse(text string) (*Reflection, error) {
    raw := strings.TrimSpace(text)
    
    var rj reflectJSON
    
    // ATTEMPT 1: Direct JSON parse
    if err := json.Unmarshal([]byte(raw), &rj); err == nil {
        return reflectJSONToReflection(rj), nil  // SUCCESS
    }
    
    // ATTEMPT 2: Extract ```json {...} ``` block
    if m := jsonBlockRe.FindStringSubmatch(raw); len(m) == 2 {
        if err := json.Unmarshal([]byte(m[1]), &rj); err == nil {
            return reflectJSONToReflection(rj), nil  // SUCCESS
        }
    }
    
    // ATTEMPT 3: Extract first {...} block
    if m := jsonObjectRe.FindString(raw); m != "" {
        if err := json.Unmarshal([]byte(m), &rj); err == nil {
            return reflectJSONToReflection(rj), nil  // SUCCESS
        }
    }
    
    // ALL FAILED: Return error (caller handles fallback)
    return nil, fmt.Errorf("no valid JSON found in reflect response")
}

func reflectJSONToReflection(rj reflectJSON) *Reflection {
    return &Reflection{
        OverallConfidence:   rj.OverallConfidence,
        Succeeded:           rj.Succeeded,
        LessonsLearned:      rj.LessonsLearned,
        SuggestedAdjustment: rj.SuggestedAdjustment,
        FinalAnswer:         rj.FinalAnswer,
        NeedsReplan:         rj.NeedsReplan,
        ReplanReason:        rj.ReplanReason,
    }
}
```

**Example Parsing Scenarios:**

Scenario 1 - Pure JSON:
```json
{"overall_confidence": 0.85, "succeeded": true, ...}
```
→ Attempt 1 succeeds

Scenario 2 - Code block:
```
Here's my evaluation:
```json
{
  "overall_confidence": 0.85,
  "succeeded": true,
  ...
}
```
```
→ Attempt 2 succeeds (extracts JSON from block)

Scenario 3 - Mixed prose:
```
Based on the observations, I believe:
- The task succeeded
- Confidence: 0.85
{
  "overall_confidence": 0.85,
  "succeeded": true,
  ...
}
I recommend continuing.
```
→ Attempt 3 succeeds (extracts first {...} block)

Scenario 4 - All fail:
```
The task completed successfully with high confidence.
```
→ All attempts fail, fallback used:
```go
Reflection{
    OverallConfidence: 0.5,
    Succeeded: obsResult.SuccessCount > 0,
    FinalAnswer: "Task completed with partial results.",
}
```

---

## Part 4: Replan Decision Logic

### Location: `cognitive.go:330-350`

```go
// ── REFLECT ───────────────────────────────────────
reflection, err := ca.reflector.Run(ctx, ch, target, state, plan, obsResult)
if err != nil {
    slog.Error("cognitive: reflect failed", "err", err)
    break
}

finalAnswer = reflection.FinalAnswer
if finalAnswer == "" {
    finalAnswer = "Task completed."
}

// Stream final answer to user
if err := ca.streamFinalAnswer(ctx, ch, target, sess, finalAnswer); err != nil {
    slog.Warn("cognitive: stream final answer failed", "err", err)
}

// RL: update reflection confidence and record DQN suggestion
if rlEnabled && rlState != nil && reflection != nil {
    rlState.ReflectionConfidence = clampRL(reflection.OverallConfidence, 0, 1)
    if reflection.NeedsReplan {
        dqnAction := ca.rlPolicy.SelectReplanAction(rlState)
        slog.Info("cognitive: DQN replan suggestion", "action", dqnAction.String())
    }
}

// ╔═ CRITICAL DECISION POINT ═╗
// Check if replan is needed
if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
    // Get user approval
    decision, _ := ca.reflector.RequestReplanApproval(ctx, ch, target, reflection)
    
    switch decision {
    case ReplanAbort:
        // User aborts: stop immediately
        slog.Info("cognitive: replan aborted by user", "session", sess.ID)
        goto persist
        
    case ReplanContinue:
        // User continues: skip replanning
        slog.Info("cognitive: replan skipped (continue)", "session", sess.ID)
        goto persist
        
    case ReplanAdjust:
        // User adjusts: modify goal and retry
        slog.Info("cognitive: adjusting and replanning", "session", sess.ID)
        if reflection.SuggestedAdjustment != "" {
            // BUILD NEW USER MESSAGE:
            // new = suggested_adjustment + "\nOriginal: " + old
            state.UserMessage = reflection.SuggestedAdjustment + "\nOriginal: " + state.UserMessage
        }
        continue  // Go to next attempt (back to PLAN phase)
    }
}

break // No replan needed, exit loop
```

### Configuration: Lines 221-228

```go
cogCfg := ca.cfg.Cognitive

// Load confidence threshold
confidenceThreshold := cogCfg.ConfidenceThreshold
if confidenceThreshold <= 0 {
    confidenceThreshold = 0.6  // DEFAULT: 60%
}

// Load max replan attempts
maxReplans := cogCfg.MaxReplanAttempts
if maxReplans <= 0 {
    maxReplans = MaxReplanAttempts  // DEFAULT: 2 (3 total)
}
```

### Loop Structure: Lines 249-350

```go
// ╔═ REPLAN LOOP ═╗
for attempt := 0; attempt <= maxReplans; attempt++ {
    if attempt > 0 {
        slog.Info("cognitive: replanning", "attempt", attempt, "max", maxReplans)
    }
    
    // PLAN phase
    plan, err = ca.planner.Run(ctx, state)
    // ... validation ...
    
    // ACT phase
    observations, actErr := ca.executor.RunWithContext(...)
    
    // OBSERVE phase
    obsResult = ca.observer.Run(observations, plan)
    
    // REFLECT phase
    reflection, err = ca.reflector.Run(ctx, ch, target, state, plan, obsResult)
    
    // ◄─ DECISION POINT ─►
    if reflection.OverallConfidence < confidenceThreshold && reflection.NeedsReplan {
        // Get approval...
        switch decision {
        case ReplanAdjust:
            continue  // ◄─ LOOP: Next attempt
        default:
            break  // ◄─ BREAK: Exit loop
        }
    }
    
    break  // ◄─ NORMAL EXIT: Success or no replan needed
}
```

---

## Part 5: Observation Statistics Computation

### Location: `observe.go:14-47`

```go
func (o *Observer) Run(observations []Observation, plan *TaskPlan) *ObservationResult {
    result := &ObservationResult{
        Observations: observations,
    }
    
    // Calculate effective tasks (total - skipped)
    total := len(plan.SubTasks)
    skippedCount := 0
    for _, st := range plan.SubTasks {
        if st.Status == SubTaskSkipped {
            skippedCount++
        }
    }
    
    // Count outcomes
    for _, obs := range observations {
        if obs.Denied {
            result.DeniedCount++
        } else if obs.Error != "" {
            result.FailureCount++
        } else {
            result.SuccessCount++
        }
    }
    
    // Calculate progress: successful / effective
    effective := total - skippedCount
    if effective > 0 {
        result.OverallProgress = float64(result.SuccessCount) / float64(effective)
    }
    // Range: [0.0, 1.0]
    // 0.0 = no success, 1.0 = all success
    
    // Detect error patterns
    result.ErrorPatterns = detectErrorPatterns(observations, result)
    
    return result
}
```

**Example:**
```
Plan has 5 subtasks:
- t1: DONE (success)
- t2: DONE (success)
- t3: SKIPPED
- t4: DONE (failure)
- t5: DONE (denied)

Calculation:
- SuccessCount = 2
- FailureCount = 1
- DeniedCount = 1
- SkippedCount = 1
- EffectiveCount = 5 - 1 = 4

OverallProgress = 2 / 4 = 0.5 (50%)
```

---

## Part 6: RL Integration

### Episode Reward Calculation: `rl_helpers.go:42-56`

```go
func computeSimpleEpisodeReward(reflection *Reflection, obs *ObservationResult) float64 {
    if reflection == nil {
        return -0.5
    }
    
    reward := 0.0
    
    // Success/failure component: ±1.0
    if reflection.Succeeded {
        reward += 1.0      // Success bonus
    } else {
        reward -= 1.0      // Failure penalty
    }
    
    // Progress component: 0.0 to 0.5
    if obs != nil {
        reward += obs.OverallProgress * 0.5
    }
    
    return reward
}
```

**Reward Examples:**

| succeeded | progress | reward | interpretation |
|-----------|----------|--------|-----------------|
| true | 1.0 | +1.5 | Perfect success |
| true | 0.5 | +1.25 | Success, partial progress |
| true | 0.0 | +1.0 | Success, no progress (odd) |
| false | 1.0 | +0.5 | Failed but 100% progress |
| false | 0.5 | -0.75 | Failed, partial progress |
| false | 0.0 | -1.0 | Complete failure |
| nil | - | -0.5 | No reflection data |

### State Updates: `rl_helpers.go:25-39`

```go
// After PLAN phase
func updateRLStateWithPlan(s *rl.RLState, plan *TaskPlan) {
    s.SubTaskCount = normalizeRL(float64(len(plan.SubTasks)), 10)
    s.PlanConfidence = clampRL(plan.OverallConfidence, 0, 1)
    s.ReplanCount = normalizeRL(float64(plan.ReplanCount), 5)
}

// After OBSERVE phase
func updateRLStateWithObservation(s *rl.RLState, obs *ObservationResult) {
    s.SuccessCount = normalizeRL(float64(obs.SuccessCount), 10)
    s.FailureCount = normalizeRL(float64(obs.FailureCount), 10)
    s.DeniedCount = normalizeRL(float64(obs.DeniedCount), 10)
    s.Progress = clampRL(obs.OverallProgress, 0, 1)
    s.ErrorPatternCnt = normalizeRL(float64(len(obs.ErrorPatterns)), 5)
}

// Normalization: value / max, clamped to [0, 1]
func normalizeRL(val, maxVal float64) float64 {
    if maxVal <= 0 {
        return 0
    }
    r := val / maxVal
    if r > 1 {
        return 1
    }
    if r < 0 {
        return 0
    }
    return r
}

// Clamping
func clampRL(v, lo, hi float64) float64 {
    if v < lo {
        return lo
    }
    if v > hi {
        return hi
    }
    return v
}
```

---

## Part 7: Configuration

### File: `config.go:145-155`

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

### Defaults: `config.go:389-396`

```go
Cognitive: CognitiveConfig{
    ConfidenceThreshold:    0.6,      // 60%
    MaxParallelTools:       3,
    MaxReplanAttempts:      2,        // 3 total attempts
    PlanMaxTokens:          2048,
    ReflectMaxTokens:       1024,
    ApprovalTimeoutSeconds: 120,
}
```

---

## Part 8: Data Structures

### File: `cognitive_types.go`

```go
// ╔═ Input to REFLECT ═╗
type ObservationResult struct {
    Observations    []Observation      // Individual tool results
    SuccessCount    int                // # of successful tools
    FailureCount    int                // # of failed tools
    DeniedCount     int                // # of denied tools
    OverallProgress float64            // 0.0-1.0
    ErrorPatterns   []string           // ["permission_error", ...]
}

// ╔═ Output of REFLECT ═╗
type Reflection struct {
    OverallConfidence   float64        // 0.0-1.0 confidence score
    Succeeded           bool           // Core objectives met?
    LessonsLearned      []string       // Actionable lessons
    SuggestedAdjustment string         // For replanning
    FinalAnswer         string         // User-visible response
    NeedsReplan         bool           // Should we replan?
    ReplanReason        string         // Why replan is needed
}

// ╔═ JSON Structure (raw LLM output) ═╗
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

---

## Part 9: Complete Example Flow

```
USER INPUT: "Find docs for library X"

│
├─ PERCEIVE: Parse goal, fetch memories
│  state = {
│    Goal: "Find documentation for library X",
│    Complexity: "simple",
│    RelevantMemories: [...]
│  }
│
├─ PLAN: Generate execution plan
│  plan = {
│    Summary: "Search web, read docs, summarize",
│    SubTasks: [
│      {id: "t1", description: "Search web", tool: "web_search", confidence: 0.9},
│      {id: "t2", description: "Read README", tool: "file_read", confidence: 0.95},
│      {id: "t3", description: "Summarize", tool: "", confidence: 0.8}
│    ],
│    OverallConfidence: 0.88
│  }
│
├─ ACT: Execute subtasks
│  observations = [
│    {SubTaskID: "t1", ToolName: "web_search", Status: SUCCESS, Output: "Found..."},
│    {SubTaskID: "t2", ToolName: "file_read", Status: SUCCESS, Output: "README..."},
│    {SubTaskID: "t3", ToolName: "", Status: SUCCESS, Output: "Summary..."}
│  ]
│
├─ OBSERVE: Compute statistics
│  obsResult = {
│    SuccessCount: 3,
│    FailureCount: 0,
│    DeniedCount: 0,
│    OverallProgress: 1.0,
│    ErrorPatterns: []
│  }
│
├─ REFLECT: LLM evaluation
│  USER MESSAGE:
│  ORIGINAL GOAL: Find documentation for library X
│  PLAN SUMMARY: Search web, read docs, summarize
│  EXECUTION OBSERVATIONS:
│  - SubTask t1 [web_search]:
│    Status: SUCCESS
│    Output: Found github.com/...
│  ... (and so on)
│  STATISTICS:
│  Success: 3, Failures: 0, Denied: 0, Progress: 100%, Error patterns: none
│
│  LLM RESPONSE:
│  {
│    "overall_confidence": 0.95,
│    "succeeded": true,
│    "lessons_learned": [
│      "Official GitHub repo is best source for docs",
│      "README typically covers main features"
│    ],
│    "suggested_adjustment": "",
│    "final_answer": "Library X documentation shows...",
│    "needs_replan": false,
│    "replan_reason": ""
│  }
│
│  DECISION LOGIC:
│  confidence (0.95) >= threshold (0.6) → NO REPLAN NEEDED
│  OR
│  needs_replan = false → NO REPLAN NEEDED
│
├─ RETURN: Final answer to user
│  "Library X documentation shows..."
│
└─ SAVE: Experience to memory.md (async)
```

---

## Debugging Tips

### Enable Debug Logging

```bash
export LOG_LEVEL=debug
# Look for log lines:
# - "reflect complete"
# - "cognitive: replanning"
# - "reflect: parse failed"
```

### Check Reflection JSON

Add this in `reflect.go` to see LLM output:

```go
// After resp, err := r.provider.Complete(ctx, req)
fmt.Fprintf(os.Stderr, "LLM Response:\n%s\n\n", resp.Text)
```

### Common Issues

1. **Parse always fails**
   - Check: Is LLM outputting valid JSON?
   - Try: Lower reflect_max_tokens, use simpler prompt

2. **Never replans**
   - Check: Is `reflection.NeedsReplan` true?
   - Check: Is `confidence < threshold`?
   - Try: Lower confidence_threshold to 0.5

3. **Always replans (stuck loop)**
   - Check: Max replan attempts reached?
   - Try: Increase `max_replan_attempts` to catch more attempts
   - Try: Increase `confidence_threshold` to 0.8

