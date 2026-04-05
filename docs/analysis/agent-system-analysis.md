# IronClaw Agent 系统 — 完整实现分析

## 目录

- [1. 架构总览：双模式 Agent](#1-架构总览双模式-agent)
- [2. Provider 接口：LLM 抽象层](#2-provider-接口llm-抽象层)
- [3. Simple 模式：Runtime 线性循环](#3-simple-模式runtime-线性循环)
- [4. Cognitive 模式：五阶段认知循环](#4-cognitive-模式五阶段认知循环)
- [5. PERCEIVE 阶段：感知与分类](#5-perceive-阶段感知与分类)
- [6. PLAN 阶段：任务分解为 DAG](#6-plan-阶段任务分解为-dag)
- [7. ACT 阶段：拓扑排序 + 并行执行](#7-act-阶段拓扑排序--并行执行)
- [8. OBSERVE 阶段：统计聚合](#8-observe-阶段统计聚合)
- [9. REFLECT 阶段：反思与重规划](#9-reflect-阶段反思与重规划)
- [10. 上下文构建与历史压缩](#10-上下文构建与历史压缩)
- [11. 子 Agent 系统：多级编排](#11-子-agent-系统多级编排)
- [12. 辩论模式](#12-辩论模式)
- [13. 强化学习集成](#13-强化学习集成)
- [14. 辅助组件](#14-辅助组件)
- [15. 推荐阅读顺序](#15-推荐阅读顺序)

---

## 1. 架构总览：双模式 Agent

```
                          用户消息
                             │
                             ▼
                   ┌─────────────────────┐
                   │  agent.mode 配置?    │
                   └──────┬──────┬───────┘
                          │      │
                  simple  │      │  cognitive
                          ▼      ▼
               ┌──────────┐    ┌──────────────────┐
               │ Runtime   │    │ CognitiveAgent   │
               │ (线性循环) │    │ (五阶段循环)      │
               │           │    │                  │
               │ LLM → 工具│    │ PERCEIVE         │
               │  → LLM   │    │  → PLAN          │
               │  → 工具   │    │  → ACT           │
               │  → ...   │    │  → OBSERVE        │
               │           │    │  → REFLECT        │
               └──────────┘    │  → (replan?)      │
                               └──────────────────┘
                                        │
                                 complexity=simple?
                                        │ 是
                                        ▼
                                  委托给 Runtime
```

**设计理念**：简单问题用 Simple 模式直接处理；复杂任务用 Cognitive 模式进行结构化推理，支持任务分解、并行执行、反思和重规划。

---

## 2. Provider 接口：LLM 抽象层

📄 **文件**: `internal/agent/provider.go`

```go
type Provider interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
    Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error)
}

type CompletionRequest struct {
    Model       string              // LLM 模型 ID
    System      string              // 系统提示
    Messages    []CompletionMessage // 对话历史
    Tools       []ToolDefinition    // 可用工具定义
    MaxTokens   int                 // 最大输出 token 数
}

type CompletionResponse struct {
    Text       string           // 生成的文本
    ToolCalls  []ToolUseBlock   // 请求的工具调用
    StopReason StopReason       // 停止原因：end_turn | tool_use | max_tokens
}

type StreamIterator interface {
    Next() (StreamDelta, error)  // 逐 chunk 读取流式响应
    Close()
}
```

`Provider` 是整个系统与 LLM 交互的唯一接口。具体实现 `ClaudeProvider`（`stream.go`）调用 Claude API。所有上层组件（Runtime, CognitiveAgent, Reflector, Planner 等）都通过此接口调用 LLM。

---

## 3. Simple 模式：Runtime 线性循环

📄 **核心文件**: `internal/agent/runtime.go`

### 3.1 Runtime 结构体

```go
type Runtime struct {
    provider       Provider          // LLM 提供者
    tools          *tool.Registry    // 工具注册表
    sessions       *session.Manager  // 会话管理
    db             *store.DB         // SQLite
    cfg            config.AgentConfig
    llmCfg         config.LLMConfig
    approvalFunc   ApprovalFunc      // 工具审批回调
    memStore       memory.Store      // 记忆存储
    skillMgr       *skill.Manager    // 技能管理
    agentMgr       *AgentManager     // 子 Agent 管理
    orchestrator   *AgentOrchestrator
    compressor     *memory.IncrementalCompressor
    memoryBaseDir  string
    resultStore    *tool.ResultStore  // 大结果磁盘缓存
    compressionPipeline *CompressionPipeline
    hookMgr        *hook.Manager     // 钩子事件系统
    permEngine     *tool.PermissionEngine
    agentID        string            // 唯一实例 ID
    parentID       string            // 父 Agent（子 Agent 场景）
    depth          int               // 嵌套深度
    chainID        string            // 调用链 ID
    bgManager      *BackgroundManager
    promptCache    *PromptCache
    agentMCP       *AgentMCPManager  // 每 Agent 的 MCP 服务
}
```

### 3.2 HandleMessage 核心循环

📄 **文件**: `internal/agent/runtime.go` — `HandleMessage` 方法

```
HandleMessage(ctx, channel, msg)
     │
     ├─ 1. 获取/创建 Session（基于 channel + channelID）
     │
     ├─ 2. 构建系统提示（多层组装）：
     │     Personality.md → Core prompt → Rules → 记忆检索结果
     │     → Skills 提示 → Agents 提示
     │
     ├─ 3. 如果历史消息 > 40 条 → CompactHistory（压缩旧消息）
     │
     ├─ 4. 迭代循环（最多 MaxIterations 次）：
     │     │
     │     ├─ a. 创建流式消息更新器 (StreamUpdater)
     │     │
     │     ├─ b. 构建 CompletionRequest：
     │     │      model + system prompt + messages + tools + max_tokens
     │     │
     │     ├─ c. 调用 provider.Stream() → 逐 chunk 收集响应
     │     │      └─ 实时更新 StreamUpdater（用户看到打字效果）
     │     │
     │     ├─ d. 如果 StopReason = end_turn → 结束消息 → break
     │     │
     │     ├─ e. 如果 StopReason = tool_use：
     │     │      ├─ executeTools() → 获取工具结果
     │     │      ├─ 工具结果加入 Session 历史
     │     │      └─ 继续下一轮迭代（带工具结果的上下文）
     │     │
     │     └─ f. 循环回到 step b
     │
     ├─ 5. 持久化 Session
     │
     └─ 6. 保存用户消息到记忆系统
```

### 3.3 系统提示构建

📄 **文件**: `internal/agent/runtime.go` — `buildSystemPrompt` 方法

系统提示按层次组装：

| 层次 | 来源 | 说明 |
|------|------|------|
| 1 | `~/.IronClaw/Soul.md` | 人格/风格定义 |
| 2 | `Agent.md` | Agent 核心指令 |
| 3 | `Memory.md` | 持久化规则 |
| 4 | 记忆检索 | 从 FileMemoryStore 检索相关记忆 |
| 5 | 用户画像 | `LoadUserProfile()` 加载用户画像 |
| 6 | 技能提示 | `skillMgr.BuildPromptSection()` |
| 7 | Agent 提示 | `agentMgr.BuildPromptSection()` |

### 3.4 工具执行

📄 **文件**: `internal/agent/runtime.go` — `executeTools` 方法

```
executeTools(toolCalls)
     │
     ├─ 对每个 ToolCall：
     │   ├─ 从 Registry 获取工具
     │   ├─ 检查权限：PermissionEngine.Evaluate()
     │   │   ├─ allow → 直接执行
     │   │   ├─ deny → 返回拒绝结果
     │   │   └─ ask → 调用 ApprovalFunc（等待用户审批）
     │   ├─ tool.Execute(ctx, input)
     │   ├─ 如果结果过大 → ResultStore 持久化到磁盘
     │   └─ 记录到 tool_log 表
     │
     └─ 返回所有工具结果
```

---

## 4. Cognitive 模式：五阶段认知循环

📄 **核心文件**: `internal/agent/cognitive.go`

### 4.1 CognitiveAgent 结构体

```go
type CognitiveAgent struct {
    runtime            *Runtime           // 简单任务委托
    perceiver          *Perceiver         // PERCEIVE 阶段
    planner            *Planner           // PLAN 阶段
    executor           *Executor          // ACT 阶段
    observer           *Observer          // OBSERVE 阶段
    reflector          *Reflector         // REFLECT 阶段
    sessions           *session.Manager
    db                 *store.DB
    cfg                config.AgentConfig
    memStore           memory.Store
    skillMgr           *skill.Manager
    agentMgr           *AgentManager
    orchestrator       *AgentOrchestrator
    entityExtractor    *graph.LLMEntityExtractor  // 知识图谱实体提取
    rlPolicy           RLPolicy           // 可选 RL 策略
    rlTrainer          RLTrainer          // 可选 RL 训练器
}
```

### 4.2 HandleMessage 五阶段流程

```
CognitiveAgent.HandleMessage(ctx, ch, msg)
     │
     ├─ 1. 获取/创建 Session + 添加用户消息
     ├─ 2. 压缩历史（如果需要）
     │
     ├─ 3. ═══ PERCEIVE ═══（无 LLM 调用）
     │     ├─ 复杂度分类（启发式）
     │     ├─ 记忆检索（session + user scope）
     │     ├─ 知识库检索
     │     └─ 知识图谱检索 + 图谱增强记忆
     │
     ├─ 4. 如果 complexity = simple → 委托给 Runtime.HandleMessage() → 返回
     │
     ├─ 5. 辩论检测：如果匹配辩论关键词 → handleDebate()
     │
     ├─ 6. 多次重规划循环（attempt ≤ maxReplans）：
     │     │
     │     ├─ ═══ PLAN ═══（单次 LLM 调用）
     │     │   └─ 生成 TaskPlan（DAG 子任务 + 依赖关系）
     │     │
     │     ├─ 如果 DirectReply 非空 → 直接流式输出 → break
     │     │
     │     ├─ ═══ ACT ═══（拓扑排序 + 并行执行）
     │     │   └─ 收集 []Observation
     │     │
     │     ├─ ═══ OBSERVE ═══（纯计算）
     │     │   └─ 统计成功/失败/拒绝/进度
     │     │
     │     ├─ ═══ REFLECT ═══（LLM 评估）
     │     │   ├─ 生成最终回答
     │     │   ├─ 判断是否需要重规划
     │     │   └─ 提取经验保存到记忆
     │     │
     │     ├─ 流式输出最终回答
     │     │
     │     └─ 检查重规划：
     │         ├─ confidence < threshold AND needsReplan?
     │         │   ├─ 请求用户决定（continue/adjust/abort）
     │         │   └─ adjust → 修改 UserMessage → 继续循环
     │         └─ 否则 → break
     │
     ├─ 7. RL：记录 episode 经验（异步）
     ├─ 8. 持久化 Session
     └─ 9. 保存到记忆系统
```

---

## 5. PERCEIVE 阶段：感知与分类

📄 **核心文件**: `internal/agent/perceive.go`

### 5.1 CognitiveState 输出

```go
type CognitiveState struct {
    SessionID        string
    UserID           string
    UserMessage      string
    Goal             Goal                      // 意图 + 复杂度
    RelevantMemories []memory.SearchResult     // 相关记忆
    RecentHistory    []CompletionMessage       // 最近对话
    Skills           string                    // 技能提示
    Agents           string                    // Agent 提示
    KnowledgeContext []string                  // 知识库片段
    GraphContext     []string                  // 知识图谱关系
    Personality      string                    // Soul.md
    PersistentRules  string                    // Memory.md
}
```

### 5.2 复杂度分类（启发式，无 LLM）

📄 **文件**: `internal/agent/perceive.go` — `classifyComplexity` 方法

| 复杂度 | 判定条件 |
|--------|---------|
| `Simple` | ≤5 词，无动作关键词 |
| `Moderate` | ≥1 个工具触发词 或 动作关键词 |
| `Complex` | ≥2 个工具触发词 或 ≥3 个动作关键词 或 >40 词 |

Simple 任务直接委托给 Runtime 处理，避免不必要的五阶段开销。

### 5.3 知识图谱增强记忆

📄 **文件**: `internal/agent/perceive.go` — `boostByGraphConnectivity` 方法

```
对每条检索到的记忆：
  记忆分数 × (1 + 0.2 × 图谱连接数)

即：在知识图谱中关联越多的记忆，在检索排序中获得越高的权重。
```

---

## 6. PLAN 阶段：任务分解为 DAG

📄 **核心文件**: `internal/agent/plan.go`

### 6.1 TaskPlan 结构

```go
type TaskPlan struct {
    Summary           string       // 计划摘要
    SubTasks          []*SubTask   // 子任务列表（DAG）
    OverallConfidence float64      // 整体信心度
    DirectReply       string       // 非空 = 跳过 ACT/OBSERVE
    ReplanCount       int          // 已重规划次数
}

type SubTask struct {
    ID          string        // 任务 ID
    Description string        // 描述
    ToolName    string        // 空 = LLM 生成文本
    ToolInput   string        // 原始 JSON
    DependsOn   []string      // 前置依赖任务 ID
    Confidence  float64       // 子任务信心度
    Status      SubTaskStatus // pending | running | done | failed | skipped
}
```

### 6.2 规划流程

```
Planner.Run(ctx, state)
     │
     ├─ 1. 构建 LLM 输入：
     │     USER_REQUEST + TOOLS + MEMORIES + KNOWLEDGE + GRAPH + HISTORY
     │
     ├─ 2. 系统提示要求：
     │     "输出 JSON 格式。子任务必须形成合法 DAG（无环）。
     │      如果可以直接回复，设置 direct_reply；否则填充 sub_tasks。"
     │
     ├─ 3. 解析响应：尝试 直接 JSON → ```json 代码块 → 第一个 {...}
     │
     └─ 4. 验证：
           ├─ 工具名在 Registry 中存在
           ├─ DAG 无环检测（Kahn 算法）
           └─ 验证失败 → 回退为 DirectReply
```

### 6.3 DAG 验证：Kahn 算法

📄 **文件**: `internal/agent/plan.go` — `validateDAG` 方法

使用 Kahn 拓扑排序算法检测环：
1. 计算每个节点的入度
2. 将入度为 0 的节点加入队列
3. BFS 处理，每处理一个节点将后继节点入度减 1
4. 如果最终处理的节点数 ≠ 总节点数 → 存在环

---

## 7. ACT 阶段：拓扑排序 + 并行执行

📄 **核心文件**: `internal/agent/act.go`

### 7.1 Observation 结构

```go
type Observation struct {
    SubTaskID  string  // 子任务 ID
    ToolName   string  // 使用的工具
    Input      string  // 输入
    Output     string  // 输出
    Error      string  // 错误
    DurationMs int64   // 执行时长(ms)
    Denied     bool    // 用户拒绝了审批
}
```

### 7.2 拓扑执行循环

📄 **文件**: `internal/agent/act.go` — `RunWithContext` 方法

```
Executor.RunWithContext(ctx, ch, sess, target, plan, taskCtx, rlState, collector)
     │
     ├─ maxParallel = cfg.MaxParallelTools（默认 3）
     │
     └─ 循环直到无就绪任务：
           │
           ├─ collectReady：找出所有 pending 且前置依赖已完成的任务
           │
           ├─ 如果 ready 数 > maxParallel → 截断为 maxParallel 个
           │
           ├─ 标记所有 ready 任务为 running
           │
           ├─ 并行执行（sync.WaitGroup）：
           │   │
           │   └─ 对每个 ready 任务 → go func {
           │         ├─ executeSubTask(...)
           │         │   ├─ 检查工具存在
           │         │   ├─ 检查审批（RequiresApproval + approvalFunc）
           │         │   ├─ agent_* 工具：注入 TaskContext（前置输出）
           │         │   ├─ 执行工具
           │         │   ├─ 记录到 DB + Session
           │         │   └─ 失败 → 标记下游任务为 skipped
           │         │
           │         └─ 发送进度：[3/5] 描述... ✅/❌
           │       }
           │
           └─ wg.Wait() → 收集所有 Observation
```

### 7.3 关键特性

- **DAG 执行**：拓扑排序确保依赖关系被尊重
- **并行批处理**：最多 `MaxParallelTools` 个并发执行
- **下游跳过**：任务失败时，所有依赖它的后续任务标记为 `skipped`
- **Agent 上下文传递**：`agent_*` 工具的前置任务输出注入到输入中
- **RL 集成**：每个工具执行记录 bandit experience

---

## 8. OBSERVE 阶段：统计聚合

📄 **核心文件**: `internal/agent/observe.go`

### 8.1 ObservationResult

```go
type ObservationResult struct {
    Observations    []Observation
    SuccessCount    int
    FailureCount    int
    DeniedCount     int
    OverallProgress float64    // SuccessCount / (total - skipped)
    ErrorPatterns   []string   // ["all_denied", "permission_error", "network_error", "tool_not_found"]
}
```

### 8.2 处理逻辑（纯计算，无 LLM）

```
Observer.Run(observations, plan)
     │
     ├─ 统计 success / failure / denied 计数
     ├─ 计算进度 = 成功数 / (总数 - 跳过数)
     └─ 分类错误模式（权限、网络、工具不存在等）
```

**错误模式检测**用于帮助 REFLECT 阶段理解失败原因。

---

## 9. REFLECT 阶段：反思与重规划

📄 **核心文件**: `internal/agent/reflect.go`

### 9.1 Reflection 结构

```go
type Reflection struct {
    OverallConfidence   float64   // 整体信心度
    Succeeded           bool      // 是否成功
    LessonsLearned      []string  // 学到的教训
    SuggestedAdjustment string    // 重规划建议
    FinalAnswer         string    // 用户可见的最终回答
    NeedsReplan         bool      // 是否需要重规划
    ReplanReason        string    // 重规划原因
}
```

### 9.2 反思流程

```
Reflector.Run(ctx, ch, target, state, plan, obsResult)
     │
     ├─ 1. 构建 LLM 输入：GOAL + PLAN_SUMMARY + OBSERVATIONS + STATS
     │
     ├─ 2. 系统提示：
     │     "评估任务是否成功。如果 confidence < 0.5 且有显著失败，设 needs_replan = true。"
     │
     ├─ 3. 解析 LLM 响应为 Reflection JSON
     │
     └─ 4. 异步写入经验（fire-and-forget）：
           ├─ 如果有 FactExtractor → 提取 facts → LifecycleManager → 记忆
           ├─ 否则 → 保存 cognitive_experience 原始条目
           └─ 提取知识图谱实体
```

### 9.3 重规划审批

📄 **文件**: `internal/agent/reflect.go` — `RequestReplanApproval` 方法

```
如果 channel 实现了 ReflectionSender 接口：
   → 发送交互式选择界面（Telegram inline keyboard / TUI 快捷键）
   → 用户选择：continue | adjust | abort

如果 channel 未实现：
   → 自动返回 ReplanContinue（继续执行）
```

---

## 10. 上下文构建与历史压缩

### 10.1 消息格式转换

📄 **文件**: `internal/agent/context.go`

```go
type CompletionMessage struct {
    Role       string         // "user" | "assistant" | "tool_use" | "tool_result"
    Content    string
    ToolUseID  string         // tool_result 的关联 ID
    ToolName   string         // 工具名
    ToolInput  string         // 原始 JSON
    ToolBlocks []ToolUseBlock // 合并到 assistant 消息中的工具调用
}
```

**BuildMessages(session)** 将 Session 历史转换为 Anthropic API 格式：
- `user` → `CompletionMessage{Role: "user"}`
- `assistant` + 后续 `tool_use` → 合并为单个 assistant 消息（ToolBlocks 字段）
- `tool_result` → `CompletionMessage{Role: "user", ToolUseID: ...}`

**safeTrimHistory** 确保 tool_use/tool_result 配对不被截断，且第一条消息始终为 `user`。

### 10.2 历史压缩

📄 **文件**: `internal/agent/compaction.go`

```
CompactHistory(ctx, provider, session, model)
     │
     ├─ 如果历史 ≤ 40 条（compactionThreshold）→ 跳过
     │
     ├─ 取截断点 = len(history) / 2
     │   └─ 调整截断点以不拆分 tool_use/tool_result 配对
     │
     ├─ 将旧消息发给 LLM 总结：
     │   "简洁总结以下对话历史，保留关键事实、决策和上下文。"
     │
     └─ 用单条总结消息替换旧消息：
         Role: "user"
         Content: "[Previous conversation summary]: " + summaryText
```

---

## 11. 子 Agent 系统：多级编排

### 11.1 AgentSpec — 子 Agent 定义

📄 **文件**: `internal/agent/spec.go`

```go
type AgentSpec struct {
    Name          string   // 唯一标识
    Description   string   // 功能描述
    SystemPrompt  string   // Agent 专属系统提示
    Model         string   // 可选 LLM 覆盖
    MaxIterations int      // 默认 5
    Tools         []string // 工具白名单（空 = 所有非 agent_* 工具）
    Tags          []string // 路由标签
    Mode          string   // "simple" | "cognitive"
    Timeout       duration // 执行超时

    ExecutionMode   ExecutionMode   // "spawn" | "fork" | "background"
    PermissionMode  PermissionMode  // "" | "bubble" | "accept_edits" | "bypass"
    InheritContext  bool            // fork 模式：继承父上下文
    Backend         BackendType     // "in_process" | "subprocess" | "docker"
    Hooks           AgentHookConfig // 生命周期钩子
    MCPServers      []AgentMCPConfig // 每 Agent 的 MCP 服务
}
```

### 11.2 三种执行模式

| 模式 | 说明 | 实现 |
|------|------|------|
| `spawn` | 独立 Runtime，独立 Session | `executeSpawn()` — 创建新 Runtime + captureChannel |
| `fork` | 继承父 Runtime 的 Session 上下文 | `executeFork()` — 共享消息历史，最大深度 3 层 |
| `background` | 异步 goroutine | `executeBackground()` — 非阻塞，后台运行 |

### 11.3 AgentTool — 工具包装器

📄 **文件**: `internal/agent/agent_tool.go`

子 Agent 被包装为 `tool.Tool` 接口实现，注册到 Registry 中，名称格式为 `agent_{name}`。

```
AgentTool.Execute(ctx, input)
     │
     ├─ 检查断路器（CircuitBreaker）
     ├─ 解析输入：{"task": "...", "context": "..."}
     ├─ 应用超时
     │
     └─ 根据 ExecutionMode 分发：
         ├─ spawn → 创建独立 Runtime
         │   ├─ 构建 scopedTools（排除 agent_* 防递归）
         │   ├─ 创建 captureChannel 捕获输出
         │   └─ subRuntime.HandleMessage()
         │
         ├─ fork → 继承父 Runtime
         │   ├─ 检查 fork 深度（≤ MaxForkDepth=3）
         │   ├─ 构建 SubagentContext
         │   │   ├─ 工具注册表（scoped）
         │   │   ├─ 权限模式
         │   │   ├─ 父消息历史（只读）
         │   │   └─ AgentID, ParentID, Depth, ChainID
         │   └─ 注入 context → subRuntime.HandleMessage()
         │
         └─ background → 异步执行
             └─ BackgroundManager.Spawn()
```

### 11.4 SubagentContext — 隔离与继承

📄 **文件**: `internal/agent/subagent_context.go`

```go
type SubagentContext struct {
    // --- 隔离 ---
    ToolRegistry *tool.Registry     // 受限工具集
    Permission   PermissionMode     // 审批行为
    Cancel       context.CancelFunc // 子取消（不影响父）

    // --- 继承（只读）---
    ParentMessages []session.Message // fork 模式共享历史
    SystemPrompt   string           // fork 模式共享提示
    Memory         memory.Store
    Sessions       *session.Manager
    DB             *store.DB

    // --- 追踪 ---
    AgentID  string  // 唯一调用 ID
    ParentID string  // 父 Agent
    Depth    int     // 嵌套层级（最大 3）
    ChainID  string  // 整个调用链的分组 ID
}
```

### 11.5 TaskContext — 多 Agent 结果传递

📄 **文件**: `internal/agent/task_context.go`

```go
type TaskContext struct {
    mu         sync.RWMutex
    ID         string
    Goal       string
    Results    map[string]SubAgentResult // 子任务 ID → 结果
    SharedData map[string]string         // 任意 KV 存储
}

// BuildContextForTask 收集所有依赖任务的输出，格式化注入当前任务输入：
// "Context from previous tasks:
//  --- Task t1 (agent_name) ---
//  <output>"
```

### 11.6 AgentOrchestrator — 多 Agent 调度

📄 **文件**: `internal/agent/orchestrator.go`

支持并行和 DAG 模式的多 Agent 任务调度。

---

## 12. 辩论模式

📄 **文件**: `internal/agent/debate.go`

当用户消息匹配辩论关键词时触发：

```
handleDebate(ctx, ch, target, sess, state)
     │
     ├─ 1. 选择 proposer + critic Agent
     │
     ├─ 2. BuildDebatePlan(topic, proposer, critic, maxRounds)：
     │     t1: proposer 提出观点
     │     t2: critic 批评（依赖 t1）
     │     t3: proposer 反驳（依赖 t2）
     │     ... 最多 maxRounds 轮
     │
     ├─ 3. 通过 ACT 阶段执行辩论计划
     │
     ├─ 4. OBSERVE + REFLECT 阶段综合辩论结果
     │
     └─ 5. 流式输出最终综合回答
```

---

## 13. 强化学习集成

📄 **文件**: `internal/agent/rl_helpers.go`

### 13.1 RLPolicy 接口

```go
type RLPolicy interface {
    IsEnabled() bool
    SelectTool(ctx, state, toolNames) *rl.ToolSelectionAction       // Bandit 级
    UpdateToolSelection(ctx, state, toolName, reward) error
    SelectPlanStrategy(state) *rl.PlanStrategyAction                // PPO 级
    SelectReplanAction(state) rl.ReplanActionType                   // DQN 级
}

type RLTrainer interface {
    AddExperience(exp rl.Experience)
    RecordEpisode(ctx, params rl.EpisodeParams) error
}
```

### 13.2 三层 RL 集成

| 层级 | 算法 | 决策点 | 奖励信号 |
|------|------|--------|---------|
| Bandit | Thompson Sampling | 工具选择 | 工具执行成功/拒绝/时长 |
| PPO | Proximal Policy Optimization | 规划策略 | 计划信心度 → episode 结果 |
| DQN | Deep Q-Network | 重规划决策 | 重规划动作 → episode 结果 |

### 13.3 奖励计算

```
episode_reward =
    if reflection.Succeeded { +1.0 } else { -1.0 }
    + obsResult.OverallProgress × 0.5
```

---

## 14. 辅助组件

### 14.1 CircuitBreaker — 断路器

📄 **文件**: `internal/agent/circuit_breaker.go`

防止故障子 Agent 级联传播：

| 状态 | 说明 |
|------|------|
| `Closed` | 正常运行 |
| `Open` | 连续 3 次失败 → 拒绝请求 60 秒 |
| `HalfOpen` | 60 秒后允许少量请求测试恢复 |

### 14.2 BackgroundManager — 后台执行

📄 **文件**: `internal/agent/background.go`

```go
type BackgroundManager struct {
    agents   map[string]*BackgroundAgent
    notifyCh chan AgentStatus
}
// Spawn(ctx, spec, runner) → agentID
// GetResult(agentID) → (*AgentResult, bool)
// Wait(agentID) → 等待完成
```

### 14.3 TraceCollector — 执行追踪

📄 **文件**: `internal/agent/trace.go`

```go
type Trace struct {
    ID        string
    ParentID  string
    AgentName string
    Input     string
    Output    string
    Error     string
    StartedAt time.Time
    EndedAt   time.Time
    Children  []*Trace  // 树形结构
}
```

收集执行轨迹用于调试和审计。

### 14.4 PromptCache — 提示缓存

📄 **文件**: `internal/agent/runtime.go` — `PromptCache` 字段

缓存构建好的系统提示，避免重复组装。

---

## 15. 推荐阅读顺序

### 第一层：核心接口和数据流

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 1 | `provider.go` | `Provider`, `CompletionRequest/Response` — LLM 接口抽象 |
| 2 | `cognitive_types.go` | `CognitiveState`, `TaskPlan`, `SubTask`, `Reflection` — 核心数据结构 |
| 3 | `stream.go` | `ClaudeProvider`, `StreamIterator` — 流式实现 |

### 第二层：Simple 模式

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 4 | `runtime.go` | `Runtime.HandleMessage` — 线性循环核心 |
| 5 | `context.go` | `BuildMessages`, `safeTrimHistory` — 消息格式转换 |
| 6 | `compaction.go` | `CompactHistory` — 历史压缩 |

### 第三层：Cognitive 模式五阶段

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 7 | `cognitive.go` | `CognitiveAgent.HandleMessage` — 五阶段编排 |
| 8 | `perceive.go` | `Perceiver.Run` — 无 LLM 的感知分类 |
| 9 | `plan.go` | `Planner.Run` — DAG 任务规划 + Kahn 算法验证 |
| 10 | `act.go` | `Executor.RunWithContext` — 拓扑并行执行 |
| 11 | `observe.go` | `Observer.Run` — 统计聚合 |
| 12 | `reflect.go` | `Reflector.Run` — 反思 + 重规划决策 |

### 第四层：子 Agent 系统

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 13 | `spec.go` | `AgentSpec` — 子 Agent 定义 |
| 14 | `agent_tool.go` | `AgentTool` — 工具包装 + spawn/fork/background |
| 15 | `subagent_context.go` | `SubagentContext` — 隔离与继承 |
| 16 | `task_context.go` | `TaskContext` — 结果传递 |
| 17 | `agent_manager.go` | `AgentManager` — Spec 注册管理 |
| 18 | `orchestrator.go` | `AgentOrchestrator` — 多 Agent 调度 |

### 第五层：辅助系统

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 19 | `debate.go` | 辩论模式 |
| 20 | `circuit_breaker.go` | 断路器 |
| 21 | `rl_helpers.go` | RL 状态构建 |
| 22 | `trace.go` | 执行追踪 |
| 23 | `fork.go` | Fork 上下文继承 |
| 24 | `background.go` | 后台异步执行 |
