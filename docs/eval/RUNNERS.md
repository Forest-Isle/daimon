# 执行引擎（Runners）

> 源文件：`internal/eval/dry_runner.go`、`internal/eval/cognitive_runner.go`、`internal/eval/eval_channel.go`、`internal/eval/eval_hook.go`

---

## 目录

1. [模块职责](#1-模块职责)
2. [AgentRunner 接口](#2-agentrunner-接口)
3. [DryRunner — 无 LLM 快速验证](#3-dryrunner--无-llm-快速验证)
4. [CognitiveAgentRunner — 真实 Agent 驱动](#4-cognitiveagentrunner--真实-agent-驱动)
5. [EvalChannel — 无头 Channel](#5-evalchannel--无头-channel)
6. [EvalHook — 进化事件采集](#6-evalhook--进化事件采集)
7. [RunTask 内部执行流程](#7-runtask-内部执行流程)
8. [评测 Gateway 隔离配置](#8-评测-gateway-隔离配置)
9. [MemoryAwareRunner — 记忆 Fixture 注入](#9-memoryawarerunner--记忆-fixture-注入)

---

## 1. 模块职责

执行引擎层负责将 `TaskCase` 转化为 `EvalResult`。系统提供两种 Runner，通过 `--live` 标志在 CLI 层选择：

```
--live=false  →  DryRunner          （合成结果，无 LLM 调用）
--live=true   →  CognitiveAgentRunner（真实 LLM + CognitiveAgent）
```

---

## 2. AgentRunner 接口

```go
type AgentRunner interface {
    RunTask(ctx context.Context, task TaskCase) (*EvalResult, error)
}
```

这是评测系统的**核心执行接口**。所有 Runner 实现此接口，Harness 对具体实现无感知。

可选扩展接口（通过类型断言检测）：

| 接口 | 实现者 | 用途 |
|------|--------|------|
| `SnapshotCaptor` | `CognitiveAgentRunner` | 采集进化系统快照 |
| `CogHealthCaptor` | `CognitiveAgentRunner` | 采集认知健康报告 |
| `MemoryAwareRunner` | `CognitiveAgentRunner` | 注入/清理记忆 Fixture |

---

## 3. DryRunner — 无 LLM 快速验证

### 3.1 行为

```go
type DryRunner struct{}

func (d *DryRunner) RunTask(_ context.Context, task TaskCase) (*EvalResult, error) {
    // 不调用任何 LLM API
    // 合成输出：优先用 Reference.Answer，其次拼接 MustContain 词
    agentOutput := fmt.Sprintf("Dry-run result for task %s: all checks passed.", task.ID)
    if task.Reference != nil && task.Reference.Answer != "" {
        agentOutput = task.Reference.Answer
    } else if len(task.Reference.MustContain) > 0 {
        agentOutput = strings.Join(task.Reference.MustContain, "\n")
    }

    return &EvalResult{
        Success:           true,
        AssertionPassRate: 1.0,
        Confidence:        0.95,
        ToolsUsed:         task.ExpectTools,  // 直接取期望工具列表
        // ...
    }, nil
}
```

### 3.2 特点与用途

| 特性 | 值 |
|------|-----|
| LLM 调用 | 无 |
| 执行时间 | 微秒级 |
| 结果 | 永远 Success=true |
| AssertionPassRate | 固定 1.0 |
| 进化快照 | 无（不实现 SnapshotCaptor） |
| 适用场景 | CI PR 门控、框架冒烟测试、任务定义验证 |

**注意**：DryRunner 的 `VerifyReference` 结果依然是真实计算的（Harness 中调用），因为 DryRunner 的输出会尽量匹配 `Reference`。

---

## 4. CognitiveAgentRunner — 真实 Agent 驱动

### 4.1 结构

```go
type CognitiveAgentRunner struct {
    agent        *agent.CognitiveAgent
    hook         *EvalHook            // 进化事件采集器
    channel      *EvalChannel         // 无头 Channel
    cogCollector *cogmetrics.Collector // 认知指标采集器
    memStore     memory.Store          // 记忆存储（Fixture 注入用）

    mu              sync.Mutex
    lastObservation *agent.ObservationResult
}
```

### 4.2 初始化

```go
func NewCognitiveAgentRunner(ca *agent.CognitiveAgent) *CognitiveAgentRunner {
    r := &CognitiveAgentRunner{agent: ca, channel: &EvalChannel{}}

    // 注册 Observation 回调，实时捕获 OBSERVE 阶段 Assertion 统计
    ca.SetObservationCallback(func(result *agent.ObservationResult) {
        r.mu.Lock()
        r.lastObservation = result
        r.mu.Unlock()
    })

    // 挂载 EvalHook 到进化引擎（若 Agent 有进化引擎）
    if evo := ca.EvolutionEngine(); evo != nil {
        r.hook = NewEvalHook()
        evo.RegisterHook(r.hook)
    }
    return r
}
```

由 `gateway.NewEvalRunner` 在初始化后追加：

```go
runner.SetCogCollector(collector)      // 认知指标采集器
runner.SetMemoryStore(memStore)        // 记忆存储（用于 InjectMemory）
runner.SetDashboardEmitter(emitter)    // 多路复用 Emitter（dashboard + EvalHook 压缩事件）
```

### 4.3 CaptureSnapshot

```go
func (r *CognitiveAgentRunner) CaptureSnapshot() *EvolutionSnapshot {
    // 从进化引擎读取：
    // - PreferenceLearner.ListPreferences()
    // - StrategyOptimizer.Version() / GetReplanThreshold() / GetToolPriorities()
    // - SkillSynthesizer (draft 文件计数)
    // - TrajectoryRecorder (轨迹文件计数)
    // - ReadTrajectories + ConvertTrajectories → RL 统计
    // - PreferenceLearner → 偏好质量分布
    // - ModelRouter → 近期路由决策
}
```

### 4.4 CaptureCogHealth

```go
func (r *CognitiveAgentRunner) CaptureCogHealth() *cogmetrics.HealthReport {
    if r.cogCollector == nil { return nil }
    return r.cogCollector.HealthReport()
}
```

---

## 5. EvalChannel — 无头 Channel

### 5.1 设计目标

EvalChannel 实现了 `channel.Channel` 接口，使 CognitiveAgent 能在没有用户界面的情况下运行。关键特性：

- **自动审批工具调用**（`ApprovalSender`）：所有工具调用请求返回 `true`
- **智能重规划决策**（`ReflectionSender`）：confidence < 0.6 时允许 replan
- **输出采集**：所有 `Send` 调用的消息缓存至 `messages`
- **流式写入无头处理**：`evalStreamUpdater` 在 `Finish` 时将最终文本提交到消息队列

### 5.2 接口实现

```go
// 实现的接口
var _ channel.Channel         = (*EvalChannel)(nil)
var _ channel.ApprovalSender  = (*EvalChannel)(nil)
var _ channel.ReflectionSender = (*EvalChannel)(nil)

// 自动审批所有工具调用
func (c *EvalChannel) SendApprovalRequest(...) (bool, error) {
    return true, nil
}

// 重规划决策：confidence < 0.6 允许 ReplanAdjust
func (c *EvalChannel) SendReflectionRequest(..., confidence float64) (channel.ReplanDecision, error) {
    if confidence < 0.6 {
        return channel.ReplanAdjust, nil
    }
    return channel.ReplanContinue, nil
}
```

### 5.3 消息采集

```go
// 采集所有输出消息（线程安全）
func (c *EvalChannel) Messages() []channel.OutboundMessage

// 返回最后一条消息文本（即 Agent 最终输出）
func (c *EvalChannel) LastMessage() string

// 任务间重置（避免跨任务消息污染）
func (c *EvalChannel) Reset()
```

---

## 6. EvalHook — 进化事件采集

### 6.1 设计

`EvalHook` 实现 `evolution.Hook` 接口，注册到进化引擎后，以 **per-session** 的方式缓冲三类进化事件和压缩事件：

```go
type EvalHook struct {
    reflections  map[string]*evolution.ReflectionEvent   // sessionID → 反思事件
    episodes     map[string]*evolution.EpisodeEvent       // sessionID → 完整 Episode
    toolExecs    map[string][]evolution.ToolExecEvent     // sessionID → 工具执行列表
    compressions map[string][]CompressionEvent            // sessionID → 压缩事件列表
}
```

### 6.2 四类事件

| 方法 | 触发时机 | 内容 |
|------|----------|------|
| `OnReflectionComplete` | CognitiveAgent REFLECT 阶段完成 | 目标、复杂度、成功/失败、Lessons、工具列表、重规划次数 |
| `OnEpisodeComplete` | Agent 完整 Episode 结束 | RL 奖励、进度、行动序列 |
| `OnToolExecuted` | 每次工具调用完成 | 工具名、成功/失败、耗时 |
| `RecordCompression` | 上下文压缩触发 | 原因、层数、压缩前后百分比 |

### 6.3 压缩事件的路由

压缩事件不走 `evolution.Hook` 标准接口（因为 `EmitContextCompress` 是通过 `DashboardEmitter` 路由的），而是由 `gateway.NewEvalRunner` 创建的 `CompressionEmitter` 桥接：

```
agent.ContextManager.EmitContextCompress()
    → MultiEmitter [dashboardEmitter, compressionEmitter]
    → compressionEmitter.EmitContextCompress()
    → EvalHook.RecordCompression(sessionID, reason, layers, before, after)
```

### 6.4 数据读取与清理

```go
// 读取（在 evo.WaitPending() 之后调用）
hook.GetReflection(sessionID)    → *evolution.ReflectionEvent
hook.GetEpisode(sessionID)       → *evolution.EpisodeEvent
hook.GetToolExecs(sessionID)     → []evolution.ToolExecEvent
hook.GetCompressions(sessionID)  → []CompressionEvent

// 清理（每次任务结束后调用，防止数据跨任务污染）
hook.ClearSession(sessionID)
```

---

## 7. RunTask 内部执行流程

`CognitiveAgentRunner.RunTask` 的完整执行流程：

```
RunTask(ctx, task)
│
├─ 1. 生成新 SessionID（uuid）
├─ 2. channel.Reset()                          // 清空上次消息
├─ 3. hook.ClearSession(sessionID)             // 清空上次进化事件
│
├─ 4. agent.HandleMessage(ctx, sessionID, task.Goal, channel)
│      └─ CognitiveAgent 5 阶段循环：
│         PERCEIVE → PLAN → ACT → OBSERVE → REFLECT
│         └─ 每次工具调用 → hook.OnToolExecuted
│         └─ REFLECT 完成 → hook.OnReflectionComplete
│         └─ Episode 结束 → hook.OnEpisodeComplete
│         └─ 压缩触发 → compressionEmitter → hook.RecordCompression
│
├─ 5. evo.WaitPending()                        // 确保异步进化事件已处理完
│
├─ 6. 采集 Observation 统计
│      └─ r.lastObservation → AssertionTotal/Passed/PassRate
│
├─ 7. 采集 EvalHook 数据
│      ├─ reflection → ReplanCount, Confidence, LessonsLearned
│      ├─ episode    → (供 ComputeReward 使用)
│      └─ toolExecs  → ToolExecStats（聚合 per-tool 统计）
│
├─ 8. 采集压缩事件
│      └─ hook.GetCompressions → CompressionCount, CompressionEvents
│
├─ 9. ComputeReward(input)                     // 计算 RL 奖励值 → EpisodeReward
│
├─ 10. cogCollector.RecordAssertionRate(rate)  // 更新认知指标（若 collector 存在）
│
└─ 返回 *EvalResult
```

---

## 8. 评测 Gateway 隔离配置

`initEvalGateway`（`cmd/ironclaw/eval.go`）在 live 模式下创建专用 Gateway，确保与生产环境完全隔离：

| 配置项 | 评测值 | 说明 |
|--------|--------|------|
| `agent.mode` | `cognitive` | 强制 5 阶段认知循环 |
| `evolution.enabled` | `true` | 确保进化事件可采集 |
| 内存目录 | 临时目录 | 避免污染生产记忆数据 |
| `SkipPersistedFeatureState` | `true` | 忽略磁盘上的 Feature 状态文件 |
| Dashboard | `false` | 关闭 Web Dashboard（减少干扰） |
| 工具权限 | 宽松 | 工具调用不需要用户审批（由 EvalChannel 处理） |
| `knowledge.enabled` | `true` | 确保知识库检索可测试 |
| `memory.enabled` | `true` | 确保记忆 Fixture 注入有效 |

`gateway.NewEvalRunner` 是 Gateway 层的工厂方法，返回一个完整配置的 `CognitiveAgentRunner`：

```go
func (g *Gateway) NewEvalRunner() (*eval.CognitiveAgentRunner, error) {
    runner := eval.NewCognitiveAgentRunner(g.cognitiveAgent)
    runner.SetCogCollector(g.cogCollector)
    runner.SetMemoryStore(g.memoryStore)
    runner.SetDashboardEmitter(multiEmitter)  // dashboard + compressionEmitter
    return runner, nil
}
```

---

## 9. MemoryAwareRunner — 记忆 Fixture 注入

用于记忆维度（`DimMemory`、`DimMemoryRetention`）的评测任务，需要在任务执行前将特定记忆注入 Agent 的活跃存储中：

```go
type MemoryAwareRunner interface {
    InjectMemory(ctx context.Context, entries ...memory.Entry) error
    CleanupMemory(ctx context.Context, ids ...string) error
}
```

### 9.1 InjectMemory 实现

```go
func (r *CognitiveAgentRunner) InjectMemory(ctx context.Context, entries ...memory.Entry) error {
    for _, e := range entries {
        r.memStore.Save(ctx, e)
    }
    // 强制重建 FTS5 索引，确保 PERCEIVE 阶段能立即搜索到注入的条目
    if rebuilder, ok := r.memStore.(indexRebuilder); ok {
        rebuilder.RebuildIndex(ctx)
    }
    return nil
}
```

**关键设计**：注入后立即调用 `RebuildIndex`，否则 FTS5 全文搜索缓存不更新，PERCEIVE 阶段可能搜不到新注入的记忆。

### 9.2 典型使用模式

```go
// 在 fixtures_memory.go 中的任务定义
TaskCase{
    ID:   "memory-recall-01",
    Goal: "What do you know about the user's preferred coding style?",
    Dimension: DimMemory,
    SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
        mr, ok := runner.(MemoryAwareRunner)
        if !ok { return nil }
        return mr.InjectMemory(ctx, memory.Entry{
            ID:      "style-pref-01",
            Content: "User prefers functional style with no side effects",
            Scope:   memory.ScopeUser,
        })
    },
    CleanupWithRunner: func(ctx context.Context, runner AgentRunner) error {
        mr, ok := runner.(MemoryAwareRunner)
        if !ok { return nil }
        return mr.CleanupMemory(ctx, "style-pref-01")
    },
}
```
