# IronClaw Architecture Overview

## System Architecture

```
                        ┌─────────────────────────────────────┐
                        │     ENTRY POINT                     │
                        │  cmd/ironclaw/main.go               │
                        │  - start                            │
                        │  - tui                              │
                        │  - skill                            │
                        │  - memory                           │
                        └─────────────┬───────────────────────┘
                                      │
                        ┌─────────────▼───────────────────────┐
                        │      GATEWAY                        │
                        │ (Central Orchestrator)              │
                        │ internal/gateway/gateway.go         │
                        └─────────────┬───────────────────────┘
                                      │
        ┌─────────────────────────────┼─────────────────────────────┐
        │                             │                             │
        ▼                             ▼                             ▼
   ┌─────────────┐           ┌──────────────────┐         ┌──────────────┐
   │   CONFIG    │           │    DATABASE      │         │   SESSION    │
   │             │           │    (SQLite)      │         │   MANAGER    │
   │ yaml → Go   │           │                  │         │              │
   │ structs     │           │ Memory index     │         │ Per-channel  │
   │             │           │ Knowledge base   │         │ conversation │
   │ Feature     │           │ Scheduled tasks  │         │ history      │
   │ flags:      │           │ Hook audit log   │         │              │
   │ - memory    │           │ RL experiences   │         │ Message list │
   │ - knowledge │           │                  │         │ + metadata   │
   │ - skills    │           │ Index tables:    │         │              │
   │ - cognitive │           │ - memory_index   │         └──────────────┘
   │ - rl        │           │ - knowledge_*    │
   │ - scheduler │           │ - scheduled_*    │
   │             │           │ - hook_audit     │
   └─────────────┘           └──────────────────┘
                                      │
        ┌─────────────────────────────┼─────────────────────────────────┐
        │                             │                                 │
        ▼                             ▼                                 ▼
   ┌─────────────┐           ┌──────────────────┐         ┌──────────────┐
   │    TOOL     │           │     AGENT        │         │   MEMORY     │
   │   SYSTEM    │           │    SYSTEM        │         │   SYSTEM     │
   │             │           │                  │         │              │
   │ Registry    │◄─────────►│  Runtime:        │◄─────────┤ File Store   │
   │ (map[tool]) │           │  Simple exec     │          │ (MD + YAML)  │
   │             │           │                  │          │              │
   │ Built-in:   │           │  Cognitive:      │          │ Lifecycle:   │
   │ - bash      │           │  PERCEIVE        │          │ - Add        │
   │ - file_*    │           │  PLAN            │          │ - Update     │
   │ - http      │           │  ACT             │          │ - Delete     │
   │ - memory    │           │  OBSERVE         │          │ - Noop       │
   │             │           │  REFLECT         │          │              │
   │ Interfaces: │           │                  │          │ Background:  │
   │ - Tool      │           │ Provider:        │          │ - Consolidator
   │ - ReadOnly  │           │ LLM connection   │          │ - Compactor  │
   │ - Capable   │           │                  │          │ - Compress   │
   │             │           │ Phases:          │          │              │
   │ Permission: │           │ - Perceiver      │          │ Embedding:   │
   │ - Allow     │           │ - Planner        │          │ - OpenAI     │
   │ - Deny      │           │ - Executor       │          │              │
   │ - Ask       │           │ - Observer       │          │ Scopes:      │
   │             │           │ - Reflector      │          │ - user       │
   │ Result      │           │                  │          │ - session    │
   │ Store:      │           │ Options:         │          │ - feedback   │
   │ - Large     │           │ - Orchestrator   │          │ - global     │
   │   results   │           │ - Debate mode    │          │              │
   │   to disk   │           │ - Multi-agent    │          │ Types:       │
   │             │           │                  │          │ - episodic   │
   │ MCP Tools:  │           │ Multi-Agent:     │          │ - semantic   │
   │ - Dynamic   │           │ - SubAgent       │          │ - procedural │
   │   discovery │           │ - Orchestrator   │          │              │
   │             │           │ - Debate         │          └──────────────┘
   └─────────────┘           │                  │
                             │ RL Integration   │
                             │ - State tracking │
                             │ - Action learn   │
                             │ - Reward score   │
                             └──────────────────┘
        │                             │                                 │
        │                             │                                 │
        │                             ▼                                 │
        │                    ┌──────────────────┐                       │
        │                    │   KNOWLEDGE      │                       │
        │                    │   SYSTEM         │                       │
        │                    │                  │                       │
        │                    │ Retriever:       │◄──────────────────────┘
        │                    │ - BM25 search    │
        │                    │ - Vector search  │
        │                    │ - Hybrid rank    │
        │                    │                  │
        │                    │ Store:           │
        │                    │ - SQLite KB      │
        │                    │ - Chunks         │
        │                    │ - Embeddings     │
        │                    │ - Sources        │
        │                    │                  │
        │                    │ Graph:           │
        │                    │ - Nodes          │
        │                    │ - Edges          │
        │                    │ - Entity extract │
        │                    │ - Decay task     │
        │                    │ - Graph sync     │
        │                    │                  │
        │                    │ Optional:        │
        │                    │ - Reranker (LLM) │
        │                    │ - Caching        │
        │                    │ - Ingest         │
        │                    └──────────────────┘
        │
        ▼
   ┌─────────────┐
   │  CHANNELS   │
   │             │
   │ Interface:  │
   │ - Channel   │
   │ - Approval  │
   │ - Feedback  │
   │ - Reflection│
   │ - Notify    │
   │             │
   │ Telegram:   │
   │ - Bot API   │
   │ - Updates   │
   │ - Approval  │
   │ - Feedback  │
   │ - Timeout   │
   │             │
   │ TUI:        │
   │ - Bubbletea │
   │ - Interactive
   │ - Streaming │
   │ - Approval  │
   │ - Feedback  │
   └─────────────┘
        │
        ├─────────────────────────────┐
        ▼                             ▼
   ┌──────────────┐          ┌──────────────┐
   │  SCHEDULER   │          │     MCP      │
   │              │          │   MANAGER    │
   │ Cron-based   │          │              │
   │ polling      │          │ MCP servers  │
   │              │          │ (discovered) │
   │ Db polling   │          │              │
   │ every 60s    │          │ Tool adapter │
   │              │          │ wrappers     │
   │ Fire tasks   │          │              │
   │ on schedule  │          │ Hot-reload   │
   │              │          │ watcher      │
   │              │          │ (~/.IronClaw │
   │              │          │  /mcp/)      │
   └──────────────┘          └──────────────┘
        │
        └──────────────────────────────┐
                                       │
        ┌──────────────────────────────┴──────────────┐
        │                                             │
        ▼                                             ▼
   ┌─────────────┐                          ┌─────────────┐
   │    HOOK     │                          │   SKILLS    │
   │   SYSTEM    │                          │             │
   │             │                          │ Manager:    │
   │ Pre-tool    │                          │ - Load MD   │
   │ Post-tool   │                          │ - Parse FM  │
   │ User msg    │                          │ - Lazy load │
   │ Pre-compact │                          │   content   │
   │             │                          │             │
   │ Factory:    │                          │ Built-in:   │
   │ - Git inject│                          │ - In repo   │
   │ - Workdir   │                          │             │
   │ - Preserver │                          │ User:       │
   │             │                          │ - ~/.Iron   │
   │ Audit log   │                          │   Claw/     │
   │ (DB)        │                          │   skills/   │
   │             │                          │             │
   │             │                          │ ClawHub:    │
   │             │                          │ - CLI pkg   │
   │             │                          │             │
   └─────────────┘                          └─────────────┘
        │
        ▼
   ┌──────────────┐
   │     RL       │
   │   SYSTEM     │
   │              │
   │ Trainer:     │
   │ - Episode    │
   │ - Training   │
   │ - Storage    │
   │              │
   │ Algorithms:  │
   │ - Bandit     │
   │ - PPO        │
   │ - DQN        │
   │              │
   │ State/Action │
   │ - Tool usage │
   │ - Plan steps │
   │              │
   │ Reward:      │
   │ - Success    │
   │ - Efficiency │
   │ - Safety     │
   │ - Satisfaction
   │              │
   │ Experience   │
   │ Buffer (DB)  │
   │              │
   └──────────────┘
```

## Component Interactions

### Request Flow

```
User Input (Telegram/TUI)
    │
    ▼
Channel.Start() → InboundHandler
    │
    ▼
Gateway.handleInbound()
    │
    ├─► Session.GetOrCreate()
    │
    ├─► Cognitive Mode Check
    │   ├─► Yes: CognitiveAgent.HandleMessage()
    │   │   ├─► PERCEIVE (Memory + KB search)
    │   │   ├─► PLAN (LLM tool planning)
    │   │   ├─► ACT (Execute tools, approval)
    │   │   ├─► OBSERVE (Result evaluation)
    │   │   └─► REFLECT (Replan decision)
    │   │
    │   └─► No: Runtime.Execute()
    │       ├─► Get LLM provider
    │       ├─► Execute tools
    │       └─► Return response
    │
    ├─► Lifecycle Manager (if memory enabled)
    │   ├─► Extract facts
    │   ├─► Search similar
    │   └─► ADD/UPDATE/DELETE/NOOP
    │
    └─► Channel.Send() response
```

### Tool Execution Pipeline

```
LLM Returns Tool Calls
    │
    ▼
Extract ToolUseBlocks
    │
    ├─► For each tool:
    │   │
    │   ├─► Permission.Check()
    │   │
    │   ├─► Hook.FirePreToolUse()
    │   │
    │   ├─► RequiresApproval? → Channel.SendApprovalRequest()
    │   │
    │   ├─► Tool.Execute()
    │   │
    │   ├─► Hook.FirePostToolUse()
    │   │
    │   └─► Result.Size > threshold? → ResultStore.Write()
    │
    ▼
Return Results to LLM
```

### Memory Lifecycle Pipeline

```
New Fact Extracted
    │
    ▼
Normalize & Embed
    │
    ▼
Memory.Search(embedding) → Similar facts
    │
    ▼
LLM Decision Engine
    ├─► ADD: New unique fact
    │   └─► Write to file/db
    │
    ├─► UPDATE: Merge with existing
    │   └─► Update file/db
    │
    ├─► DELETE: Remove conflicting
    │   └─► Delete file/db
    │
    └─► NOOP: Redundant
        └─► Skip
    │
    ▼
Update Memory Index
    │
    ▼
GraphSync.Sync() (if graph enabled)
    │
    ▼
Background Tasks:
    ├─► Consolidator (24h)
    ├─► Compactor (1h)
    └─► GraphDecay (6h)
```

## Data Storage

### File Structure

```
~/.IronClaw/
├── memory/                      # File-based memory
│   ├── user/                    # User-scoped facts
│   │   ├── mem_001.md
│   │   ├── mem_002.md
│   │   └── ...
│   ├── session/                 # Session-scoped facts
│   │   ├── mem_100.md
│   │   └── ...
│   ├── feedback/                # User feedback
│   │   └── ...
│   └── global/                  # Global facts
│       └── ...
│
├── cache/
│   └── tool-results/            # Large tool results
│       ├── result_abc.txt
│       └── ...
│
├── skills/                      # Installed user skills
│   ├── web_scraper/
│   │   └── SKILL.md
│   └── ...
│
├── mcp/                         # MCP server configs (hot-reload)
│   ├── github.yaml
│   └── ...
│
└── logs/                        # Optional log files
    └── ironclaw.log
```

### Database Schema (SQLite)

```
ironclaw.db
├── sessions
│   ├── id (PK)
│   ├── channel
│   ├── channel_id
│   ├── created_at
│   └── updated_at
│
├── session_messages
│   ├── id (PK)
│   ├── session_id (FK)
│   ├── role (user/assistant/tool_use/tool_result)
│   ├── content
│   ├── tool_name
│   ├── tool_input
│   └── created_at
│
├── memory_index
│   ├── id (PK)
│   ├── scope (user/session/feedback/global)
│   ├── user_id
│   ├── session_id
│   ├── type (episodic/semantic/procedural)
│   ├── importance (1-10)
│   ├── strength (0-1)
│   ├── sensitivity (public/private/secret)
│   ├── embedding (BLOB/float32)
│   ├── created_at
│   ├── updated_at
│   └── accessed_at
│
├── knowledge_sources
│   ├── id (PK)
│   ├── uri
│   ├── source_type
│   ├── title
│   ├── chunk_count
│   ├── metadata
│   ├── created_at
│   └── updated_at
│
├── knowledge_chunks
│   ├── id (PK)
│   ├── source_id (FK)
│   ├── source_uri
│   ├── source_type
│   ├── content
│   ├── embedding (BLOB/float32)
│   ├── chunk_index
│   ├── metadata
│   └── created_at
│
├── scheduled_tasks
│   ├── id (PK)
│   ├── name
│   ├── cron_expr
│   ├── prompt
│   ├── channel
│   ├── channel_id
│   ├── enabled (boolean)
│   ├── created_at
│   └── updated_at
│
├── hook_audit_log
│   ├── id (PK)
│   ├── hook_type
│   ├── event_data
│   ├── result
│   └── timestamp
│
├── rl_experiences
│   ├── id (PK)
│   ├── episode_id
│   ├── state (BLOB/JSON)
│   ├── action (BLOB/JSON)
│   ├── reward (float)
│   ├── next_state (BLOB/JSON)
│   ├── done (boolean)
│   └── timestamp
│
└── knowledge_graph (if graph enabled)
    ├── nodes
    │   ├── id (PK)
    │   ├── type
    │   ├── name
    │   ├── properties
    │   ├── created_at
    │   └── updated_at
    │
    └── edges
        ├── id (PK)
        ├── source_id (FK)
        ├── target_id (FK)
        ├── type
        ├── weight
        ├── properties
        ├── created_at
        ├── valid_from
        └── valid_to
```

## Execution Modes

### Mode 1: Simple Agent

```
Config: agent.mode = "simple"

Flow:
User Input
    ▼
Get LLM Provider
    ▼
Build System Prompt + History
    ▼
Call LLM → ToolCalls
    ▼
Execute Tools
    ▼
Return Results to LLM
    ▼
LLM Response
    ▼
Send to Channel
```

**Use When**: Need fast, simple task execution

### Mode 2: Cognitive Agent

```
Config: agent.mode = "cognitive"

Flow:
User Input
    ▼
PERCEIVE: Gather context
    ├─► Session history
    ├─► Memory search (embeddings)
    ├─► KB search (BM25 + vector)
    └─► Build system prompt
    ▼
PLAN: Generate tool plan
    ├─► LLM analyzes task
    ├─► Plan tools needed
    └─► Request approval if destructive
    ▼
ACT: Execute tools
    ├─► Permission check
    ├─► Run tools (concurrent if read-only)
    └─► Store results
    ▼
OBSERVE: Evaluate results
    ├─► Did results match plan?
    ├─► Assign confidence
    └─► Flag anomalies
    ▼
REFLECT: Should replan?
    ├─► Analyze vs objectives
    ├─► Ask user (if configured)
    ├─► Replan decision
    └─► Learn from outcome (RL)
    ▼
Send Response to Channel
```

**Use When**: Need sophisticated reasoning, multi-step planning, user interaction

## Concurrency Model

### Thread Safety

```
├─► Tool Registry
│   └─► RWMutex (read-heavy)
│
├─► Memory Store
│   └─► Goroutine-safe operations
│   ├─► Consolidator (24h loop)
│   ├─► Compactor (1h loop)
│   └─► Compressor (on-demand)
│
├─► Session Manager
│   └─► Per-session lock (concurrent sessions OK)
│
├─► Gateway
│   ├─► Channels (concurrent)
│   ├─► Scheduler polling (1 goroutine)
│   ├─► MCP hot-reload (1 goroutine)
│   └─► Result cleanup (1 goroutine)
│
├─► Tool Execution
│   ├─► Read-only tools (concurrent)
│   │   └─► max_concurrency limit
│   └─► Write tools (serial)
│
└─► Knowledge Graph
    └─► Decay task (6h loop)
```

### Goroutine Management

- **Channel listeners**: 1 per channel (Telegram, TUI)
- **Scheduler polling**: 1 goroutine
- **MCP hot-reload**: 1 goroutine
- **Result cleanup**: 1 goroutine
- **Memory consolidator**: 1 goroutine
- **Memory compactor**: 1 goroutine
- **Graph decay**: 1 goroutine (if enabled)

All use `context.Done()` for graceful shutdown.

## Initialization Order

```
main.go
    │
    ▼
config.Load() → Config struct
    │
    ▼
gateway.New()
    ├─► 1. initDatabase()
    │   └─► Open SQLite, run migrations
    │
    ├─► 2. initToolsAndHooks()
    │   ├─► Create tool registry
    │   ├─► Register built-in tools
    │   └─► Create hook manager
    │
    ├─► 3. initAgentRuntime()
    │   └─► Create agent.Runtime with provider
    │
    ├─► 4. initMemorySystem()
    │   ├─► Create FileMemoryStore
    │   ├─► Create lifecycle manager
    │   ├─► Create consolidator
    │   └─► Create compactor
    │
    ├─► 5. initCognitiveAgent()
    │   └─► Create all 5 phases (if enabled)
    │
    ├─► 6. initKnowledgeSystem()
    │   ├─► Create KB (if enabled)
    │   └─► Create graph (if enabled)
    │
    ├─► 7. initSkillManager()
    │   ├─► Load built-in skills
    │   └─► Load user skills
    │
    ├─► 8. initMultiAgent()
    │   └─► Create orchestrator (if enabled)
    │
    └─► 9. Wire dependencies
        ├─► runtime.SetMemoryStore()
        ├─► runtime.SetSkillManager()
        ├─► cognitiveAgent.SetMemoryStore()
        └─► runtime.SetApprovalFunc()
    │
    ▼
gateway.Start()
    ├─► Start MCP servers
    ├─► Start MCP watcher
    ├─► Start channels
    ├─► Start scheduler
    ├─► Start HTTP server (if enabled)
    ├─► Start RL trainer (if enabled)
    └─► Ready for messages
```

---

**Last Updated**: April 10, 2026
