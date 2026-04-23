# 进化系统集成（Evolution Integration）

> 源文件：`internal/eval/eval_hook.go`、`internal/eval/cognitive_runner.go`（`CaptureSnapshot`、`populateFromEvolution`）、`internal/cogmetrics/`、`internal/evolution/`、`internal/gateway/gateway.go`（`NewEvalRunner`）

---

## 目录

1. [模块职责](#1-模块职责)
2. [双向耦合设计](#2-双向耦合设计)
3. [EvalHook — 进化事件采集器](#3-evalhook--进化事件采集器)
4. [compressionAdapter — 压缩事件桥接](#4-compressionadapter--压缩事件桥接)
5. [CaptureSnapshot — 进化状态快照](#5-capturesnapshot--进化状态快照)
6. [populateFromEvolution — 填充任务级进化指标](#6-populatefromevolution--填充任务级进化指标)
7. [ComputeReward — RL 奖励计算](#7-computereward--rl-奖励计算)
8. [cogmetrics.Collector — 认知健康采集器](#8-cogmetricscollector--认知健康采集器)
9. [cogmetrics.HealthReport — 认知健康报告](#9-cogmetricshealthreport--认知健康报告)
10. [NewEvalRunner — Gateway 侧装配](#10-newevalrunner--gateway-侧装配)
11. [进化子系统状态读取路径](#11-进化子系统状态读取路径)
12. [InsightsCycle 触发机制](#12-insightscycle-触发机制)

---

## 1. 模块职责

进化系统集成层负责在评测过程中**采集**进化子系统的实时状态，并**量化**每次评测对进化系统的影响：

- **EvalHook**：实时采集 REFLECT/Episode/Tool/Compression 事件
- **CaptureSnapshot**：量化进化子系统在某时刻的聚合状态
- **cogmetrics.Collector**：独立采集认知健康指标（滚动窗口）
- **ComputeReward**：将任务执行结果转化为 RL 奖励信号

---

## 2. 双向耦合设计

```
┌────────────────────────────────────────────────────────────────┐
│                  评测系统 → 进化系统（驱动）                     │
│                                                                │
│  CognitiveAgent.HandleMessage(goal)                            │
│       ↓ 每次 REFLECT 阶段完成                                   │
│  evolution.Engine.DispatchReflection(ReflectionEvent)          │
│       ↓ 异步 goroutine（10s 超时 + panic 恢复）                  │
│  PreferenceLearner.OnReflectionComplete(event)                 │
│  StrategyOptimizer.OnReflectionComplete(event)                 │
│  EvalHook.OnReflectionComplete(event)  ← 评测专用              │
│  cogmetrics.Collector.OnReflectionComplete(event)              │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│                  进化系统 → 评测系统（被度量）                    │
│                                                                │
│  CaptureSnapshot() 读取：                                       │
│    PreferenceLearner.ListPreferences() → PreferenceCount       │
│    StrategyOptimizer.Version()         → StrategyVersion       │
│    StrategyOptimizer.GetReplanThreshold() → ReplanThreshold    │
│    SkillSynthesizer.DraftCount()       → SkillDraftCount       │
│    TrajectoryRecorder.Count()          → TrajectoryCount       │
│    ReadTrajectories() → RLStats (AvgReward/SuccessRate/...)    │
└────────────────────────────────────────────────────────────────┘
```

---

## 3. EvalHook — 进化事件采集器

`EvalHook` 实现 `evolution.Hook` 接口，专为评测设计，以 **per-session** 方式缓冲事件：

### 3.1 接口实现

```go
type EvalHook struct {
    mu           sync.Mutex
    reflections  map[string]*evolution.ReflectionEvent   // sessionID → 反思
    episodes     map[string]*evolution.EpisodeEvent       // sessionID → Episode
    toolExecs    map[string][]evolution.ToolExecEvent     // sessionID → 工具执行列表
    compressions map[string][]CompressionEvent            // sessionID → 压缩事件列表
}
```

### 3.2 与生产侧 Hook 的区别

| 特性 | 生产侧 Hook（PreferenceLearner 等） | EvalHook |
|------|----------------------------------|----------|
| 状态持久化 | 写入文件/DB | 仅内存（per-session Map） |
| 数据范围 | 全局累积 | per-session 隔离 |
| 生命周期 | 永久 | 每任务结束后 `ClearSession` |
| 副作用 | 修改偏好/策略 | 只读采集 |

### 3.3 ReflectionEvent 关键字段

```go
type ReflectionEvent struct {
    SessionID      string
    Goal           string
    Complexity     string    // "simple" | "moderate" | "complex"
    Succeeded      bool
    Confidence     float64
    LessonsLearned []string
    ToolsUsed      []string
    ReplanCount    int
    UserFeedback   float64
    FinalAnswer    string
    Timestamp      time.Time
}
```

### 3.4 EpisodeEvent 关键字段

```go
type EpisodeEvent struct {
    SessionID   string
    Reward      float64    // ComputeReward 的结果（由进化引擎计算）
    Progress    float64    // AssertionPassRate
    ReplanCount int
    ToolsUsed   []string
    Actions     []string   // Agent 的行动序列摘要
}
```

---

## 4. compressionAdapter — 压缩事件桥接

上下文压缩事件不走 `evolution.Hook` 标准路径（压缩触发在 agent 内部，事件通过 `DashboardEmitter` 接口发出），需要通过专用适配器桥接：

```
agent.ContextManager.EmitContextCompress(sessionID, reason, layers, before, after)
    │
    ▼
MultiEmitter（gateway.NewEvalRunner 创建）
    ├─ DashboardEmitter（若 dashboard 开启）
    └─ compressionAdapter（评测专用）
            │
            ▼
        EvalHook.RecordCompression(sessionID, reason, layers, before, after)
```

`compressionAdapter` 实现完整的 `agent.DashboardEmitter` 接口，但除 `EmitContextCompress` 外所有方法都是 no-op：

```go
type compressionAdapter struct {
    hook *EvalHook
}

func (a *compressionAdapter) EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64) {
    a.hook.RecordCompression(sessionID, reason, layersRun, beforePct, afterPct)
}
// 其余 10+ 个方法均为空实现
```

**设计意图**：`EvalHook` 专注于 `evolution.Hook` 职责；压缩事件通过适配器注入，避免 EvalHook 承担过多职责，同时满足完整接口约束。

---

## 5. CaptureSnapshot — 进化状态快照

```go
func (r *CognitiveAgentRunner) CaptureSnapshot() *EvolutionSnapshot
```

`CaptureSnapshot` 从进化引擎读取所有子系统的**当前聚合状态**，生成可序列化的 `EvolutionSnapshot`。

### 5.1 数据来源映射

| EvolutionSnapshot 字段 | 数据来源 |
|----------------------|----------|
| `PreferenceCount` | `PreferenceLearner.ListPreferences()` 的长度 |
| `StrategyVersion` | `StrategyOptimizer.Version()` |
| `SkillDraftCount` | `SkillSynthesizer.DraftCount()` 或草稿目录文件数 |
| `TrajectoryCount` | `TrajectoryRecorder.Count()` 或轨迹目录文件数 |
| `ReplanThreshold` | `StrategyOptimizer.GetReplanThreshold()` |
| `ReplanThresholdPrev` | `StrategyOptimizer.PreviousThreshold()` |
| `ReplanThresholdReason` | `StrategyOptimizer.LastChangeReason()` |
| `ToolPriorities` | `StrategyOptimizer.GetToolPriorities()` |
| `RLEpisodeCount` | `ReadTrajectories() + ConvertTrajectories()` 的 EpisodeCount |
| `RLAvgReward` | 近期轨迹奖励均值 |
| `RLSuccessRate` | 近期轨迹成功率 |
| `RLAvgProgress` | 近期轨迹进度均值 |
| `PreferenceHigh/Med/LowConfCount` | 遍历 ListPreferences() 按 Confidence 分桶 |
| `PreferenceAvgConfidence` | 所有偏好条目的 Confidence 均值 |
| `PreferenceToolCount` | Category = "tool_preference" 的条目数 |
| `PreferenceComplexityCount` | Category = "complexity_handling" 的条目数 |
| `RouterDecisions` | 由 RunSuite 调用 `aggregateRouterDecisions()` 填充（EvoAfter 专有） |

### 5.2 调用时机

```
RunSuite / RunSuiteWithOptions
    ├─ CaptureSnapshot()  → suite.EvoBefore  (任务循环开始前)
    │
    ├─ [任务循环...]
    │
    └─ CaptureSnapshot()  → suite.EvoAfter   (任务循环结束后)
         + aggregateRouterDecisions()         (仅 EvoAfter)
```

---

## 6. populateFromEvolution — 填充任务级进化指标

```go
func (r *CognitiveAgentRunner) populateFromEvolution(result *EvalResult, sessionID string)
```

每个任务执行后，从 `EvalHook` 读取该 session 的进化事件，填充 `EvalResult`：

```go
// 从 ReflectionEvent 读取
reflection := r.hook.GetReflection(sessionID)
result.ReplanCount = reflection.ReplanCount
result.Confidence  = reflection.Confidence
result.Success     = reflection.Succeeded

// 从 ToolExecEvent 读取（聚合 per-tool 统计）
toolExecs := r.hook.GetToolExecs(sessionID)
result.ToolExecStats = aggregateToolExecs(toolExecs)

// 从 CompressionEvent 读取
compressions := r.hook.GetCompressions(sessionID)
result.CompressionCount  = len(compressions)
result.CompressionEvents = compressions

// 计算 RL 奖励
result.EpisodeReward = evolution.ComputeReward(RewardInput{
    Succeeded:    reflection.Succeeded,
    Progress:     result.AssertionPassRate,
    DurationMs:   result.Duration.Milliseconds(),
    ReplanCount:  reflection.ReplanCount,
    UserFeedback: task.UserFeedback,
})
```

---

## 7. ComputeReward — RL 奖励计算

```go
func ComputeReward(input RewardInput) float64
```

### 7.1 RewardInput

```go
type RewardInput struct {
    Succeeded    bool
    Progress     float64   // AssertionPassRate（0.0-1.0）
    DurationMs   int64     // 执行耗时
    ReplanCount  int       // 重规划次数
    UserFeedback float64   // -1.0 ～ +1.0
}
```

### 7.2 奖励计算公式（概述）

```
base_reward = Succeeded ? 1.0 : 0.0
progress_bonus = Progress * 0.3
duration_penalty = f(DurationMs)   // 超时惩罚
replan_penalty = ReplanCount * 0.05  // 每次重规划 -0.05
user_feedback_bonus = UserFeedback * 0.2  // 用户反馈调节

reward = base_reward + progress_bonus - duration_penalty - replan_penalty + user_feedback_bonus
reward = clamp(reward, 0.0, 1.0)
```

**当 `task.UserFeedback != 0` 时**，评测 Runner 会用含用户反馈的 `RewardInput` 重新计算 `EpisodeReward`，覆盖进化引擎的默认计算结果：

```go
if task.UserFeedback != 0 {
    result.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
        Succeeded:    result.Success,
        Progress:     result.AssertionPassRate,
        DurationMs:   result.Duration.Milliseconds(),
        ReplanCount:  result.ReplanCount,
        UserFeedback: task.UserFeedback,
    })
}
```

---

## 8. cogmetrics.Collector — 认知健康采集器

`cogmetrics.Collector` 也实现了 `evolution.Hook` 接口，在进化引擎中与 `EvalHook` 并行注册。它维护**滚动窗口**（默认 100 条记录）的滚动平均值：

### 8.1 采集的指标

| 指标 | 滚动窗口含义 | 事件来源 |
|------|------------|----------|
| `assertionPassRate` | Assertion 通过率均值 | `RecordAssertionRate()`（eval Runner 手动调用） |
| `replanRate` | 有重规划任务的比例 | `OnEpisodeComplete` |
| `replanSuccess` | 重规划后的成功率 | `OnEpisodeComplete`（ReplanCount > 0） |
| `noReplanSuccess` | 无重规划时的成功率 | `OnEpisodeComplete`（ReplanCount == 0） |
| `avgConfidence` | 平均置信度 | `OnReflectionComplete` |
| `toolReliability` | per-tool 成功率 | `OnToolExecuted` |
| `complexitySuccess` | 按复杂度分类的成功率 | `OnReflectionComplete` |

### 8.2 RollingAvg 实现

```go
// O(1) 环形缓冲，固定窗口大小
type RollingAvg struct {
    buf   []float64
    pos   int
    count int
    size  int
}
```

不使用 `append`，直接覆盖最旧的数据点，恒定内存占用。

---

## 9. cogmetrics.HealthReport — 认知健康报告

`Collector.Snapshot()` 返回 `HealthReport`：

```go
type HealthReport struct {
    AssertionPassRate float64             `json:"assertion_pass_rate"`
    ReplanRate        float64             `json:"replan_rate"`
    ReplanSuccess     float64             `json:"replan_success_rate"`
    NoReplanSuccess   float64             `json:"no_replan_success_rate"`
    AvgConfidence     float64             `json:"avg_confidence"`
    ToolReliability   map[string]float64  `json:"tool_reliability"`
    ComplexitySuccess map[string]float64  `json:"complexity_success"`
    TotalEpisodes     int64               `json:"total_episodes"`
    TotalReflections  int64               `json:"total_reflections"`
    StrategyVersion   int                 `json:"strategy_version"`
    GeneratedAt       time.Time           `json:"generated_at"`
    UptimeDuration    time.Duration       `json:"uptime_duration_ms"`
}
```

**`SuiteResult.CogHealth`** 在套件执行结束后由 `CaptureCogHealth()` 填充（仅当 evolution 开启时）。

### 9.1 诊断用途

| 字段 | 健康指标解读 |
|------|------------|
| `AssertionPassRate` > 0.8 | Agent 自验证能力良好 |
| `ReplanRate` < 0.3 | 规划质量较高，很少需要重规划 |
| `ReplanSuccess` > `NoReplanSuccess` | 重规划策略有效（重规划后更容易成功） |
| `AvgConfidence` > 0.7 | Agent 置信度估计准确 |
| `ToolReliability[tool]` > 0.9 | 该工具可靠性高 |

---

## 10. NewEvalRunner — Gateway 侧装配

`gateway.NewEvalRunner()` 是创建完整配置 `CognitiveAgentRunner` 的入口：

```go
func (g *Gateway) NewEvalRunner() (*eval.CognitiveAgentRunner, error) {
    // 1. 创建 Runner（同时注册 EvalHook 到进化引擎）
    runner := eval.NewCognitiveAgentRunner(g.cognitiveAgent)

    // 2. 挂载 cogmetrics 采集器（用于 CaptureCogHealth）
    runner.SetCogCollector(g.cogCollector)

    // 3. 挂载记忆存储（用于 InjectMemory）
    runner.SetMemoryStore(g.memoryStore)

    // 4. 构建多路复用 DashboardEmitter
    // compressionEmitter 仅负责将压缩事件路由到 EvalHook
    compressionEmitter := runner.CompressionEmitter()
    multiEmitter := agent.NewMultiEmitter(g.dashboardEmitter, compressionEmitter)
    runner.SetDashboardEmitter(multiEmitter)

    // 5. 记录当前 Feature 状态（写入 SuiteResult.FeatureState）
    runner.SetFeatureState(g.features.RuntimeOverrides())

    return runner, nil
}
```

---

## 11. 进化子系统状态读取路径

以下表格梳理了 `CaptureSnapshot` 调用的完整调用链：

```
CaptureSnapshot()
│
├─ evo := r.agent.EvolutionEngine()
│
├─ pref := evo.PreferenceLearner()
│   └─ pref.ListPreferences()       → []Preference (count, quality)
│
├─ opt := evo.StrategyOptimizer()
│   ├─ opt.Version()                → StrategyVersion
│   ├─ opt.GetReplanThreshold()     → ReplanThreshold
│   ├─ opt.PreviousThreshold()      → ReplanThresholdPrev
│   ├─ opt.LastChangeReason()       → ReplanThresholdReason
│   └─ opt.GetToolPriorities()      → ToolPriorities map
│
├─ synth := evo.SkillSynthesizer()
│   └─ synth.DraftCount()           → SkillDraftCount
│
├─ traj := evo.TrajectoryRecorder()
│   ├─ traj.Count()                 → TrajectoryCount
│   └─ ReadTrajectories(dir, 7days)
│       → ConvertTrajectories()     → RLStats (4 fields)
│
├─ router := evo.Router()
│   └─ (RouterDecisions 由 aggregateRouterDecisions 填充，非此处)
│
└─ 偏好质量分布计算
    └─ 遍历 ListPreferences()，按 confidence 分 3 桶
```

---

## 12. InsightsCycle 触发机制

纵向评测（`eval longitudinal --force-insights`）在轮次间强制触发洞见生成：

```go
func (r *CognitiveAgentRunner) RunInsightsCycle() (ran bool, reason string) {
    evo := r.agent.EvolutionEngine()
    if evo == nil {
        return false, "no evolution engine"
    }
    // 绕过 6 小时定时器，立即执行：
    // 1. ReadTrajectories(过去 7 天)
    // 2. InsightGenerator.GenerateInsights(trajectories)
    // 3. StrategyOptimizer.ApplyInsights(insights)
    // 4. PreferenceLearner.ApplyInsights(insights)
    return evo.RunInsightsCycle(context.Background())
}
```

**evo.WaitPending()** 在每个任务的 `RunTask` 结束时调用，确保所有异步进化事件处理完成再读取状态：

```go
// RunTask 末尾
if evo := r.agent.EvolutionEngine(); evo != nil {
    evo.WaitPending()  // 等待 DispatchReflection / DispatchEpisode 的 goroutine 完成
}
// 此后 EvalHook.GetReflection() 才能读到完整数据
```
