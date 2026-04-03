# IronClaw Subagent Architecture - Visual Diagrams

## 1. System Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            IronClaw Main Process                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                               │
│  ┌──────────────────────────────┐                                            │
│  │   Gateway (Initialization)   │                                            │
│  ├──────────────────────────────┤                                            │
│  │ • Database                   │                                            │
│  │ • Session Manager            │                                            │
│  │ • Tool Registry              │                                            │
│  │ • AgentManager     ◄─────────┼─── Load agents/ specs from YAML            │
│  │ • LLM Provider                │                                            │
│  │ • Memory Store               │                                            │
│  │ • Skill Manager              │                                            │
│  └──────────────┬───────────────┘                                            │
│                 │                                                             │
│                 ▼                                                             │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │              Tool Registry                                           │   │
│  ├──────────────────────────────────────────────────────────────────────┤   │
│  │  • bash_tool          ◄──── Internal tools                          │   │
│  │  • file_tool                                                        │   │
│  │  • http_tool                                                        │   │
│  │  • browser_tool                                                     │   │
│  │  • agent_researcher   ◄──── Registered dynamically by AgentManager  │   │
│  │  • agent_analyzer                                                   │   │
│  │  • agent_data_analyst                                               │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                 ▲                                                             │
│                 │                                                             │
│  ┌──────────────┴────────────────────────────────────────────────────────┐  │
│  │              Orchestrator Runtime                                     │  │
│  ├────────────────────────────────────────────────────────────────────────┤  │
│  │                                                                        │  │
│  │  HandleMessage(user_message)                                         │  │
│  │  ├─ Build system prompt (includes agent descriptions)               │  │
│  │  ├─ Agent Loop:                                                     │  │
│  │  │  ├─ Call LLM with available tools                               │  │
│  │  │  ├─ LLM decides: "use agent_researcher"                         │  │
│  │  │  ├─ Execute tool: agent_researcher                              │  │
│  │  │  │  └─ AgentTool.Execute() ─────┐                               │  │
│  │  │  └─ Add result to session        │                              │  │
│  │  └─ Continue loop                   │                              │  │
│  │                                      │ (creates                      │  │
│  │  External Channels:                  │  temporary                    │  │
│  │  ├─ Telegram                         │  sub-agent)                   │  │
│  │  ├─ TUI                              │                              │  │
│  │  └─ Webhook                          │                              │  │
│  │                                      │                              │  │
│  └──────────────────────────────────────┼──────────────────────────────┘  │
│                                         │                                  │
└─────────────────────────────────────────┼──────────────────────────────────┘
                                          │
                                          ▼ (temporary, scoped)
                       ┌──────────────────────────────────────┐
                       │   Sub-Agent Runtime (Temporary)      │
                       ├──────────────────────────────────────┤
                       │ • Scoped Tool Registry               │
                       │   - bash, file, http (whitelisted)   │
                       │   - NO agent_* tools                 │
                       │ • Session (shared)                   │
                       │ • Memory (shared)                    │
                       │ • Config (merged from spec)          │
                       │ • Max Iterations: 5                  │
                       │                                      │
                       │  HandleMessage(task_message)         │
                       │  ├─ LLM decides: use http tool       │
                       │  ├─ Execute http tool                │
                       │  ├─ Generate response                │
                       │  └─ Exit (no more tool calls)        │
                       │                                      │
                       └──────┬──────────────────────────────┘
                              │
                              ▼
                       ┌──────────────────────────────┐
                       │   Capture Channel            │
                       ├──────────────────────────────┤
                       │ • Final message buffer       │
                       │ • Returns last message       │
                       │ (discards intermediate)      │
                       └──────┬──────────────────────┘
                              │
                              ▼ tool.Result
                    {output: "Research findings..."}
                              │
                              └─► Orchestrator (continues loop)
```

## 2. Agent Tool Execution Flow

```
AgentTool.Execute(json_input)
│
├─ 1. Circuit Breaker Check
│  └─ if breaker.Open: return Error("circuit breaker open")
│
├─ 2. Parse Input
│  └─ Unmarshal: { "task": "...", "context": "..." }
│
├─ 3. Build Scoped Registry
│  ├─ Start with parent tools: [bash, file, http, agent_*]
│  ├─ Exclude agent_* (prevent recursion)
│  ├─ Apply whitelist (if spec.Tools specified)
│  └─ Result: [bash, file, http] ◄─── Sub-agent tools only
│
├─ 4. Merge Configuration
│  ├─ Start with base config
│  ├─ Override from spec:
│  │  ├─ MaxIterations = 5
│  │  ├─ SystemPrompt = spec's custom prompt
│  │  ├─ Model = sonnet (if specified)
│  │  └─ MaxTokens = 8000 (if > 0)
│  └─ Result: merged config
│
├─ 5. Create Temporary Runtime
│  └─ NewRuntime(provider, scopedTools, sessions, db, mergedCfg, llmCfg)
│
├─ 6. Set Shared Resources
│  ├─ subRuntime.SetMemoryStore(memStore) ◄─── Share persistent memory
│  └─ (optional) SetSkillManager, SetAgentManager
│
├─ 7. Create Capture Channel
│  └─ captureChannel{} (buffer for messages)
│
├─ 8. Build Task Message
│  └─ Text: "Context...\n\nTask: " + spec.SystemPrompt
│
├─ 9. Execute Sub-Agent
│  └─ subRuntime.HandleMessage(ctx, captureChannel, msg)
│      └─ Runs agent loop (iterations 1..N until exit)
│
├─ 10. Collect Output
│   └─ output = captureChannel.Collect() ◄─── Last message only
│
├─ 11. Record Result
│  ├─ if success: breaker.RecordSuccess()
│  └─ if error: breaker.RecordFailure()
│
└─ 12. Return Result
   └─ tool.Result{Output: output, Error: error_msg}
      (as JSON for LLM)
```

## 3. Runtime System Prompt Building

```
buildSystemPrompt(userText)
│
├─ 1. Personality Section (from config)
│  └─ "## Personality\n..."
│
├─ 2. System Prompt (from config)
│  └─ "You are a helpful AI..."
│
├─ 3. Persistent Rules (from config)
│  └─ "## Rules\n- Be concise\n..."
│
├─ 4. Relevant Memories (from memStore)
│  └─ memStore.Search(userText, limit=5)
│      ├─ FTS5 BM25 search
│      ├─ Vector similarity search
│      └─ RRF fusion ranking
│
├─ 5. User Context (from memory files)
│  └─ memory.LoadUserProfile(memoryBaseDir, userID)
│
├─ 6. Available Skills (from skillMgr)
│  └─ skillMgr.BuildPromptSection(userText)
│
├─ 7. Available Agents ◄─────────────────── INJECTED BY AGENT MANAGER
│  │
│  └─ r.agentMgr.BuildPromptSection()
│     ├─ "## Available Agents\n"
│     ├─ "- agent_researcher: Searches web for findings"
│     ├─ "- agent_analyzer: Analyzes data..."
│     └─ "- agent_writer: Writes polished reports..."
│
└─ Result: Multi-layer system prompt
   (agent descriptions enable LLM to select agents)
```

## 4. Tool Scoping: Orchestrator vs Sub-Agent

```
┌─────────────────────────────────────────────────────────────┐
│                   Orchestrator Runtime                      │
│  Available Tools:                                           │
│  ├─ bash_tool                                              │
│  ├─ file_tool                                              │
│  ├─ http_tool                                              │
│  ├─ agent_researcher      ◄─── Can call sub-agents        │
│  ├─ agent_analyzer                                         │
│  └─ agent_writer                                           │
│                                                             │
│  User: "Research and write report"                         │
│  LLM selects: agent_researcher tool                        │
└──────────────────┬──────────────────────────────────────────┘
                   │
                   ▼ AgentTool.Execute()
                   │
    ┌──────────────────────────────────────────┐
    │  Sub-Agent Runtime (agent_researcher)    │
    │  Available Tools:                        │
    │  ├─ bash_tool                            │
    │  ├─ file_tool                            │
    │  ├─ http_tool                            │
    │  └─ (NO agent_* tools here!)             │
    │      ↑ Always excluded (recursion guard) │
    │                                          │
    │  Task: "Research recent AI advances"    │
    │  LLM selects: http_tool                  │
    │  Executes: Fetch https://...             │
    └──────────────────────────────────────────┘
```

## 5. Circuit Breaker State Machine

```
┌─────────────────────────────────────────────────────────────┐
│                     CIRCUIT BREAKER                         │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌────────────────────────────────────────────────┐        │
│  │             STATE: CLOSED                      │        │
│  ├────────────────────────────────────────────────┤        │
│  │ • Normal operation                             │        │
│  │ • Accept all requests                          │        │
│  │ • Track failures (failureCount)                │        │
│  │ • Reset to 0 on success                        │        │
│  └──────────────────┬─────────────────────────────┘        │
│                    │                                         │
│                    │ failureCount >= 3                      │
│                    ▼                                         │
│  ┌────────────────────────────────────────────────┐        │
│  │             STATE: OPEN                        │        │
│  ├────────────────────────────────────────────────┤        │
│  │ • Reject all requests immediately              │        │
│  │ • Error: "circuit breaker open"                │        │
│  │ • Wait: resetAfter (default 60s)               │        │
│  │ • Record: lastFailTime                         │        │
│  └──────────────────┬─────────────────────────────┘        │
│                    │                                         │
│                    │ time.Since(lastFailTime) > resetAfter │
│                    ▼                                         │
│  ┌────────────────────────────────────────────────┐        │
│  │           STATE: HALF-OPEN                     │        │
│  ├────────────────────────────────────────────────┤        │
│  │ • Test recovery: allow 1 request               │        │
│  │ • Track: successCount                          │        │
│  │ • If success: successCount++                   │        │
│  │ • If failure: go to OPEN immediately           │        │
│  └──────────┬───────────────────────┬─────────────┘        │
│             │                       │                       │
│             │ success              │ failure                │
│             ▼                       ▼                       │
│          CLOSED ◄──────────────────► OPEN                 │
│                                                              │
└─────────────────────────────────────────────────────────────┘

Usage in AgentTool:
    err := breaker.Allow()
    if err != nil {
        return Result{Error: err.Error()}
    }
    
    result := execute()
    
    if result.Error == "" {
        breaker.RecordSuccess()
    } else {
        breaker.RecordFailure()
    }
```

## 6. Multi-Agent Pipeline (Task Context)

```
┌─────────────────────────────────────────────────────────────┐
│                    Orchestrator                             │
│  User: "Research AI and write a report"                    │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
        ┌─────────────────────────────────────┐
        │      TaskContext Created             │
        ├─────────────────────────────────────┤
        │ ID: "ctx_123"                       │
        │ Goal: "Research AI and write..."    │
        │ Results: {}                         │
        │ SharedData: {}                      │
        └─────────────────────────────────────┘
                          │
                ┌─────────┴─────────┐
                │                   │
                ▼                   ▼
        ┌───────────────┐    ┌───────────────┐
        │  Task 1       │    │  Task 2       │
        │               │    │               │
        │ agent_        │    │ agent_        │
        │ researcher    │    │ analyzer      │
        │               │    │               │
        │ Call with     │    │ Call with     │
        │ "Research     │    │ task_1 output │
        │  AI advances" │    │ + new task    │
        │               │    │               │
        │ Returns:      │    │ Returns:      │
        │ "Found X, Y"  │    │ "Key points"  │
        └───────┬───────┘    └───────┬───────┘
                │ SetResult("task_1")│ SetResult("task_2")
                │                   │
                ▼                   ▼
        ┌──────────────────────────────────┐
        │  TaskContext Updated              │
        ├──────────────────────────────────┤
        │ Results:                         │
        │  "task_1": {output: "Found X,Y"} │
        │  "task_2": {output: "Key pts"}   │
        └───────────────┬──────────────────┘
                        │
                        ▼
                ┌───────────────┐
                │  Task 3       │
                │               │
                │ agent_writer  │
                │               │
                │ Call with     │
                │ BuildContext  │
                │ (task_1, 2)   │
                │               │
                │ Returns:      │
                │ "Final report"│
                └───────────────┘
```

## 7. Gateway Initialization Sequence

```
Gateway.New(cfg)
│
├─ 1. Open Database
│  └─ store.DB (SQLite, WAL mode)
│
├─ 2. Create Session Manager
│  └─ session.Manager (with DB)
│
├─ 3. Create Tool Registry
│  └─ tool.Registry (empty)
│
├─ 4. Register Internal Tools
│  ├─ tools.Register(bash_tool)
│  ├─ tools.Register(file_tool)
│  ├─ tools.Register(http_tool)
│  └─ tools: [bash, file, http]
│
├─ 5. Create LLM Provider
│  └─ agent.NewClaudeProvider(cfg.LLM)
│
├─ 6. Create Orchestrator Runtime
│  └─ agent.NewRuntime(provider, tools, ...)
│
├─ 7. Create AgentManager ◄─────────────────┐
│  └─ agent.NewAgentManager(provider, ...)  │ (orchestrator)
│                                            │
├─ 8. Load Agent Specs                      │
│  └─ agentMgr.LoadDir("~/.IronClaw/agents/")
│      ├─ Reads: data_analyst.yaml
│      ├─ Reads: researcher.yaml
│      └─ specs: [AgentSpec, ...]
│
├─ 9. Register Agent Tools ◄────────────────┤ (key integration)
│  └─ agentMgr.RegisterAll(tools)
│      ├─ For each spec:
│      │  ├─ Create AgentTool(spec)
│      │  └─ tools.Register("agent_data_analyst")
│      │  └─ tools.Register("agent_researcher")
│      └─ tools: [bash, file, http, agent_*, agent_*, ...]
│
├─ 10. Attach AgentManager to Runtime
│  └─ runtime.SetAgentManager(agentMgr)
│      (so runtime can build agent descriptions in prompt)
│
└─ 11. Initialize Channels (Telegram, TUI)
   └─ channels: [telegram, tui, webhook]
```

