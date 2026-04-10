# 🚀 REFLECT Phase Documentation - START HERE

Welcome! You have comprehensive documentation on the REFLECT phase scoring mechanism for the IronClaw cognitive agent.

---

## ⚡ Quick Start (5 minutes)

**What is the REFLECT phase?**  
It's the 5th and final step in the cognitive loop (PERCEIVE → PLAN → ACT → OBSERVE → REFLECT). It uses an LLM to evaluate task results and decide whether to replan.

**What does REFLECT output?**
```json
{
  "overall_confidence": 0.75,
  "succeeded": true,
  "final_answer": "Task completed successfully",
  "needs_replan": false,
  "lessons_learned": ["..."],
  "suggested_adjustment": "..."
}
```

**What happens next?**
- If confidence > threshold AND needs_replan = true → ask user for approval
- Otherwise → complete task

---

## 📚 Documentation Guide

### 🎯 **Level 1: Quick Answers** (15 minutes)
**File**: `REFLECT_QUICK_REFERENCE.md`

Start here if you want to:
- Understand what REFLECT does
- Know the default configuration
- Find LLM prompts
- Understand replan logic
- Debug common issues

**Read this first!**

---

### 🔍 **Level 2: Deep Understanding** (45 minutes)
**File**: `REFLECT_SCORING_MECHANISM.md`

Read this if you want to:
- Understand the complete architecture
- Know how scoring works
- Learn about RL integration
- See data flow diagrams
- Understand memory operations

---

### 💻 **Level 3: Implementation Details** (30 minutes)
**File**: `REFLECT_CODE_WALKTHROUGH.md`

Study this if you want to:
- See actual code examples
- Understand function signatures
- Trace complete execution flow
- Debug specific issues
- Modify the implementation

---

### 🎨 **Level 4: Visual Reference** (20 minutes)
**File**: `REFLECT_VISUAL_SUMMARY.txt`

Use this for:
- ASCII diagrams of flow
- Decision flowcharts
- Configuration tables
- Quick code lookups

---

### 🗺️ **Level 5: Complete Navigation** (5 minutes)
**File**: `REFLECT_PHASE_INDEX.md`

Use this to:
- Find what you're looking for
- Navigate between documents
- Understand document relationships

---

## 🎯 Choose Your Path

### "I just want to understand REFLECT quickly"
→ **5 min**: Skim this file  
→ **15 min**: Read REFLECT_QUICK_REFERENCE.md  
→ **5 min**: Look at REFLECT_VISUAL_SUMMARY.txt diagrams  
**Total: 25 minutes**

---

### "I need to fix a bug"
→ **15 min**: REFLECT_QUICK_REFERENCE.md → "Debugging" section  
→ **15 min**: REFLECT_CODE_WALKTHROUGH.md → relevant function  
→ **10 min**: Check source code with documentation  
**Total: 40 minutes**

---

### "I want to modify the prompt"
→ **10 min**: REFLECT_QUICK_REFERENCE.md → "LLM Call Format"  
→ **15 min**: REFLECT_CODE_WALKTHROUGH.md → "Prompt Construction"  
→ **5 min**: Locate `cognitive_prompts.go`  
→ **5 min**: Make changes  
**Total: 35 minutes**

---

### "I need to understand RL integration"
→ **20 min**: REFLECT_SCORING_MECHANISM.md → "RL Integration" section  
→ **20 min**: REFLECT_CODE_WALKTHROUGH.md → "Reward Calculation"  
→ **10 min**: Review `rl_helpers.go`  
**Total: 50 minutes**

---

### "I'm implementing a major change"
→ **30 min**: Read REFLECT_SCORING_MECHANISM.md completely  
→ **45 min**: Study REFLECT_CODE_WALKTHROUGH.md completely  
→ **15 min**: Review source files  
→ **30 min**: Plan and implement changes  
**Total: 2 hours**

---

## 🔑 Key Concepts at a Glance

| Concept | Value | File | Line |
|---------|-------|------|------|
| Confidence Threshold | 0.6 | config.go | 390 |
| Max Replan Attempts | 2 | config.go | 392 |
| Replan Trigger | confidence < 0.6 AND needs_replan | cognitive.go | 331 |
| Default Confidence | 0.5 | reflect.go | 121 |
| Reward Formula | (success ? 1.0 : -1.0) + progress * 0.5 | rl_helpers.go | 43-44 |
| JSON Parsing | 3-level fallback | reflect.go | 304-342 |

---

## ❓ Common Questions

**Q: When does the LLM get called for reflection?**  
A: After ACT/OBSERVE phases complete (cognitive.go line 305)

**Q: What information does the LLM see?**  
A: Goal + Plan + Observations + Statistics + Memory + History (reflect.go lines 89-115)

**Q: Can I change the confidence threshold?**  
A: Yes! Edit `agent.cognitive.confidence_threshold` in config (default: 0.6)

**Q: What if the LLM response isn't valid JSON?**  
A: Automatic fallback: confidence=0.5, succeeded=(success_count > 0) (reflect.go lines 120-125)

**Q: How many times can a task be replanned?**  
A: Max replan_attempts attempts (default 2 = 3 total passes allowed)

**Q: Are memory operations async?**  
A: Yes, background goroutine with fresh context (reflect.go line 182)

**Q: How is task progress calculated?**  
A: success_count / (total_tasks - skipped) clamped to [0.0, 1.0]

---

## 📁 All Documentation Files

- **REFLECT_QUICK_REFERENCE.md** - Quick lookup guide
- **REFLECT_SCORING_MECHANISM.md** - Deep technical dive  
- **REFLECT_CODE_WALKTHROUGH.md** - Implementation guide
- **REFLECT_VISUAL_SUMMARY.txt** - ASCII diagrams
- **REFLECT_PHASE_INDEX.md** - Navigation hub
- **REFLECT_DOCUMENTATION_MANIFEST.md** - Complete manifest
- **REFLECT_START_HERE.md** - This file

**Total: ~112 KB of comprehensive documentation**

---

## 🚀 Next Steps

1. **Choose your learning path above** based on your needs
2. **Start with the recommended file** for your use case
3. **Reference REFLECT_VISUAL_SUMMARY.txt** for diagrams
4. **Check source code** with line numbers from docs
5. **Use REFLECT_PHASE_INDEX.md** to navigate

---

## ✨ What This Documentation Covers

✓ Complete REFLECT phase architecture  
✓ LLM prompting and response parsing  
✓ Replan decision logic and thresholds  
✓ Confidence scoring mechanism  
✓ RL integration (PPO/DQN)  
✓ Memory operations  
✓ Error handling & fallbacks  
✓ Configuration schema  
✓ Complete code examples  
✓ Data flow diagrams  
✓ Debugging guides  

---

## 💡 Pro Tips

1. **Use Ctrl+F** to search for specific topics in markdown files
2. **Keep REFLECT_VISUAL_SUMMARY.txt open** while reading code
3. **Reference line numbers** from docs when reading source
4. **Check the manifest** for complete file listings
5. **Start with the index** if you're unsure what to read

---

**Ready?** Start with **REFLECT_QUICK_REFERENCE.md** or choose a path above!

---

*Documentation created: April 9, 2026*  
*Coverage: 100% of REFLECT phase mechanics*  
*Status: Complete and verified*
