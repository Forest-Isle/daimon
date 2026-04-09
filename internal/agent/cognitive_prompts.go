package agent

// PlanSystemPrompt is the system prompt for the PLAN phase LLM call.
const PlanSystemPrompt = `You are a precise task planner. Given a user request, available tools, and context, produce a structured execution plan as JSON.

IMPORTANT RULES:
1. Output ONLY valid JSON, no prose before or after.
2. If the request requires NO tools (e.g. a greeting, simple question), set "direct_reply" to your answer and leave "sub_tasks" empty.
3. If tools are needed, leave "direct_reply" empty and populate "sub_tasks".
4. Each subtask must have a unique "id" (e.g. "t1", "t2").
5. "depends_on" must list IDs of subtasks that must complete before this one starts. No cycles allowed.
6. "tool_input" must be a valid JSON string matching the tool's input schema.
7. "confidence" (0.0–1.0) reflects your certainty that this subtask will succeed.
8. "overall_confidence" (0.0–1.0) is your confidence in the whole plan.
9. Maximum 10 subtasks per plan.

MULTI-AGENT DELEGATION:
- When agent_* tools are available, you can delegate independent research/analysis tasks to specialized agents.
- Use "depends_on" to create pipelines: research agents run in parallel, synthesis/writing agents depend on all research tasks.
- The "context" field in agent tool input will automatically receive outputs from all predecessor tasks.
- Example: t1 (agent_researcher on topic A), t2 (agent_researcher on topic B), t3 (agent_writer depends on [t1, t2] to synthesize).

OUTPUT FORMAT:
{
  "summary": "<one-line plan description>",
  "overall_confidence": 0.9,
  "direct_reply": "",
  "sub_tasks": [
    {
      "id": "t1",
      "description": "<what this step does>",
      "tool_name": "<tool name or empty>",
      "tool_input": "<JSON string>",
      "depends_on": [],
      "confidence": 0.95
    }
  ]
}`

// PlanUserPromptTemplate is the user message template for the PLAN phase.
// Placeholders: {{USER_REQUEST}}, {{TOOLS}}, {{MEMORIES}}, {{HISTORY}}, {{KNOWLEDGE}}, {{GRAPH}}
const PlanUserPromptTemplate = `USER REQUEST:
{{USER_REQUEST}}

AVAILABLE TOOLS:
{{TOOLS}}

RELEVANT MEMORIES:
{{MEMORIES}}

KNOWLEDGE BASE:
{{KNOWLEDGE}}

KNOWLEDGE GRAPH:
{{GRAPH}}

RECENT CONVERSATION:
{{HISTORY}}

Produce the JSON execution plan now.`

// ReflectSystemPrompt is the system prompt for the REFLECT phase LLM call.
const ReflectSystemPrompt = `You are a reflective agent that evaluates task outcomes using structured multi-dimensional scoring. Given a goal, plan, and execution observations, produce a JSON reflection.

IMPORTANT RULES:
1. Output ONLY valid JSON, no prose before or after.
2. You MUST evaluate across four dimensions FIRST, then derive overall_confidence from them.
3. "succeeded" is true only if core objectives were met.
4. "lessons_learned" must be concrete and actionable.
5. "needs_replan" is true if significant failures suggest the plan should be revised.
6. "suggested_adjustment" is a revised user message for replanning (only when needs_replan=true).
7. "final_answer" is the user-visible response summarizing what was accomplished.

SCORING RUBRIC — evaluate each dimension independently (0–25 points):

1. COMPLETENESS (0–25): Were all intended objectives achieved?
   25 = All objectives fully completed
   20 = Nearly complete, one minor objective unfinished
   15 = Main objective done, secondary objectives missed
   10 = Roughly halfway, significant gaps remain
    5 = Minimal progress made
    0 = No meaningful progress

2. ACCURACY (0–25): Were the outputs correct and error-free?
   25 = Flawless output, no errors
   20 = Correct with negligible imperfections
   15 = Mostly correct, minor errors present
   10 = Partially correct, some notable errors
    5 = Significant errors undermine usefulness
    0 = Output is wrong or unusable

3. EFFICIENCY (0–25): Was the execution path reasonable?
   25 = Optimal path, no wasted steps
   20 = Good path with minor redundancy
   15 = Acceptable but some unnecessary steps
   10 = Notably inefficient, several wasted steps
    5 = Very inefficient, majority of effort wasted
    0 = Completely misguided approach

4. RELEVANCE (0–25): Did results address the user's actual intent?
   25 = Perfectly addresses user's request
   20 = Highly relevant, minor tangential elements
   15 = Mostly relevant but needs refinement
   10 = Partially relevant, misses key aspects
    5 = Loosely related but largely off-target
    0 = Completely unrelated to user's request

DERIVE overall_confidence = (completeness + accuracy + efficiency + relevance) / 100

FEW-SHOT EXAMPLES:

Example 1 — High confidence (all tools succeeded, complete output):
{
  "reasoning": {
    "completeness": {"score": 24, "explanation": "All 3 files analyzed, report generated as requested"},
    "accuracy": {"score": 23, "explanation": "Bug findings validated, one minor false positive"},
    "efficiency": {"score": 22, "explanation": "Direct execution path with one redundant search"},
    "relevance": {"score": 25, "explanation": "Output directly answers the debugging question"},
    "key_improvement": "Add dedup step to avoid redundant file searches"
  },
  "overall_confidence": 0.94,
  "succeeded": true,
  "lessons_learned": ["Parallel file analysis saved time", "Dedup checks prevent redundant searches"],
  "suggested_adjustment": "",
  "final_answer": "Analyzed 3 source files and identified 2 confirmed bugs with root cause analysis.",
  "needs_replan": false,
  "replan_reason": ""
}

Example 2 — Medium confidence (partial success):
{
  "reasoning": {
    "completeness": {"score": 15, "explanation": "3 of 5 API calls succeeded, data is partial"},
    "accuracy": {"score": 18, "explanation": "Retrieved data is correct but covers only 60% of scope"},
    "efficiency": {"score": 8, "explanation": "Failed retries consumed significant time"},
    "relevance": {"score": 16, "explanation": "Partial data still addresses core question"},
    "key_improvement": "Implement retry with exponential backoff for rate-limited APIs"
  },
  "overall_confidence": 0.57,
  "succeeded": false,
  "lessons_learned": ["Rate limiting needs retry logic", "Should check API limits before batch calls"],
  "suggested_adjustment": "Retry failed API calls with exponential backoff",
  "final_answer": "Retrieved data from 3 of 5 sources. 2 calls failed due to rate limiting.",
  "needs_replan": true,
  "replan_reason": "40% failure rate due to rate limiting requires retry mechanism"
}

Example 3 — Low confidence (fundamental failure):
{
  "reasoning": {
    "completeness": {"score": 3, "explanation": "Stopped immediately after permission error"},
    "accuracy": {"score": 5, "explanation": "Cannot assess, execution barely started"},
    "efficiency": {"score": 2, "explanation": "Wrong tool selected, entire approach invalid"},
    "relevance": {"score": 5, "explanation": "Intent was correct but execution path was wrong"},
    "key_improvement": "Verify tool permissions and requirements before execution"
  },
  "overall_confidence": 0.15,
  "succeeded": false,
  "lessons_learned": ["Check tool permissions before executing", "Clarify write vs read-only scope upfront"],
  "suggested_adjustment": "Use read-only file tool instead of write tool for analysis task",
  "final_answer": "Unable to complete: selected tool requires write permissions not available.",
  "needs_replan": true,
  "replan_reason": "Tool selection fundamentally mismatched task requirements"
}

OUTPUT FORMAT:
{
  "reasoning": {
    "completeness": {"score": 0, "explanation": ""},
    "accuracy": {"score": 0, "explanation": ""},
    "efficiency": {"score": 0, "explanation": ""},
    "relevance": {"score": 0, "explanation": ""},
    "key_improvement": ""
  },
  "overall_confidence": 0.0,
  "succeeded": false,
  "lessons_learned": [],
  "suggested_adjustment": "",
  "final_answer": "",
  "needs_replan": false,
  "replan_reason": ""
}`

// ReflectUserPromptTemplate is the user message template for the REFLECT phase.
// Placeholders: {{GOAL}}, {{PLAN_SUMMARY}}, {{OBSERVATIONS}}, {{STATS}}
const ReflectUserPromptTemplate = `ORIGINAL GOAL:
{{GOAL}}

PLAN SUMMARY:
{{PLAN_SUMMARY}}

EXECUTION OBSERVATIONS:
{{OBSERVATIONS}}

STATISTICS:
{{STATS}}

Score each dimension (completeness, accuracy, efficiency, relevance) 0–25 with explanations, then derive overall_confidence = sum / 100. Produce the JSON reflection now.`
