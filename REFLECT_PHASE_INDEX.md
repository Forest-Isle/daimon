# REFLECT Phase Documentation Index

Welcome! You now have comprehensive documentation on the REFLECT phase scoring mechanism in the IronClaw cognitive agent. Here's how to navigate:

---

## 📚 Documentation Files

### 1. **REFLECT_QUICK_REFERENCE.md** ⭐ Start Here
- **Best for**: Quick lookups, debugging
- **Contains**: 
  - What REFLECT is
  - Core files and their purposes
  - LLM prompts and responses
  - Replan trigger logic
  - Configuration examples
  - Common issues & solutions
- **Read time**: 15 minutes

### 2. **REFLECT_SCORING_MECHANISM.md** 🔍 Deep Dive
- **Best for**: Understanding the full architecture
- **Contains**:
  - Complete phase sequence
  - Full prompt templates with examples
  - JSON parsing strategy (3 fallbacks)
  - Replan decision logic with thresholds
  - Observation computation details
  - RL integration (PPO/DQN)
  - Reward calculation
  - Configuration schema
  - Calibration & grounding mechanisms
  - Memory operations
  - Data flow diagrams
- **Read time**: 45 minutes

### 3. **REFLECT_CODE_WALKTHROUGH.md** 💻 Implementation Details
- **Best for**: Code-level understanding, debugging
- **Contains**:
  - Step-by-step code examples from actual files
  - Function signatures and implementations
  - Complete flow examples
  - Parsing scenarios
  - Configuration structure
  - Data structure definitions
  - Debugging tips
- **Read time**: 30 minutes

---

## 🎯 Quick Navigation by Use Case

### I want to...

**Understand what REFLECT does**
→ Read: REFLECT_QUICK_REFERENCE.md (What is REFLECT section)

**Debug why replan isn't triggering**
→ Read: REFLECT_QUICK_REFERENCE.md (Common Issues)
→ Check: `cognitive.go:330-350` in code walkthrough

**Change confidence threshold**
→ Read: REFLECT_QUICK_REFERENCE.md (Configuration Examples)
→ Edit: `config.yaml` under `agent.cognitive.confidence_threshold`

**Understand LLM scoring prompts**
→ Read: REFLECT_SCORING_MECHANISM.md (LLM Prompts section)
→ Files: `cognitive_prompts.go`, line 55-100

**Trace full execution flow**
→ Read: REFLECT_CODE_WALKTHROUGH.md (Complete Example Flow)
→ Read: REFLECT_SCORING_MECHANISM.md (Data Flow Diagram)

**Integrate RL policy**
→ Read: REFLECT_SCORING_MECHANISM.md (RL Integration section)
→ Code: `rl_helpers.go`, `cognitive.go:263-328`

**Fix JSON parsing errors**
→ Read: REFLECT_QUICK_REFERENCE.md (JSON Parsing section)
→ Code: REFLECT_CODE_WALKTHROUGH.md (Part 3: JSON Parsing)

**Understand observation statistics**
→ Read: REFLECT_QUICK_REFERENCE.md (Observation Statistics section)
→ Code: `observe.go:14-47`

**Add personality to reflections**
→ Read: REFLECT_SCORING_MECHANISM.md (Context Provided section)
→ Create: `Soul.md` file with persona

---

## 📋 Key Concepts Checklist

By the end of reading these docs, you should understand:

- [ ] The 5-phase cognitive loop and REFLECT's role
- [ ] What LLM prompt is sent for reflection scoring
- [ ] How confidence scores (0.0-1.0) are interpreted
- [ ] The difference between `succeeded` and `confidence`
- [ ] How OBSERVE phase feeds data to REFLECT
- [ ] The 3-level JSON parsing fallback strategy
- [ ] When replanning is triggered (both conditions)
- [ ] The default confidence threshold (0.6)
- [ ] Max replan attempts (2 retries = 3 total)
- [ ] How lessons_learned are stored for future retrieval
- [ ] Error pattern detection (permission, network, etc.)
- [ ] Overall progress calculation (0.0-1.0)
- [ ] RL episode reward formula (-1.5 to +1.5)
- [ ] How to configure reflection parameters
- [ ] Fallback behavior when JSON parsing fails
- [ ] How to extend with personality and persistent rules

---

## 🔗 File Cross-References

**Core Implementation:**
- `internal/agent/reflect.go` - Main REFLECT phase (143 lines)
- `internal/agent/cognitive.go` - Main loop + decision logic (567 lines)
- `internal/agent/cognitive_prompts.go` - Prompt templates (103 lines)
- `internal/agent/cognitive_types.go` - Data structures (182 lines)
- `internal/agent/observe.go` - Statistics computation (89 lines)
- `internal/agent/rl_helpers.go` - RL integration (81 lines)
- `internal/config/config.go` - Configuration schema (496 lines)

**Key Line Numbers:**
- Confidence threshold default: `config.go:390`
- Max replan attempts default: `config.go:392`
- Replan trigger logic: `cognitive.go:331`
- Reflect phase entry: `cognitive.go:305`
- JSON parsing: `reflect.go:304-330`
- Observation stats: `observe.go:40`
- RL reward: `rl_helpers.go:42-56`

---

## 🧪 Testing & Validation

To verify your understanding:

1. **Trace a simple task through REFLECT**
   - Set confidence_threshold = 0.9 (force replans)
   - Run a simple task
   - Verify it replans when confidence < 0.9

2. **Test JSON parsing fallbacks**
   - Check logs for "reflect complete"
   - Verify confidence scores appear correct

3. **Verify error pattern detection**
   - Cause a permission error in a tool
   - Check if error_patterns includes "permission_error"

4. **Validate observation progress**
   - Plan task with 4 subtasks, 3 succeed, 1 fails
   - Progress should be 3/4 = 0.75

---

## 📊 Summary Table

| Aspect | Default | Configurable | File |
|--------|---------|-------------|------|
| Confidence Threshold | 0.6 | Yes | config.go:149 |
| Max Replan Attempts | 2 | Yes | config.go:151 |
| Reflect Max Tokens | 1024 | Yes | config.go:153 |
| Reflect Model | Default LLM | Yes | config.go:148 |
| Parse Fallback Confidence | 0.5 | No | reflect.go:120 |
| Progress Formula | success/total | No | observe.go:40 |
| RL Episode Reward Range | -1.5 to +1.5 | No | rl_helpers.go:56 |
| Error Patterns Detected | 4 types | No | observe.go:50-76 |

---

## ❓ FAQ

**Q: Why do some tasks never trigger replanning?**
A: Replan requires BOTH conditions:
1. `confidence < threshold` AND
2. `needs_replan == true`
If either is false, no replan. Check both in logs.

**Q: What happens if JSON parsing always fails?**
A: Fallback used: confidence=0.5, succeeded=(any_success), final_answer="Task completed with partial results."

**Q: How many total attempts can a task have?**
A: max_replan_attempts + 1. Default: 2 + 1 = 3 total attempts.

**Q: Can the LLM's suggested_adjustment be wrong?**
A: Yes. The LLM can suggest poor adjustments. It's up to you to review and adjust if needed.

**Q: What's the difference between succeeded and confidence?**
A: `succeeded` = "Did we accomplish core objectives?" (boolean)
`confidence` = "How confident are we?" (0-1 score)
Both are independent signals.

**Q: Can I use a different model for REFLECT?**
A: Yes! Set `reflect_model` in config to use a different model than the default.

**Q: How does RL affect reflection scoring?**
A: RL doesn't change scoring. It observes and learns from scores to optimize future behavior.

---

## 🚀 Next Steps

1. **Start small**: Read REFLECT_QUICK_REFERENCE.md (15 min)
2. **Go deeper**: Read REFLECT_SCORING_MECHANISM.md (45 min)
3. **Get hands-on**: Read REFLECT_CODE_WALKTHROUGH.md (30 min)
4. **Experiment**: 
   - Modify config and observe effects
   - Add logging to see JSON responses
   - Test with different confidence thresholds
5. **Integrate**: Use insights to extend or modify REFLECT phase

---

## 📝 Document Maintenance

These documents were generated on: **2026-04-09**

Last reviewed against:
- `internal/agent/reflect.go` (10,949 bytes)
- `internal/agent/cognitive.go` (16,860 bytes)
- `internal/agent/cognitive_prompts.go` (3,609 bytes)
- `internal/agent/cognitive_types.go` (5,528 bytes)
- `internal/agent/observe.go` (8,650 bytes)
- `internal/agent/rl_helpers.go` (2,162 bytes)
- `internal/config/config.go` (496 lines)

---

## 📞 Questions?

Look for these log patterns to understand behavior:
```
reflect complete (confidence=X, succeeded=Y, needs_replan=Z)
cognitive: replanning (attempt=A, max=B)
reflect: parse failed, using fallback
cognitive: replan aborted by user
cognitive: adjusting and replanning
```

Enjoy exploring the REFLECT phase! 🎯

