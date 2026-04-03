# IronClaw Subagent Architecture - Complete Documentation

## Overview

IronClaw implements a sophisticated **subagent system** that allows orchestrator agents to delegate specialized tasks to specialized child agents. This creates a multi-agent collaboration framework where:

- **Orchestrator agents** (main runtime) can call `agent_*` tools
- **Subagents** (temporary Runtime instances) execute with scoped tool sets and custom configurations
- **Task context** and **knowledge sharing** enable pipeline-style workflows
- **Circuit breaker pattern** prevents cascading failures
- **Memory stores** persist across agent invocations

---

## Core Architecture

### 1. Agent Tool (`internal/agent/agent_tool.go`)

**`AgentTool` is the bridge between the agent system and the tool system.**

```
Orchestrator LLM
    ↓ (makes tool call)
AgentTool.Execute()
    ├─ Validates input
    ├─ Creates scoped tool registry
    ├─ Creates temporary Runtime
    ├─ Runs Runtime.HandleMessage()
    ├─ Captures output via captureChannel
    └─ Returns Result
```

#### Key Components:

**Input Schema:**
```go
type agentToolInput struct {
    Task    string `json:"task"`        // The task to delegate
    Context string `json:"context,omitempty"` // Context from previous tasks
}
```

**AgentTool Structure:**
```go
type AgentTool struct {
    spec     *AgentSpec              // Agent specification
    provider Provider                // LLM provider reference
    sessions *session.Manager        // Session persistence
    db       *store.DB               // Database access
    memStore memory.Store            // Shared memory store
    tools    *tool.Registry          // Parent tool registry (for scoping)
    cfg      config.AgentConfig      // Config (with spec overrides)
    llmCfg   config.LLMConfig        // LLM config (with spec overrides)
    breaker  *CircuitBreaker         // Failure circuit breaker
}
```

#### Execution Flow:

1. **Circuit Breaker Check** - Prevents cascading failures
2. **Input Validation** - Parses JSON input, validates required fields
3. **Timeout Setup** - Applies spec timeout (default 120s) via context.WithTimeout
4. **Scoped Registry Building** - Creates isolated tool set via `buildScopedRegistry()`
5. **Config Merging** - Applies spec overrides:
   - `MaxIterations` from spec
   - `SystemPrompt` from spec (if provided)
   - `Model` from spec (if provided)
   - `MaxTokens` from spec (if > 0)
6. **Temporary Runtime Creation** - `NewRuntime()` with scoped tools and merged config
7. **Message Building** - Combines task + optional context
8. **Channel Capture** - Uses `captureChannel` to collect output
9. **Execution** - Calls `subRuntime.HandleMessage()`
10. **Output Collection** - Returns final message from capture buffer

#### Tool Scoping:

```go
func (a *AgentTool) buildScopedRegistry() *tool.Registry {
    scoped := tool.NewRegistry()
    allTools := a.tools.All()

    for _, t := range allTools {
        name := t.Name()

        // Always exclude agent_* tools (prevent recursion)
        if strings.HasPrefix(name, "agent_") {
            continue
        }

        // If whitelist specified, only include listed tools
        if len(a.spec.Tools) > 0 {
            if !contains(a.spec.Tools, name) {
                continue
            }
        }

        scoped.Register(t)
    }

    return scoped
}
```

**Important:** `agent_*` tools are always excluded to prevent infinite recursion.

---

### 2. Agent Specification (`internal/agent/spec.go`)

**`AgentSpec` defines the capabilities and constraints of a subagent.**

```go
type AgentSpec struct {
    Name            string          // Unique agent name (becomes agent_NAME tool)
    Description     string          // What this agent does
    SystemPrompt    string          // Custom system prompt override
    Model           string          // Optional LLM model override
    MaxTokens       int             // Optional max_tokens override
    MaxIterations   int             // Iteration limit (default 5)
    Tools           []string        // Tool whitelist (empty = all, agent_* excluded)
    Tags            []string        // Routing tags for semantic matching
    Mode            string          // "simple" (default) | "cognitive"
    Timeout         duration        // Execution timeout (default 120s)
    RequiresApproval bool           // Require user approval before execution
    MaxRetries      int             // Retry count on failure
    Remote          *RemoteAgentConfig // Phase 3: A2A remote agent (reserved)
}
```

**Example YAML spec:**
```yaml
# agents/data_analyst.yaml
name: data_analyst
description: Analyzes CSV files and generates insights
system_prompt: |
  You are a data analysis expert. When given a CSV file,
  analyze its structure, identify patterns, and provide insights.
model: claude-3-5-sonnet-20241022
max_iterations: 5
max_tokens: 8000
timeout: 180s
tools:
  - bash           # bash command execution
  - file_read      # read files
  - file_write     # write results
requires_approval: false
```

---

### 3. Agent Manager (`internal/agent/agent_manager.go`)

**`AgentManager` loads agent specs and registers them as tools.**

```go
type AgentManager struct {
    mu       sync.RWMutex
    specs    []*AgentSpec
    provider Provider
    sessions *session.Manager
    db       *store.DB
    memStore memory.Store
    tools    *tool.Registry
    cfg      config.AgentConfig
    llmCfg   config.LLMConfig
}
```

#### Key Methods:

**Load Agent Specifications:**
```go
// LoadDir loads all YAML files from a directory
func (m *AgentManager) LoadDir(dir string) error
    // Reads ~/.IronClaw/agents/*.yaml files
    // Parses YAML with environment variable expansion
    // Validates specs and adds to manager

// Add adds an inline spec (for programmatic definition)
func (m *AgentManager) Add(spec *AgentSpec) error
```

**Register as Tools:**
```go
// RegisterAll creates AgentTool instances for each spec
// and registers them in the tool registry (making them available to orchestrator)
func (m *AgentManager) RegisterAll(registry *tool.Registry)
```

**Prompt Injection:**
```go
// BuildPromptSection generates a section describing available agents
// for injection into the orchestrator's system prompt
func (m *AgentManager) BuildPromptSection() string
```

Output example:
```
## Available Agents

You can delegate tasks to specialized agents using the corresponding agent_* tools.
Each agent runs independently with its own tool set and iteration budget.

- **agent_data_analyst**: Analyzes CSV files and generates insights [tags: analysis, data]
- **agent_researcher**: Searches the web and synthesizes findings [tags: research]
```

---

### 4. Capture Channel (`internal/agent/agent_tool.go`)

**`captureChannel` implements `channel.Channel` to collect sub-agent output in-memory.**

```go
type captureChannel struct {
    mu       sync.Mutex
    messages []string
}
```

**Why separate from regular channels?**
- Sub-agents should NOT send to Telegram/TUI (external channels)
- Output must be captured and returned as a tool result
- Only final message is extracted for efficiency

#### Methods:

```go
// Collect returns last message from buffer (sub-agent's final response)
func (c *captureChannel) Collect() string {
    // Returns c.messages[len(c.messages)-1]
    // Intermediate messages are tool invocations/progress updates
}

// Finish appends final streamed message
func (u *captureUpdater) Finish(text string) error {
    // Appends to c.messages and returns nil
}

// Update ignores intermediate streaming updates
func (u *captureUpdater) Update(_ string) error {
    // Ignores intermediate updates
}
```

---

### 5. Runtime Integration (`internal/agent/runtime.go`)

**The `Runtime` is reused for subagents with temporary instances.**

```go
type Runtime struct {
    provider       Provider
    tools          *tool.Registry
    sessions       *session.Manager
    db             *store.DB
    cfg            config.AgentConfig
    llmCfg         config.LLMConfig
    approvalFunc   ApprovalFunc
    memStore       memory.Store
    skillMgr       *skill.Manager
    agentMgr       *AgentManager        // For nested agent calls
    compressor     *memory.IncrementalCompressor
    // ... other fields
}
```

**Key setter methods for subagent setup:**
```go
func (r *Runtime) SetMemoryStore(s memory.Store)
func (r *Runtime) SetSkillManager(m *skill.Manager)
func (r *Runtime) SetAgentManager(m *AgentManager)
```

**Subagent execution path:**
1. `AgentTool.Execute()` creates temporary Runtime
2. Calls `subRuntime.SetMemoryStore(a.memStore)` to share memory
3. Calls `subRuntime.HandleMessage(ctx, captureChannel, msg)`
4. Runtime processes message through agent loop
5. Output captured by captureChannel

---

### 6. Circuit Breaker (`internal/agent/circuit_breaker.go`)

**Prevents cascading failures from recurring sub-agent errors.**

```go
type CircuitBreaker struct {
    mu            sync.Mutex
    state         CircuitState     // Closed | Open | HalfOpen
    failureCount  int
    successCount  int
    threshold     int              // Default: 3 failures
    resetAfter    time.Duration    // Default: 60s
    lastFailTime  time.Time
    halfOpenLimit int              // Default: 1
}
```

#### State Machine:

```
┌─────────────┐
│  CLOSED     │  Normal operation
│  (accepts)  │
└──────┬──────┘
       │ 3 failures
       ▼
┌─────────────┐
│   OPEN      │  Rejecting requests
│  (rejects)  │  "circuit breaker open: agent is failing"
└──────┬──────┘
       │ After 60s timeout
       ▼
┌─────────────┐
│  HALF-OPEN  │  Testing recovery, allows 1 request
│ (1 request) │
└──────┬──────┘
       │
       ├─ Success ──→ CLOSED
       └─ Failure ──→ OPEN
```

**Used in AgentTool.Execute():**
```go
if err := a.breaker.Allow(); err != nil {
    return tool.Result{Error: err.Error()}, nil
}

// ... execute ...

if success {
    a.breaker.RecordSuccess()
} else {
    a.breaker.RecordFailure()
}
```

---

### 7. Task Context (`internal/agent/task_context.go`)

**Enables multi-agent collaboration and pipeline workflows.**

```go
type TaskContext struct {
    mu         sync.RWMutex
    ID         string
    Goal       string
    Results    map[string]SubAgentResult  // task_id → result
    SharedData map[string]string          // arbitrary KV store
}

type SubAgentResult struct {
    AgentName  string
    Output     string
    Error      string
    DurationMs int64
}
```

#### Usage Pattern:

```
TaskContext
    │
    ├─ Task 1: agent_researcher
    │   └─ Output: "Search findings on X"
    │
    ├─ Task 2: agent_analyzer (depends on Task 1)
    │   ├─ GetResult("task_1") → gets researcher output
    │   └─ Analyzes findings
    │
    └─ Task 3: agent_writer (depends on Task 1, 2)
        └─ GetResult("task_1"), GetResult("task_2")
           └─ Writes final report
```

#### Key Methods:

```go
// Store result from a subtask
func (tc *TaskContext) SetResult(taskID string, result SubAgentResult)

// Retrieve result from a subtask
func (tc *TaskContext) GetResult(taskID string) (SubAgentResult, bool)

// Set/get arbitrary shared data
func (tc *TaskContext) SetShared(key, value string)
func (tc *TaskContext) GetShared(key string) (string, bool)

// Build context string for a dependent task
func (tc *TaskContext) BuildContextForTask(taskID string, plan *TaskPlan) string
    // Returns: "Context from previous tasks:\n\n--- Task X (agent_Y) ---\n<output>\n..."
```

---

### 8. Tool Registry (`internal/tool/tool.go`)

**Holds all available tools, including dynamically created `agent_*` tools.**

```go
type Registry struct {
    mu    sync.RWMutex
    tools map[string]Tool  // name → Tool implementation
}
```

#### Agent Tool Registration:

```go
// Gateway initialization (gateway/gateway.go):

// 1. Create AgentManager
agentMgr := agent.NewAgentManager(provider, sessions, db, memStore, tools, cfg.Agent, cfg.LLM)

// 2. Load specs from ~/.IronClaw/agents/*.yaml
agentMgr.LoadDir(userdir.AgentsDir())

// 3. Register all agent specs as tools
agentMgr.RegisterAll(tools)  // Creates AgentTool for each spec, registers as agent_NAME
```

**Tool names created:**
- `agent_data_analyst` (from spec name "data_analyst")
- `agent_researcher` (from spec name "researcher")
- etc.

---

## Data Flow: Step-by-Step Example

### Scenario: Orchestrator delegates research task

```
1. User Message
   └─ "Research recent advances in AI and summarize findings"

2. Orchestrator Agent Loop
   ├─ Receives message
   ├─ Builds system prompt (includes Available Agents section)
   ├─ Calls LLM with available tools including "agent_researcher"
   └─ LLM decides: "This needs research, call agent_researcher"

3. Tool Call Generated by LLM
   {
       "tool_call_id": "tc_123",
       "tool_name": "agent_researcher",
       "tool_input": {
           "task": "Research recent advances in AI from 2024-2026",
           "context": ""
       }
   }

4. AgentTool.Execute() invoked with tool_input
   ├─ Circuit breaker check → PASS
   ├─ Parse JSON input
   ├─ Build scoped registry
   │   ├─ Include: bash, file_read, file_write, http
   │   └─ Exclude: agent_* (all agent tools)
   ├─ Merge spec config
   │   ├─ MaxIterations: 5
   │   ├─ SystemPrompt: "You are a research expert..."
   │   └─ Model: claude-3-5-sonnet (if overridden)
   ├─ Create temporary Runtime
   │   ├─ Provider: LLM provider
   │   ├─ Tools: scoped registry
   │   ├─ Sessions: shared
   │   ├─ Memory: shared memStore
   │   └─ Config: merged
   ├─ Create message
   │   ├─ Text: "Research recent advances in AI from 2024-2026"
   │   └─ Channel: "agent" (synthetic)
   ├─ Create captureChannel
   └─ Call subRuntime.HandleMessage(ctx, captureChannel, msg)

5. Sub-agent Runtime Loop (Iteration 1)
   ├─ Add user message to session
   ├─ Build system prompt
   │   ├─ Personality: (from config)
   │   ├─ System prompt: (spec override or config)
   │   ├─ Memories: (retrieve relevant from memStore)
   │   ├─ User context: (load profile)
   │   └─ Skills: (load matching skills)
   ├─ Request LLM completion
   │   ├─ Model: claude-3-5-sonnet
   │   ├─ Tools: [bash, file_read, file_write, http]
   │   └─ Messages: [system, user message]
   ├─ LLM decides: "I'll use http tool to fetch research"
   └─ Generate tool call

6. Tool Call Execution (Iteration 1)
   ├─ Tool: http
   ├─ Input: GET https://example.com/ai-advances
   └─ Result: "Recent AI advances in 2024-2026: ..."

7. Sub-agent Runtime Loop (Iteration 2)
   ├─ Add tool result to session
   ├─ Request LLM completion
   │   ├─ Messages: [system, user msg, tool_result]
   │   └─ LLM decides: "Now I'll synthesize findings and respond"
   ├─ Generate final response
   │   └─ Text: "Based on my research, here are the key AI advances:..."
   └─ No tool calls → exit loop

8. Output Capture
   └─ captureChannel.Collect() returns final message

9. Result Returned to Orchestrator
   {
       "output": "Based on my research, here are the key AI advances:...",
       "error": ""
   }

10. Orchestrator Continues
    ├─ Add tool result to session
    ├─ Request LLM (next iteration)
    │   └─ LLM generates final response with research findings
    └─ Send response to user
```

---

## Integration Points

### 1. Gateway Initialization (gateway/gateway.go)

```go
func New(cfg *config.Config) (*Gateway, error) {
    // ... create DB, sessions, tools, etc ...

    // Create agent manager
    agentMgr := agent.NewAgentManager(
        provider, sessions, db, memStore, tools,
        cfg.Agent, cfg.LLM,
    )

    // Load agent specs from ~/.IronClaw/agents/
    agentMgr.LoadDir(userdir.AgentsDir())

    // Register all specs as agent_* tools
    agentMgr.RegisterAll(tools)

    // Attach to runtime for prompt injection
    runtime.SetAgentManager(agentMgr)

    // Store for later use
    g.agentMgr = agentMgr
}
```

### 2. Runtime System Prompt Building (runtime.go)

```go
func (r *Runtime) buildSystemPrompt(ctx context.Context, userText string) string {
    var sb strings.Builder

    // 1. Personality
    sb.WriteString(r.cfg.Personality)

    // 2. Core system prompt
    sb.WriteString(r.cfg.SystemPrompt)

    // 3. Rules
    sb.WriteString(r.cfg.PersistentRules)

    // 4. Relevant memories
    if r.memStore != nil {
        results, _ := r.memStore.Search(ctx, ...)
        // Append memory snippets
    }

    // 5. User profile
    if r.memoryBaseDir != "" {
        profileContent, _ := memory.LoadUserProfile(...)
        // Append user context
    }

    // 6. Skills
    if r.skillMgr != nil {
        section := r.skillMgr.BuildPromptSection(userText)
        // Append skill descriptions
    }

    // 7. Available Agents ← INJECTED BY AGENT MANAGER
    if r.agentMgr != nil {
        section := r.agentMgr.BuildPromptSection()
        // Append: "## Available Agents\n- agent_researcher: ...\n- agent_analyzer: ..."
        sb.WriteString(section)
    }

    return sb.String()
}
```

### 3. Tool Execution (runtime.go)

```go
func (r *Runtime) executeTools(ctx context.Context, ch channel.Channel, ...) {
    // Standard tool execution loop
    for _, tc := range toolCalls {
        tool, err := r.tools.Get(tc.Name)  // Gets agent_* tool
        if err != nil {
            // handle error
        }

        // For agent_* tools:
        // → AgentTool.Execute() creates temp Runtime
        // → Runs sub-agent
        // → Returns output as Result

        // Execute and collect result
    }
}
```

---

## Configuration

### Agent Config (config.yaml)

```yaml
agent:
  mode: simple                    # simple or cognitive
  max_iterations: 10
  system_prompt: |
    You are a helpful AI assistant...
  personality: |
    You are curious and helpful...
  persistent_rules: |
    Follow these rules:
    - Be concise
    - Prioritize user needs
  compression:
    strategy: layered
```

### Agent Specs (agents/*.yaml)

```yaml
name: data_analyst
description: Analyzes data and generates insights
system_prompt: |
  You are a data analysis expert specializing in CSV analysis.
  When given a CSV file:
  1. Load and parse it
  2. Identify structure and data types
  3. Perform statistical analysis
  4. Generate insights
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

## Key Design Patterns

### 1. Scoped Tooling

```
Orchestrator Runtime
├─ Tools: [bash, file_read, file_write, http, agent_researcher, agent_analyzer]
│
└─ Delegates to agent_researcher
   └─ Sub-agent Runtime
      ├─ Tools: [bash, file_read, file_write, http]
      │          ↑ whitelisted, no agent_* tools
      └─ Runs independently with isolated tool set
```

**Benefit:** Prevents unintended tool usage, maintains security boundaries.

### 2. Spec Overrides

```
Base Config           Spec Override          Final Config
────────────────────────────────────────────────────────
model: gpt-4          model: sonnet           → sonnet
max_tokens: 4096      max_tokens: 8000       → 8000
max_iterations: 5     max_iterations: 3      → 3
system_prompt: ...    system_prompt: ...     → spec's prompt
```

**Benefit:** Different agents can have different LLM configurations.

### 3. Memory Sharing

```
Orchestrator
├─ memStore: persistent memory
│   └─ shared with all sub-agents
│
└─ agent_researcher
   ├─ Reads from memStore
   ├─ Adds new facts to memStore
   └─ Next agent reads updated facts
```

**Benefit:** Sub-agents contribute to and benefit from persistent knowledge.

### 4. Failure Isolation

```
circuit_breaker: Closed
│
└─ agent_researcher fails 3 times
   │
   └─ circuit_breaker: Open
      └─ Next call: rejected immediately (fail fast)
      └─ After 60s: try half-open (test recovery)
```

**Benefit:** Prevents resource exhaustion from repeatedly calling failing agents.

### 5. Recursive Prevention

```go
// In buildScopedRegistry()
if strings.HasPrefix(name, "agent_") {
    continue  // Always skip agent_* tools
}
```

**Benefit:** Prevents agent_researcher from calling agent_researcher (infinite loop).

---

## Best Practices

### 1. Agent Specification

✅ **DO:**
- Provide clear, specific descriptions
- Specify tool whitelist if agent only needs subset
- Set appropriate timeout based on complexity
- Use tags for semantic routing

❌ **DON'T:**
- Create agents that are too similar (confuses orchestrator)
- Set extremely large max_iterations (wastes tokens)
- Give all tools to every agent (security/efficiency)
- Use agent names that are too generic

### 2. System Prompts

✅ **DO:**
- Be specific about the agent's role and expertise
- Include examples of expected behavior
- Specify output format if needed
- Mention relevant tool capabilities

❌ **DON'T:**
- Copy orchestrator prompt into subagent spec
- Include references to agent names in the prompt
- Make prompts too long (use config instead)

### 3. Task Context

✅ **DO:**
- Use when orchestrator explicitly breaks task into substeps
- Include task IDs in all results
- Document dependencies clearly

❌ **DON'T:**
- Treat TaskContext as general state store
- Expect sub-agents to automatically use shared data
- Store sensitive information in SharedData

### 4. Error Handling

✅ **DO:**
- Monitor circuit breaker states
- Log sub-agent failures with context
- Implement retry logic at orchestrator level
- Use RequiresApproval for high-risk agents

❌ **DON'T:**
- Cascade failures without circuit breaking
- Call failing agents repeatedly
- Ignore error fields in tool Results

---

## Example: Multi-Agent Pipeline

### Workflow: "Research and write a report on X"

**Agent Specs:**

```yaml
# agents/researcher.yaml
name: researcher
description: Searches the web and compiles research findings
tools: [http, file_read]
max_iterations: 5

# agents/analyzer.yaml
name: analyzer
description: Analyzes research findings and extracts key insights
tools: [bash, file_read, file_write]
max_iterations: 4

# agents/writer.yaml
name: writer
description: Writes polished reports
tools: [file_write, file_read]
max_iterations: 3
```

**Orchestrator Logic:**

1. User: "Research AI advances and write a report"

2. Orchestrator decides:
   - Task 1: Call agent_researcher with "Find recent AI advances"
   - Task 2: Call agent_analyzer with research output
   - Task 3: Call agent_writer with analysis

3. TaskContext tracks:
   - task_1 result: "Found X, Y, Z advances"
   - task_2 result: "Key insights: A, B, C"
   - task_3 result: "Final report..."

4. Each agent:
   - Receives previous results via `context` field
   - Uses shared memStore to persist learnings
   - Performs specialized work
   - Returns structured output

---

## Troubleshooting

### Problem: "circuit breaker open: agent is failing"

**Causes:**
- Agent timeout exceeded
- LLM API error
- Tool execution error

**Solution:**
- Check agent logs for specific error
- Increase timeout if legitimate
- Review system prompt for issues
- Monitor LLM API status

### Problem: Agent making unintended tool calls

**Causes:**
- Tool whitelist not specified
- Agent can access unexpected tools

**Solution:**
- Add `tools: [...]` whitelist to spec
- Test with limited tool set first
- Review available tools in scoped registry

### Problem: Sub-agent output truncated

**Causes:**
- Output exceeds LLM max_tokens
- Streaming message interrupted

**Solution:**
- Increase max_tokens in spec
- Split large tasks into smaller subtasks
- Use file_write tool for large outputs

### Problem: Memory not shared between agents

**Causes:**
- memStore not set on runtime
- Memory not saved with correct scope

**Solution:**
- Ensure Gateway.SetMemoryStore() called
- Check memory.Save() completing successfully
- Verify memory retrieval in buildSystemPrompt()

---

## Future Enhancements (Phase 3)

### Remote Agents (A2A Protocol)

```yaml
name: external_research
description: Uses external research API
remote:
  url: https://external-agent.example.com/execute
  agent_card: https://external-agent.example.com/agent.json
  auth_type: bearer
  auth_token: ${EXTERNAL_AGENT_TOKEN}
  timeout: 30s
```

When `spec.Remote != nil`:
- AgentTool will call remote endpoint via A2A protocol
- Instead of creating local Runtime
- Same tool.Result interface (compatible)

### Agent-to-Agent Learning

- Sub-agents contribute to knowledge graph
- Entity extraction from sub-agent outputs
- Relation mining between agent conversations
- Dynamic routing based on learned patterns

### Nested Cognitive Agents

```yaml
name: complex_analyzer
mode: cognitive  # Use 5-phase loop instead of simple
description: Performs complex multi-phase analysis
```

---

## Summary

The IronClaw subagent system provides:

1. **Modular specialization** - Different agents for different domains
2. **Safe isolation** - Scoped tools, circuit breakers, recursive prevention
3. **Knowledge sharing** - Persistent memory across agents
4. **Pipeline workflows** - TaskContext for multi-step processes
5. **Flexible configuration** - Spec overrides for LLM, tools, behavior
6. **Extensibility** - Ready for remote agents, cognitive modes, advanced routing

The architecture uses familiar patterns (factory, adapter, circuit breaker, registry) and integrates cleanly with the existing Runtime infrastructure, allowing orchestrator agents to dynamically spawn specialized sub-agents as needed.

