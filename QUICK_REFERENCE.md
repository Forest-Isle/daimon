# IronClaw Subagent System - Quick Reference Guide

## TL;DR

IronClaw allows orchestrator agents to delegate tasks to specialized sub-agents via `agent_*` tools. Each sub-agent:
- Runs with its own scoped tool set (no recursive agent calls)
- Uses a custom system prompt and LLM configuration
- Has its own iteration budget and timeout
- Shares persistent memory with the orchestrator
- Is protected by a circuit breaker from repeated failures

---

## Core Components

| Component | File | Purpose |
|-----------|------|---------|
| **AgentTool** | `agent_tool.go` | Executes sub-agents as tools |
| **AgentSpec** | `spec.go` | Defines agent configuration (YAML) |
| **AgentManager** | `agent_manager.go` | Loads specs, registers as tools |
| **Runtime** | `runtime.go` | Agent loop (reused for sub-agents) |
| **CircuitBreaker** | `circuit_breaker.go` | Prevents cascading failures |
| **TaskContext** | `task_context.go` | Multi-agent pipeline coordination |
| **captureChannel** | `agent_tool.go` | Captures sub-agent output |

---

## Quick Start: Define a Sub-Agent

**Create `~/.IronClaw/agents/researcher.yaml`:**

```yaml
name: researcher
description: Searches the web and compiles findings
system_prompt: |
  You are a research expert. Search for recent findings
  and compile them into a concise summary.
model: claude-3-5-sonnet-20241022      # optional override
max_tokens: 8000                         # optional override
max_iterations: 5
timeout: 180s
tools:
  - http                                 # whitelist: only http, file_read
  - file_read
requires_approval: false
tags:
  - research
  - web
```

**That's it!** On startup, IronClaw will:
1. Load the spec
2. Create `AgentTool("researcher")`
3. Register as `agent_researcher` tool
4. Orchestrator can now call it

---

## How to Call a Sub-Agent (from LLM perspective)

**The LLM sees this in its system prompt:**

```
## Available Agents

You can delegate tasks to specialized agents using the corresponding agent_* tools.
Each agent runs independently with its own tool set and iteration budget.

- **agent_researcher**: Searches the web and compiles findings [tags: research, web]
- **agent_analyzer**: Analyzes research findings [tags: analysis]
```

**The LLM can decide to call:**

```json
{
  "type": "tool_use",
  "id": "tc_123",
  "name": "agent_researcher",
  "input": {
    "task": "Research recent AI developments in 2024-2026",
    "context": ""
  }
}
```

**Result returned to LLM:**

```json
{
  "output": "Recent AI developments include: 1) GPT-5... 2) Claude...",
  "error": ""
}
```

---

## AgentTool Execution (12-Step Process)

```
1. Circuit Breaker Check
   → Fail fast if agent repeatedly errors
   
2. Parse Input
   → Extract task and context strings
   
3. Build Scoped Registry
   → Include only whitelisted tools
   → Exclude all agent_* tools (prevent recursion)
   
4. Merge Configuration
   → Override MaxIterations, SystemPrompt, Model, MaxTokens from spec
   
5. Create Temporary Runtime
   → NewRuntime(provider, scopedTools, sessions, db, mergedCfg, llmCfg)
   
6. Set Shared Resources
   → subRuntime.SetMemoryStore(memStore) [share persistent memory]
   
7. Create Capture Channel
   → In-memory buffer for collecting output
   
8. Build Task Message
   → Combine context (if any) with task
   
9. Execute Sub-Agent
   → subRuntime.HandleMessage(ctx, captureChannel, msg)
   → Runs agent loop until completion
   
10. Collect Output
    → Capture final message from buffer
    
11. Record Result
    → breaker.RecordSuccess() or breaker.RecordFailure()
    
12. Return Result
    → tool.Result{Output: "...", Error: "..."}
```

---

## Key Configuration Points

### In Config File (config.yaml)

```yaml
agent:
  mode: simple                      # or "cognitive" for 5-phase loop
  max_iterations: 10
  system_prompt: |
    You are a helpful AI...
```

### In Agent Spec (agents/*.yaml)

```yaml
name: researcher
system_prompt: |                    # Override system prompt
  Custom prompt for this agent...
model: claude-3-5-sonnet-20241022  # Override LLM model
max_tokens: 8000                    # Override max tokens
max_iterations: 5                   # Override iteration limit
timeout: 180s                       # Execution timeout (default 120s)
tools: [http, file_read]            # Tool whitelist (empty = all, agent_* excluded)
requires_approval: true             # Require user OK before execution
```

---

## Important: Always Excluded

**agent_* tools are ALWAYS excluded from sub-agents:**

```go
// In buildScopedRegistry()
if strings.HasPrefix(name, "agent_") {
    continue  // Skip all agent tools
}
```

**Why?**
- Prevents infinite loops
- Simplifies orchestrator role (orchestrator decides agent sequencing)
- Agent tool not suitable for recursive delegation

---

## Tool Scoping Example

**Orchestrator sees all tools:**
```
bash, file_read, file_write, http, browser,
agent_researcher, agent_analyzer, agent_writer
```

**Sub-agent (researcher) scoped to:**
```
http, file_read
(no agent_* tools)
```

**Sub-agent (writer) scoped to:**
```
file_read, file_write
(no agent_* tools)
```

---

## Circuit Breaker Pattern

**Prevents cascading failures:**

```
Closed (normal)
  ↓ [3 failures]
Open (rejecting)
  ↓ [wait 60s]
HalfOpen (testing 1 request)
  ├─ [success] → Closed
  └─ [failure] → Open
```

**Usage:**
```go
err := breaker.Allow()
if err != nil {
    return Result{Error: err.Error()}  // "circuit breaker open: agent is failing"
}

// ... execute agent ...

if success {
    breaker.RecordSuccess()
} else {
    breaker.RecordFailure()
}
```

---

## Memory Sharing

**All agents access the same memory store:**

```
Orchestrator
├─ memStore (persistent, file-based at ~/.ironclaw/memory/)
│  └─ Shared with all sub-agents
│
└─ agent_researcher
   ├─ Reads from memStore
   ├─ Adds new facts
   └─ Next agent reads updated facts
```

**How:**
```go
// In AgentTool.Execute()
subRuntime.SetMemoryStore(a.memStore)  // Share the store
```

---

## Multi-Agent Pipeline (TaskContext)

**For orchestrating sequential agent calls:**

```go
// Create context
tc := agent.NewTaskContext("task_123", "Research and write report")

// Task 1: Research
result1 := callAgent("researcher", "Find recent AI advances")
tc.SetResult("task_1", result1)

// Task 2: Analyze (using task 1 output)
context := tc.BuildContextForTask("task_2", plan)
result2 := callAgent("analyzer", task, context)
tc.SetResult("task_2", result2)

// Task 3: Write (using task 1 and 2 outputs)
context := tc.BuildContextForTask("task_3", plan)
result3 := callAgent("writer", task, context)
```

---

## System Prompt Layers

**Runtime builds system prompt in this order:**

1. **Personality** - from config
2. **Core Prompt** - from config
3. **Rules** - from config
4. **Memories** - retrieved from memStore based on user input
5. **User Profile** - loaded from ~/.ironclaw/memory/user/
6. **Skills** - from skillMgr (matched to user input)
7. **Available Agents** - injected by agentMgr ← **This tells LLM about sub-agents!**

**Example layer 7:**
```
## Available Agents

You can delegate tasks to specialized agents using the corresponding agent_* tools.
Each agent runs independently with its own tool set and iteration budget.

- **agent_researcher**: Searches the web and compiles findings [tags: research]
- **agent_analyzer**: Analyzes data and extracts insights [tags: analysis]
```

---

## Integration Checklist

✅ **Gateway loads agent specs:**
```go
agentMgr := agent.NewAgentManager(provider, sessions, db, memStore, tools, cfg.Agent, cfg.LLM)
agentMgr.LoadDir(userdir.AgentsDir())       // ~/.IronClaw/agents/
agentMgr.RegisterAll(tools)                  // Register as agent_* tools
```

✅ **Runtime has access to agent manager:**
```go
runtime.SetAgentManager(agentMgr)           // For prompt injection
```

✅ **Memory shared with sub-agents:**
```go
runtime.SetMemoryStore(memStore)            // In AgentTool.Execute()
```

✅ **LLM provider available:**
```go
// Used by both orchestrator and sub-agents
provider := agent.NewClaudeProvider(apiKey, model, baseURL)
```

---

## Troubleshooting

### Problem: Sub-agent can call other agents
**Solution:** Check your whitelist in agent spec. Remove `agent_*` names if present (they're auto-excluded anyway).

### Problem: "circuit breaker open: agent is failing"
**Solution:** Agent timed out or errored 3 times. Check:
- Agent timeout setting (spec.timeout)
- Agent system prompt clarity
- Available tools in whitelist
- LLM API status

### Problem: Memory not shared
**Solution:** Verify:
- Gateway calls `runtime.SetMemoryStore(memStore)`
- AgentTool calls `subRuntime.SetMemoryStore(a.memStore)`
- memStore is not nil

### Problem: Agent making unexpected tool calls
**Solution:** Add tool whitelist to spec:
```yaml
tools:
  - http
  - file_read
```

---

## Files to Know

| File | Lines | Purpose |
|------|-------|---------|
| `agent_tool.go` | 286 | AgentTool implementation + capture channel |
| `agent_manager.go` | 181 | Load specs, register tools |
| `spec.go` | 87 | AgentSpec definition |
| `runtime.go` | 414 | Main agent loop (reused for sub-agents) |
| `circuit_breaker.go` | 108 | Failure protection |
| `task_context.go` | 105 | Multi-agent coordination |
| `tool/tool.go` | 139 | Tool interface + Registry |

---

## Example: Complete Sub-Agent Definition

```yaml
# ~/.IronClaw/agents/data_analyst.yaml
name: data_analyst
description: Analyzes data files and generates statistical insights
system_prompt: |
  You are a data analysis expert. When given a task:
  1. Use bash to explore data structure
  2. Use file_read to examine content
  3. Perform statistical analysis
  4. Provide clear insights and recommendations
model: claude-3-5-sonnet-20241022
max_tokens: 8000
max_iterations: 5
timeout: 180s
tools:
  - bash
  - file_read
  - file_write
requires_approval: false
tags:
  - analysis
  - data
  - csv
```

---

## Next Steps

1. **Define agents** in `~/.IronClaw/agents/`
2. **Test with orchestrator** - LLM will see agent descriptions in prompt
3. **Monitor** circuit breaker states and memory sharing
4. **Optimize** tool whitelists based on agent needs

For advanced features (remote agents, cognitive mode, RL), see CLAUDE.md Phase 3 notes.
