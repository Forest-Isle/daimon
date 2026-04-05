# IronClaw 通道、MCP、技能与存储系统 — 完整实现分析

## 目录

- [Part 1: Channel 通道系统](#part-1-channel-通道系统)
  - [1. 核心接口](#1-核心接口)
  - [2. Telegram 适配器](#2-telegram-适配器)
  - [3. TUI 适配器（Bubble Tea）](#3-tui-适配器bubble-tea)
- [Part 2: MCP 协议客户端](#part-2-mcp-协议客户端)
  - [4. ToolAdapter：工具桥接](#4-tooladapter工具桥接)
  - [5. Manager：生命周期管理](#5-manager生命周期管理)
- [Part 3: Skill 技能系统](#part-3-skill-技能系统)
  - [6. Skill 模型与解析](#6-skill-模型与解析)
  - [7. Manager：技能库管理](#7-manager技能库管理)
- [Part 4: Store 存储层](#part-4-store-存储层)
  - [8. SQLite 包装器](#8-sqlite-包装器)
  - [9. 迁移系统](#9-迁移系统)
- [Part 5: Gateway 装配](#part-5-gateway-装配)
  - [10. 初始化序列](#10-初始化序列)
  - [11. 配置结构](#11-配置结构)
  - [12. CLI 入口](#12-cli-入口)
- [推荐阅读顺序](#推荐阅读顺序)

---

# Part 1: Channel 通道系统

## 1. 核心接口

📄 **文件**: `internal/channel/channel.go` + `internal/channel/message.go`

### 1.1 Channel 接口

```go
type Channel interface {
    Name() string                                                        // "telegram" | "tui"
    Start(ctx context.Context, handler InboundHandler) error             // 开始接收消息
    Send(ctx context.Context, msg OutboundMessage) error                 // 发送消息
    SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error) // 流式发送
    Stop(ctx context.Context) error                                     // 停止
}

type InboundHandler func(ctx context.Context, msg InboundMessage)

type StreamUpdater interface {
    Update(text string) error  // 增量更新消息
    Finish(text string) error  // 结束流式更新
}
```

### 1.2 可选接口：审批与反思

```go
// 交互式工具审批（不实现则自动通过）
type ApprovalSender interface {
    SendApprovalRequest(ctx context.Context, target MessageTarget,
                       toolName, input string) (bool, error)
}

// 重规划决策（不实现则默认 ReplanContinue）
type ReflectionSender interface {
    SendReflectionRequest(ctx context.Context, target MessageTarget,
                         reason string, confidence float64) (ReplanDecision, error)
}

type ReplanDecision string
const (
    ReplanContinue ReplanDecision = "continue"  // 继续执行
    ReplanAdjust   ReplanDecision = "adjust"    // 调整计划
    ReplanAbort    ReplanDecision = "abort"     // 中止
)
```

### 1.3 消息类型

```go
type InboundMessage struct {
    Channel      string  // 通道名
    ChannelID    string  // 用户/聊天标识
    UserID       string
    UserName     string
    Text         string  // 消息内容
    CallbackData string  // Inline keyboard 回调数据
    ReplyToMsgID string
}

type OutboundMessage struct {
    Channel     string
    ChannelID   string
    Text        string
    ParseMode   string  // "Markdown" | "HTML" | ""
    ReplyMarkup any     // 通道特定（如 Inline keyboard）
}

type MessageTarget struct {
    Channel   string
    ChannelID string
}
```

---

## 2. Telegram 适配器

📄 **核心文件**: `internal/channel/telegram/adapter.go`

### 2.1 结构体

```go
type Adapter struct {
    bot                *tgbotapi.BotAPI
    allowedUserIDs     map[int64]bool         // 白名单
    handler            channel.InboundHandler
    stopCh             chan struct{}
    pendingApprovals   sync.Map               // toolName → chan bool
    pendingReflections sync.Map               // key → chan ReplanDecision
    approvalTimeoutSecs int                   // 默认 120s
}
```

### 2.2 消息处理流程

```
Start(handler)
     │
     ├─ 启动 Telegram 长轮询（timeout=30s）
     │
     └─ 收到 Update：
         ├─ CallbackQuery → handleCallback()
         │   ├─ 解析 "action:key"
         │   ├─ "approve:toolName" → pendingApprovals[toolName] <- true
         │   ├─ "deny:toolName" → pendingApprovals[toolName] <- false
         │   ├─ "reflect_continue:key" → pendingReflections[key] <- Continue
         │   ├─ "reflect_adjust:key" → pendingReflections[key] <- Adjust
         │   └─ "reflect_abort:key" → pendingReflections[key] <- Abort
         │
         └─ Message → 验证 UserID → go handler(msg)
```

### 2.3 审批流程（ApprovalSender 实现）

📄 **文件**: `internal/channel/telegram/adapter.go` — `SendApprovalRequest`

```
SendApprovalRequest(toolName, input)
     │
     ├─ 发送消息：
     │   "🔧 Tool: *{toolName}*
     │    ```
     │    {input}
     │    ```
     │    Approve execution?"
     │
     ├─ 附带 Inline Keyboard：
     │   [✅ Approve]  [❌ Deny]
     │
     ├─ 注册 resultCh 到 pendingApprovals[toolName]
     │
     └─ 等待（超时 120s）：
         ├─ 收到 true → 批准
         ├─ 收到 false → 拒绝
         └─ 超时 → 默认拒绝
```

### 2.4 反思/重规划流程（ReflectionSender 实现）

```
SendReflectionRequest(reason, confidence)
     │
     ├─ 发送消息：
     │   "🤔 *Low confidence plan* ({confidence}%)
     │    Reason: {reason}"
     │
     ├─ 附带 Inline Keyboard：
     │   [▶️ Continue]  [🔄 Adjust]  [🛑 Abort]
     │
     └─ 等待（超时 120s）→ 默认 Continue
```

### 2.5 流式更新

📄 **文件**: `internal/channel/telegram/adapter.go` — `streamUpdater`

```go
type streamUpdater struct {
    bot    *tgbotapi.BotAPI
    chatID int64
    msgID  int
    mu     sync.Mutex
    last   string
    lastAt time.Time
}

const minUpdateInterval = 1 * time.Second  // 限流：最少 1s 间隔
```

发送初始 "⏳ Thinking..." 消息 → 后续通过 `EditMessageText` 更新 → 限流 1s 防止 API 过载。

---

## 3. TUI 适配器（Bubble Tea）

📄 **核心文件**: `internal/channel/tui/adapter.go` + `internal/channel/tui/model.go`

### 3.1 技术栈

| 库 | 用途 |
|---|------|
| `bubbletea` | Terminal UI 框架（Elm 架构） |
| `viewport` | 滚动视口组件 |
| `textarea` | 多行输入框 |
| `lipgloss` | 终端样式 |
| `glamour` | Markdown → ANSI 渲染 |

### 3.2 Model 状态机

📄 **文件**: `internal/channel/tui/model.go`

```go
type mode int
const (
    modeChat       mode = iota  // 正常聊天（textarea 活跃）
    modeApproval                // 审批模式（y/n/a 拦截）
    modeReflection              // 反思模式（1/2/3 拦截）
)

type Model struct {
    viewport viewport.Model      // 聊天历史滚动区
    textarea textarea.Model      // 输入框
    mode     mode                // 当前模式
    messages []chatMessage       // 聊天消息列表
    streamingID   string         // 流式更新 ID
    streamingText string         // 当前流式文本
    approvalCh    chan bool      // 审批结果通道
    reflectCh     chan ReplanDecision // 反思结果通道
    ...
}
```

### 3.3 键盘交互

| 模式 | 按键 | 行为 |
|------|------|------|
| modeChat | Enter | 发送消息 |
| modeChat | Ctrl+C | 退出 |
| modeChat | `/quit` | 退出 |
| modeApproval | `y/Y` | 批准工具 → "✅ Approved" |
| modeApproval | `n/N/Esc` | 拒绝工具 → "❌ Denied" |
| modeApproval | `a/A` | 始终批准 → "✅ Always approve" |
| modeReflection | `1/c/C` | 继续执行 |
| modeReflection | `2/a/A` | 调整计划 |
| modeReflection | `3/x/X/Esc` | 中止 |

### 3.4 渲染布局

```
┌──────────────────────────────────────────────┐
│  IronClaw v1.0  [cognitive]                  │ ← 标题栏（紫色）
├──────────────────────────────────────────────┤
│  [10:30] You: 帮我分析这段代码               │
│  [10:30] Agent: 这段代码实现了...            │ ← Viewport
│  [10:31] You: 能优化一下吗？                 │   （Glamour 渲染 Markdown）
│  [10:31] Agent: 当然，建议... ▊              │ ← 流式光标
├──────────────────────────────────────────────┤
│  ┌──────────────────────────────────────┐   │
│  │  请输入消息...                        │   │ ← Textarea
│  └──────────────────────────────────────┘   │
└──────────────────────────────────────────────┘
```

审批对话框覆盖底部输入区：

```
┌──────────────────────────────────────────────┐
│  🔧 Tool: bash                               │ ← 橙色圆角边框
│  ls -la /etc/                                │
│  [y] Approve  [n] Deny  [a] Always approve   │
└──────────────────────────────────────────────┘
```

### 3.5 流式更新

📄 **文件**: `internal/channel/tui/adapter.go` — `tuiStreamUpdater`

```go
type tuiStreamUpdater struct {
    program *tea.Program
    id      string
    latest  atomic.Value   // 最新文本（原子操作）
    done    chan struct{}
}

const streamThrottle = 50 * time.Millisecond  // 50ms 刷新间隔
```

后台 goroutine `pump()` 每 50ms 检查 `latest` 是否变化，变化则发送 `streamUpdateMsg` 到 Bubble Tea 程序。

### 3.6 样式系统

📄 **文件**: `internal/channel/tui/styles.go`

| 元素 | 颜色 |
|------|------|
| 标题栏 | 紫色背景 (#7D56F4) |
| 用户标签 | 绿色 (#04B575) |
| Agent 标签 | 紫色 (#7D56F4) |
| 系统消息 | 灰色斜体 (#626262) |
| 审批框 | 橙色边框 (#FF9900) |
| 反思框 | 金色边框 (#FFD700) |
| 流式文本 | 紫色斜体 |

---

# Part 2: MCP 协议客户端

📄 **核心文件**: `internal/mcp/adapter.go` + `internal/mcp/manager.go`

## 4. ToolAdapter：工具桥接

📄 **文件**: `internal/mcp/adapter.go`

```go
type ToolAdapter struct {
    client     client.MCPClient  // mark3labs/mcp-go
    serverName string
    toolDef    mcp.Tool          // MCP 工具定义
    approval   bool
}
```

**工具命名格式**: `mcp_{serverName}_{toolName}`

例如：MCP 服务 `github` 提供的 `create_issue` 工具 → 注册为 `mcp_github_create_issue`

**Execute 流程**：
1. 反序列化 JSON → `map[string]any`
2. 创建 `mcp.CallToolRequest`
3. 调用 `client.CallTool(ctx, request)`
4. 提取所有 `TextContent` → 拼接为输出

## 5. Manager：生命周期管理

📄 **文件**: `internal/mcp/manager.go`

```go
type Manager struct {
    clients map[string]client.MCPClient
    mu      sync.RWMutex
}
```

### 5.1 服务启动流程

```
StartServers(ctx, servers, registry)
     │
     └─ 对每个 server：
         startServer(name, config, registry)
              │
              ├─ 1. 构建环境变量（config.Env）
              │
              ├─ 2. 创建 Stdio 客户端：
              │     client.NewStdioMCPClient(command, env, args...)
              │
              ├─ 3. MCP 握手：
              │     client.Initialize(ctx, {
              │       ProtocolVersion: LATEST,
              │       ClientInfo: {Name: "ironclaw", Version: "1.0.0"}
              │     })
              │
              ├─ 4. 工具发现：
              │     client.ListTools(ctx) → []mcp.Tool
              │
              ├─ 5. 注册工具：
              │     对每个 tool → registry.Register(NewToolAdapter(...))
              │
              └─ 6. 存储 client 到 m.clients[name]
```

### 5.2 热重载

```go
func (m *Manager) SyncServers(ctx, desired, registry) {
    // 停止不在 desired 中的服务：
    //   m.StopServer(name, registry)
    //   → 关闭 client + registry.UnregisterByPrefix("mcp_{name}_")
    //
    // 启动 desired 中的新服务：
    //   m.startServer(name, config, registry)
}
```

Gateway 中有一个后台 watcher，每 30s 扫描 `~/.IronClaw/mcp/*.yaml` 目录，自动调用 `SyncServers`。

---

# Part 3: Skill 技能系统

📄 **核心文件**: `internal/skill/skill.go` + `internal/skill/manager.go`

## 6. Skill 模型与解析

📄 **文件**: `internal/skill/skill.go`

### 6.1 SKILL.md 文件格式

```markdown
---
name: code-review
description: Systematic code review with checklist
version: "1.0"
author: IronClaw
tags: [review, quality]
---

# Code Review Skill

## Steps
1. Check for obvious bugs
2. Verify error handling
3. ...
```

### 6.2 Skill 结构体

```go
type Skill struct {
    // 即时加载（解析时）
    Name        string    `yaml:"name"`
    Description string    `yaml:"description"`
    Version     string    `yaml:"version"`
    Author      string    `yaml:"author"`
    Tags        []string  `yaml:"tags"`
    Path        string    // 绝对路径

    // 延迟加载（调用 Content() 时）
    content     string
    contentOnce sync.Once   // 确保只加载一次
    contentErr  error
}
```

**两阶段加载**：
- **ParseSkill(path)**：读取文件，解析 YAML frontmatter → Skill 结构体（不读 body）
- **skill.Content()**：`sync.Once` 确保只读一次 body 内容，缓存结果

## 7. Manager：技能库管理

📄 **文件**: `internal/skill/manager.go`

### 7.1 加载来源

```go
//go:embed builtin/*/SKILL.md
var builtinSkills embed.FS
```

| 来源 | 方法 | 说明 |
|------|------|------|
| 内置技能 | `LoadBuiltin()` | 从编译时嵌入的 `builtin/` 目录加载 |
| 用户技能 | `LoadDir(dir)` | 从 `~/.IronClaw/skills/` 加载 |
| 额外目录 | `LoadDir(dir)` | 从配置的额外目录加载 |

去重规则：先加载的优先（同名技能后加载的被忽略）。

### 7.2 渐进式披露（Progressive Disclosure）

```
Agent 系统提示（始终包含）：
     │
     ├─ "## Skills System"
     ├─ "**Available Skills:**"
     │   "- **code-review** (v1.0): Systematic code review [review, quality]"
     │   "- **debugging** (v1.0): Step-by-step debugging [debug]"
     │
     └─ "**How to Use Skills:**"
         "1. Recognize when a skill applies"
         "2. Load the skill: call `read_skill` tool"
         "3. Follow the skill's workflow"

Agent 识别到需要 code-review：
     │
     └─ 调用 read_skill({"action": "read", "name": "code-review"})
         │
         └─ 返回完整 Markdown 指令（Content）
```

### 7.3 技能选择算法

📄 **文件**: `internal/skill/manager.go` — `Select` 方法

```go
func skillMatches(s *Skill, lowerText string) bool {
    // 匹配条件（任一满足即可）：
    // 1. 技能名包含在文本中，或文本包含在技能名中
    // 2. 描述中任一单词（>3 字符）出现在文本中
    // 3. 任一标签出现在文本中
}
```

如果没有技能匹配用户文本 → 返回所有技能（fallback）。

---

# Part 4: Store 存储层

📄 **核心文件**: `internal/store/sqlite.go`

## 8. SQLite 包装器

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
    *sql.DB
}

func Open(path string) (*DB, error) {
    // 创建目录
    // 打开 SQLite，DSN 参数：
    //   _journal_mode=WAL         → 写前日志（并发读写）
    //   _busy_timeout=5000        → 5s 等待锁
    //   _foreign_keys=on          → 外键约束
    // MaxOpenConns(1)             → 单写入者
    // 执行迁移
}
```

**WAL 模式** 允许多个读者与一个写者并发操作，是 SQLite 在并发场景下的最佳实践。

## 9. 迁移系统

📄 **文件**: `internal/store/sqlite.go` — `migrate` 方法

```
migrate(db)
     │
     ├─ 创建 _migrations 追踪表（幂等）
     │
     ├─ 读取所有 .sql 文件（embed.FS，按字母排序）
     │   001_init.sql → 004_knowledge_base.sql → 005_knowledge_graph.sql → ...
     │
     └─ 对每个迁移：
         ├─ 检查 _migrations 表是否已执行
         ├─ 未执行 → 执行 SQL
         │   └─ 容错处理："duplicate column" / "table already exists" 视为已执行
         └─ 记录到 _migrations 表
```

### 9.1 迁移文件清单

| 文件 | 说明 |
|------|------|
| `001_init.sql` | sessions, messages, scheduled_tasks, tool_log |
| `004_knowledge_base.sql` | kb_sources, kb_chunks, kb_chunks_fts |
| `005_knowledge_graph.sql` | kg_nodes, kg_edges, kg_provenance |
| `006_file_memory_index.sql` | memory_index, memory_fts, memory_embeddings |
| `007_rl_system.sql` | rl_episodes, rl_trajectories, rl_rewards, rl_bandit_arms |
| `008_access_log.sql` | memory_access_log, memory_access_stats |
| `009_memory_type_fields.sql` | memory_index 增加 type/emotion/sensitivity |
| `010_reflection_tracker.sql` | reflection_tracker_state |
| `011_temporal_graph.sql` | kg_edges 增加 valid_from/valid_to |
| `012_memory_audit_log.sql` | memory_audit_log |
| `013_cleanup_legacy.sql` | 清理旧表（V1/V2 兼容） |
| `014_permission_audit_log.sql` | permission_audit_log |
| `015_sidechain_entries.sql` | sidechain_entries（侧链推理） |

---

# Part 5: Gateway 装配

📄 **核心文件**: `internal/gateway/gateway.go` + `cmd/ironclaw/main.go`

## 10. 初始化序列

Gateway 是整个系统的装配层，**初始化顺序严格、有依赖关系**：

```
Gateway.New(cfg)
     │
     ├──  1. Database           → store.Open(path)
     ├──  2. Session Manager    → session.NewManager(db)
     ├──  3. Tool Registry      → tool.NewRegistry()
     │     ├─ 注册 BashTool, FileTool, HTTPTool, BrowserTool
     │     └─ 创建 PermissionEngine
     ├──  4. Hook Event System  → hook.NewManager(cfg.Hooks)
     ├──  5. LLM Provider      → agent.NewClaudeProvider(apiKey, baseURL)
     ├──  6. Agent Runtime      → agent.NewRuntime(provider, tools, sessions, db, cfg)
     ├──  7. Result Persistence → tool.NewResultStore(cacheDir, threshold, preview, ttl)
     │
     ├──  8. Memory Store（如果启用）
     │     ├─ EmbeddingProvider: NoopEmbedding 或 CachedEmbedder(OpenAIEmbedding)
     │     ├─ FileMemoryStore: 文件 + SQLite 索引
     │     ├─ runtime.SetMemoryStore + SetMemoryBaseDir
     │     ├─ IncrementalCompressor
     │     ├─ ForgettingCurveManager
     │     └─ 如果启用 FactExtraction：
     │         ├─ LLMFactExtractor + PIIDetector
     │         ├─ ReflectionTracker
     │         ├─ LifecycleManager
     │         ├─ Compactor (后台 6h 循环)
     │         └─ Profiler
     │
     ├──  9. Cognitive Agent（如果 mode=cognitive）
     │     ├─ Perceiver, Planner, Executor, Observer, Reflector
     │     ├─ SetMemoryStore, SetLifecycleManager
     │     └─ SetEntityExtractor
     │
     ├── 10. RL System（如果启用）
     │     └─ Bandit + PPO + DQN 策略
     │
     ├── 11. Knowledge Base（如果启用）
     │     ├─ SQLiteKnowledgeBase
     │     ├─ HybridRetriever + LLMReranker
     │     └─ IngestPipeline → 自动摄入配置的目录
     │
     ├── 12. Knowledge Graph（如果启用）
     │     ├─ SQLiteGraph
     │     ├─ LLMEntityExtractor
     │     ├─ GraphSync → lifecycleMgr.SetGraphSync()
     │     └─ GraphDecayTask (后台 24h 循环)
     │
     ├── 13. Skill Manager
     │     ├─ LoadBuiltin()
     │     ├─ LoadDir("~/.IronClaw/skills/")
     │     └─ 注册 SkillTool 到 Registry
     │
     ├── 14. Multi-Agent System
     │     ├─ AgentManager → LoadDir("~/.IronClaw/agents/")
     │     ├─ BackgroundManager
     │     └─ AgentOrchestrator
     │
     ├── 15. Compression Pipeline
     │
     ├── 16. Scheduler（cron 任务调度）
     │
     └── 17. Final Assembly
           ├─ 创建 Channels（Telegram / TUI）
           ├─ 设置 ApprovalFunc（连接 Channel ↔ Runtime）
           └─ Scheduler ↔ Channel 路由
```

### 关键适配器

| 适配器 | 作用 |
|--------|------|
| `completerAdapter` | 桥接 `agent.Provider` → `memory.Completer`（避免循环导入） |
| `noopKBEmbedder` | 无 OpenAI Key 时的空操作 EmbeddingProvider（BM25 降级） |

## 11. 配置结构

📄 **文件**: 配置对象映射（YAML `configs/ironclaw.yaml`）

主要配置节：

| 配置节 | 说明 |
|--------|------|
| `llm` | Claude API：api_key, model, base_url, max_tokens |
| `telegram` | Bot token, 允许的用户 ID 列表 |
| `tui` | 终端 UI 设置 |
| `agent` | mode (simple/cognitive), max_iterations, system_prompt |
| `cognitive` | 5 阶段参数：max_parallel_tools, confidence_threshold |
| `rl` | 强化学习：bandit/ppo/dqn 参数，奖励配置 |
| `compression` | 上下文压缩：分层策略 + 百分比阈值 |
| `store` | SQLite 路径 |
| `memory` | 文件记忆 + 嵌入配置 + 遗忘曲线参数 |
| `knowledge` | 知识库摄入 + 搜索参数 |
| `graph` | 知识图谱设置 |
| `scheduler` | 任务调度（enabled, poll_interval） |
| `tools` | 工具配置（bash/file/http/mcp/并发/结果持久化） |
| `permissions` | 权限规则 |
| `hooks` | 钩子事件处理 |
| `skills` | 技能系统 |
| `agents` | 多 Agent 协作 |

支持 `${VAR}` 环境变量展开。

### 用户目录

```
~/.IronClaw/
├── Soul.md           → 人格/风格定义
├── Memory.md         → 持久化规则
├── Agent.md          → Agent 核心指令前缀
├── config.yaml       → 用户配置覆盖
├── skills/           → 用户安装的 SKILL.md
├── agents/           → 用户定义的 Agent 规格
├── mcp/              → MCP 服务配置 (*.yaml)
├── memory/           → 文件记忆存储
│   ├── user/
│   ├── session/
│   ├── archived/
│   └── MEMORY.md
└── tui.log           → TUI 日志
```

## 12. CLI 入口

📄 **文件**: `cmd/ironclaw/main.go`

Cobra CLI 命令：

| 命令 | 说明 |
|------|------|
| `ironclaw start` | 启动 Telegram 通道 |
| `ironclaw tui` | 启动终端 UI |
| `ironclaw version` | 打印版本 |
| `ironclaw skill list` | 列出所有技能 |
| `ironclaw skill search <query>` | 搜索技能 |
| `ironclaw skill install <path>` | 安装技能 |
| `ironclaw memory reindex` | 重建记忆索引 |
| `ironclaw memory migrate` | 迁移旧版记忆到文件 |

---

# 推荐阅读顺序

### 第一层：通道抽象

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 1 | `internal/channel/channel.go` | `Channel`, `StreamUpdater`, `ApprovalSender`, `ReflectionSender` 接口 |
| 2 | `internal/channel/message.go` | `InboundMessage`, `OutboundMessage`, `MessageTarget` |

### 第二层：Telegram 适配器

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 3 | `internal/channel/telegram/adapter.go` | 完整生命周期 + 审批/反思流程 |
| 4 | `internal/channel/telegram/formatter.go` | Telegram 格式化 + Markdown 转义 |

### 第三层：TUI 适配器

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 5 | `internal/channel/tui/model.go` | 状态机 + 三种模式 + 键盘处理 |
| 6 | `internal/channel/tui/adapter.go` | Bubble Tea 集成 + 流式更新 |
| 7 | `internal/channel/tui/styles.go` | lipgloss 主题 |
| 8 | `internal/channel/tui/formatter.go` | Glamour Markdown 渲染 |

### 第四层：MCP

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 9 | `internal/mcp/adapter.go` | `ToolAdapter` — MCP 工具到 `tool.Tool` 的桥接 |
| 10 | `internal/mcp/manager.go` | 服务启动、握手、工具发现、热重载 |

### 第五层：技能系统

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 11 | `internal/skill/skill.go` | SKILL.md 格式 + 两阶段加载 |
| 12 | `internal/skill/manager.go` | 渐进式披露 + 选择算法 |

### 第六层：存储与装配

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 13 | `internal/store/sqlite.go` | WAL 模式 + 嵌入式迁移 |
| 14 | `internal/gateway/gateway.go` | 完整装配序列（最重要！） |
| 15 | `cmd/ironclaw/main.go` | CLI 入口 + 命令定义 |
