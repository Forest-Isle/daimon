# 03 - Agent 系统（核心大脑）

## 文件结构

```
internal/agent/
├── provider.go          # LLM 提供者接口 + 类型定义
├── stream.go            # Claude SDK 流式适配器
├── runtime.go           # Simple 模式运行时
├── context.go           # 消息构建辅助
├── compaction.go        # 历史压缩
├── cognitive.go         # Cognitive 模式主循环
├── cognitive_types.go   # 认知模式类型定义
├── cognitive_prompts.go # 认知模式提示词
├── perceive.go          # PERCEIVE 阶段
├── plan.go              # PLAN 阶段
├── act.go               # ACT 阶段
├── observe.go           # OBSERVE 阶段
├── reflect.go           # REFLECT 阶段
├── debate.go            # 辩论模式（多 Agent 对抗）
├── agent_tool.go        # 子 Agent 作为工具调用
├── agent_manager.go     # 多 Agent 管理器
├── spec.go              # Agent 规格定义
├── task_context.go      # 任务上下文（多Agent协作）
├── circuit_breaker.go   # 熔断器
├── aggregator.go        # 结果聚合
├── trace.go             # 追踪/日志
└── rl_helpers.go        # RL 辅助函数
```

## 一、Provider 接口层

### 核心接口

```go
type Provider interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
    Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error)
}
```

### 关键类型

```
CompletionRequest
├── Model       string
├── System      string              # 系统提示词
├── Messages    []CompletionMessage  # 对话历史
├── Tools       []ToolDefinition     # 可用工具
└── MaxTokens   int

CompletionResponse
├── Text       string
├── ToolCalls  []ToolUseBlock
└── StopReason StopReason (end_turn | tool_use | max_tokens)

StreamDelta
├── Text       string
├── ToolCall   *ToolUseBlock   # 单个工具调用（兼容）
├── ToolCalls  []ToolUseBlock  # 所有工具调用
├── Done       bool
└── StopReason StopReason
```

### ClaudeProvider（stream.go）

通过 Anthropic SDK 实现 Provider 接口，支持：
- 同步补全 (`Complete`)
- 流式输出 (`Stream`)
- 工具调用解析

## 二、Simple 模式（Runtime）

### 架构

```
用户消息
    │
    ▼
┌──────────────────────┐
│ 1. 获取/创建 Session  │
│ 2. 添加用户消息       │
│ 3. 构建系统提示       │
│ 4. 压缩历史（如需要） │
└──────────┬───────────┘
           │
    ┌──────▼──────┐
    │ Agent Loop  │ ◄── 最多 max_iterations 次
    │             │
    │ LLM 调用    │
    │    │        │
    │    ├── 无工具调用 → 完成，输出回复
    │    │
    │    └── 有工具调用 → 执行工具
    │         │
    │         ├── 需要审批? → handleApproval()
    │         ├── 执行工具 → 记录结果
    │         └── 下一轮迭代
    └─────────────┘
           │
    ┌──────▼──────┐
    │ 持久化 Session │
    │ 保存 Memory    │
    └──────────────┘
```

### 系统提示词构建（buildSystemPrompt）

系统提示词由 **7 个层次** 组成：

```
┌─────────────────────────────────────┐
│ 1. Personality (Soul.md)            │  ← 人格/风格
├─────────────────────────────────────┤
│ 2. Core System Prompt (config)      │  ← 核心指令
├─────────────────────────────────────┤
│ 3. Persistent Rules (Memory.md)     │  ← 长期规则
├─────────────────────────────────────┤
│ 4. Relevant Memories (retrieval)    │  ← 运行时检索
├─────────────────────────────────────┤
│ 5. User Profile                     │  ← 用户画像
├─────────────────────────────────────┤
│ 6. Skills                           │  ← 匹配的技能
├─────────────────────────────────────┤
│ 7. Available Agents                 │  ← 可用子代理
└─────────────────────────────────────┘
```

### 流式输出

每次迭代创建独立的 `StreamUpdater`：
- 文本增量通过 `Update()` 推送
- 工具调用时先 `Finish("🔧 Calling tools...")`
- 下一轮迭代创建新的流式消息

## 三、Cognitive 模式（五阶段认知循环）

### 整体架构

```
┌────────────────────────────────────────────────────────────┐
│                    CognitiveAgent                          │
│                                                            │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐              │
│  │Perceiver │──▶│ Planner  │──▶│ Executor │              │
│  └──────────┘   └──────────┘   └──────────┘              │
│       ▲              │              │                      │
│       │              │              ▼                      │
│       │         ┌────┘        ┌──────────┐                │
│       │         │             │ Observer  │                │
│       │         │             └────┬─────┘                │
│       │         │                  │                       │
│       │         │             ┌────▼─────┐                │
│       │         │             │Reflector │                │
│       │         │             └────┬─────┘                │
│       │         │                  │                       │
│       │         │    confidence < threshold?               │
│       │         │         │ YES        │ NO                │
│       │         │    ┌────▼────┐  ┌───▼───┐               │
│       │         │    │ Replan  │  │ Done  │               │
│       │         │    │ Request │  └───────┘               │
│       │         │    └────┬────┘                           │
│       │         │         │                                │
│       └─────────┴─────────┘ (adjust + continue)           │
│                                                            │
│  内嵌: Simple Runtime (处理 simple 复杂度任务)              │
│  内嵌: RL Policy + Trainer (可选强化学习)                   │
└────────────────────────────────────────────────────────────┘
```

### HandleMessage 主流程

```
HandleMessage(ctx, ch, msg)
    │
    ├── 1. 获取 Session + 添加用户消息
    ├── 2. 压缩历史
    │
    ├── 3. PERCEIVE → CognitiveState
    │   ├── 注入 Skills、Agents
    │   ├── 注入 Personality、PersistentRules
    │   │
    │   ├── 复杂度 = simple?
    │   │   └── YES → 委托给 runtime.HandleMessage() → 返回
    │   │
    │   └── 需要辩论?
    │       └── YES → handleDebate() → 返回
    │
    ├── 4. 初始化 RL 状态 (if enabled)
    │
    ├── 5. 【主循环】(最多 maxReplans 次)
    │   │
    │   ├── PLAN → TaskPlan
    │   │   ├── RL: PPO 策略调整
    │   │   └── DirectReply? → 跳过 ACT/OBSERVE
    │   │
    │   ├── ACT → []Observation
    │   │   └── 执行子任务（带依赖关系）
    │   │
    │   ├── OBSERVE → ObservationResult
    │   │   └── 统计 success/failure/denied
    │   │
    │   ├── REFLECT → Reflection
    │   │   ├── 生成最终回答
    │   │   ├── 事实提取 + 生命周期管理
    │   │   └── RL: 更新 reflection confidence
    │   │
    │   └── confidence < threshold + NeedsReplan?
    │       ├── 请求用户决定 (abort/continue/adjust)
    │       └── adjust → 修改目标，继续循环
    │
    ├── 6. RL: 记录 Episode
    ├── 7. 持久化 Session
    └── 8. 保存 Memory
```

### 3.1 PERCEIVE 阶段（perceive.go）

**输入**：用户消息 + Session
**输出**：`CognitiveState`
**LLM 调用**：❌ 无（纯本地启发式）

```go
type CognitiveState struct {
    SessionID        string
    UserMessage      string
    Goal             Goal              // 解析的意图 + 复杂度
    RelevantMemories []SearchResult    // 记忆检索结果
    RecentHistory    []CompletionMessage
    KnowledgeContext []string          // 知识库片段
    GraphContext     []string          // 知识图谱关系
    Skills           string            // 技能提示
    Agents           string            // 代理提示
    Personality      string            // Soul.md
    PersistentRules  string            // Memory.md
}
```

复杂度评估（本地关键词匹配）：
- `simple`：简单问候/感谢/是否问题
- `moderate`：包含 write/create/search 等动作词
- `complex`：包含工具触发词 + 动作词

### 3.2 PLAN 阶段（plan.go）

**输入**：`CognitiveState`
**输出**：`TaskPlan`
**LLM 调用**：✅ 1 次（不含 Tools 参数，纯规划）

```go
type TaskPlan struct {
    Summary           string
    SubTasks          []*SubTask    // 子任务列表
    OverallConfidence float64       // 0.0-1.0
    DirectReply       string        // 非空则跳过 ACT
    ReplanCount       int
}

type SubTask struct {
    ID          string
    Description string
    ToolName    string    // 空 = LLM 直接生成文本
    ToolInput   string    // JSON
    DependsOn   []string  // 依赖的子任务 ID
    Confidence  float64
    Status      SubTaskStatus
}
```

### 3.3 ACT 阶段（act.go）

**输入**：`TaskPlan` + Channel + Session
**输出**：`[]Observation`
**LLM 调用**：❌ 无（工具执行）

按子任务依赖顺序执行：
- 检查 DependsOn 是否都已完成
- 需要审批的工具通过 Channel 交互
- 支持并行工具执行（`MaxParallelTools`）
- RL Bandit 可影响工具选择

### 3.4 OBSERVE 阶段（observe.go）

**输入**：`[]Observation` + `TaskPlan`
**输出**：`ObservationResult`
**LLM 调用**：❌ 无（纯统计）

```go
type ObservationResult struct {
    Observations    []Observation
    SuccessCount    int
    FailureCount    int
    DeniedCount     int
    OverallProgress float64    // 0.0-1.0
    ErrorPatterns   []string   // 错误模式提取
}
```

### 3.5 REFLECT 阶段（reflect.go）

**输入**：`CognitiveState` + `TaskPlan` + `ObservationResult`
**输出**：`Reflection`
**LLM 调用**：✅ 1 次

```go
type Reflection struct {
    OverallConfidence   float64
    Succeeded           bool
    LessonsLearned      []string
    SuggestedAdjustment string
    FinalAnswer         string     // 给用户的最终回答
    NeedsReplan         bool
    ReplanReason        string
}
```

反射阶段还触发：
- 事实提取（`LLMFactExtractor`）
- 生命周期管理（ADD/UPDATE/DELETE/NOOP）
- 知识图谱实体提取
- Replan 审批请求（通过 Channel）

## 四、辩论模式（debate.go）

当检测到比较/决策类问题（"compare", "vs", "which"）且有 2+ 个代理时触发：

```
shouldDebate(state)
    │ 关键词匹配: compare/versus/vs/better/worse/pros and cons/...
    │
    ▼
handleDebate()
    │
    ├── SelectDebateAgents()     # 选择 proposer + critic
    ├── BuildDebatePlan()        # 构建辩论计划（多轮）
    ├── executor.RunWithContext() # 执行辩论
    ├── SynthesizeDebate()       # 综合辩论结果
    └── reflector.Run()          # 生成最终答案
```

## 五、多 Agent 系统（agent_manager.go）

```go
type AgentSpec struct {
    Name          string
    Description   string
    SystemPrompt  string
    Model         string
    MaxTokens     int
    MaxIterations int
    Tools         []string   // 允许使用的工具名
    Tags          []string
    Mode          string     // "simple" | "cognitive"
}
```

Agent 定义来源：
1. `~/.IronClaw/agents/` 目录下的 YAML 文件
2. 配置文件中的内联定义（`agents.definitions`）
3. 额外目录（`agents.extra_dirs`）

每个 Agent 作为 Tool 注册到 Registry，可被主 Agent 或其他 Agent 调用。

## 六、历史压缩（compaction.go）

当会话历史过长时，用 LLM 将旧消息压缩为摘要：

```
CompactHistory(ctx, provider, sess, model)
    │
    ├── 消息数 > 阈值?
    │   └── NO → 直接返回
    │
    ├── 将旧消息发送给 LLM 生成摘要
    ├── 替换旧消息为摘要消息
    └── 保留最近 N 条消息不变
```

## 七、RL 集成（rl_helpers.go）

```
buildInitialRLState()     # PERCEIVE 后构建初始状态
updateRLStateWithPlan()   # PLAN 后更新
updateRLStateWithObservation() # OBSERVE 后更新
computeSimpleEpisodeReward()  # 计算回合奖励
```

三层 RL 系统在 Agent 中的作用点：
- **Bandit**：ACT 阶段工具选择
- **PPO**：PLAN 阶段策略调整
- **DQN**：REFLECT 阶段 replan 决策
