# 评测框架自进化质量深度指标

**日期**: 2026-04-21
**范围**: 自进化评测覆盖度全面提升 — 策略参数捕获 + RL 统计 + 偏好质量评测 + 用户反馈注入 + Insights 纵向闭环 + Model Router 评测
**前置**: [EVAL_SELF_EVOLUTION_METRICS.md](EVAL_SELF_EVOLUTION_METRICS.md)（原有评测框架自进化指标基础设施）

---

## 概述

[EVAL_SELF_EVOLUTION_METRICS.md](EVAL_SELF_EVOLUTION_METRICS.md) 建立了评测框架与进化引擎的基础观测链路（ToolExecStats、EpisodeReward、CogMetrics、记忆联通）。但经过分析，评测系统对自进化的覆盖仍停留在"观测层"（发生了什么），而非"质量层"（进化是否有效）。具体缺口如下：

| 自进化方向 | 原有状态 | 缺口 |
|---|---|---|
| **RL 奖励** | 标量奖励已捕获 | 无 RL 策略质量聚合（轨迹层面的 avgReward、successRate） |
| **策略优化器** | 仅版本号计数器 | 策略参数值（ReplanThreshold、ToolPriorities）不可见 |
| **偏好学习器** | 仅条目总数 | 无置信度分布、无影响力验证、无负反馈路径测试 |
| **Insights 循环** | 未评测 | `RunInsightsCycle()` 从未被 eval 触发，无纵向学习效果验证 |
| **Model Router** | 完全黑盒 | 路由决策不可见，无法判断是否路由到了正确模型 |
| **用户反馈** | eval 中永远为 0 | 无法测试 agent 对正/负反馈的响应路径 |

本次改动通过 4 个并行开发分支分别补齐这些缺口，并在合并到 main 后增加了一处集成修复（`IterationPoint` 纵向时序数据断链）。

---

## 方向一：策略参数捕获 + RL 轨迹统计

**分支**: `eval/strategy-rl-metrics`  
**修改文件**: `internal/eval/harness.go`、`internal/eval/compare.go`、`internal/eval/cognitive_runner.go`、`internal/eval/cognitive_runner_test.go`

### 问题

原有 `EvolutionSnapshot.StrategyVersion` 只是一个整数版本计数器，无法告诉我们策略参数实际变化了什么。`StrategyOptimizer` 内部维护着 `ReplanThreshold`（可调节的重规划阈值）和 `ToolPriorities`（每工具优先级），这些参数才是进化的核心产物，却从未出现在评测报告中。

同样，`rl_bridge.go` 将轨迹转换为 `RLExperience` 向量并输入 RL 训练，但评测只捕获了 `EpisodeReward` 标量，没有轨迹层面的聚合统计（平均奖励、成功率、进展度），无法判断 RL 策略是否在演化。

### 新增字段

**`EvolutionSnapshot` 新增策略参数**（`internal/eval/harness.go`）：

```go
// Strategy parameter values captured at snapshot time.
ReplanThreshold       float64            `json:"replan_threshold,omitempty"`
ReplanThresholdPrev   float64            `json:"replan_threshold_prev,omitempty"`
ReplanThresholdReason string             `json:"replan_threshold_reason,omitempty"`
ToolPriorities        map[string]float64 `json:"tool_priorities,omitempty"`
```

**`EvolutionSnapshot` 新增 RL 轨迹统计**：

```go
// RLStats captures aggregate RL experience statistics from recent trajectories.
RLEpisodeCount int     `json:"rl_episode_count,omitempty"`
RLAvgReward    float64 `json:"rl_avg_reward,omitempty"`
RLSuccessRate  float64 `json:"rl_success_rate,omitempty"`
RLAvgProgress  float64 `json:"rl_avg_progress,omitempty"`
```

### 数据来源

`CaptureSnapshot()` 中扩展策略读取路径：

```go
if so := evo.StrategyOptimizerHook(); so != nil {
    strategy := so.GetStrategy()
    snap.StrategyVersion = strategy.Version
    snap.ReplanThreshold = strategy.ReplanThreshold.Value
    snap.ReplanThresholdPrev = strategy.ReplanThreshold.Previous
    snap.ReplanThresholdReason = strategy.ReplanThreshold.Reason
    if len(strategy.ToolPriorities) > 0 {
        tp := make(map[string]float64, len(strategy.ToolPriorities))
        for tool, param := range strategy.ToolPriorities {
            tp[tool] = param.Value
        }
        snap.ToolPriorities = tp
    }
}
```

新增 `computeRLStats()` helper，读取近 7 天轨迹（扩展自原来的 24h）并计算聚合统计：

```go
func computeRLStats(snap *EvolutionSnapshot, records []evolution.TrajectoryRecord) *EvolutionSnapshot {
    exps := evolution.ConvertTrajectories(records)
    // 计算 RLEpisodeCount、RLAvgReward、RLSuccessRate（Progress >= 1.0 的比例）、RLAvgProgress
}
```

### 对比报告增强

`EvoSnapshotDiff` 新增 4 个 delta 字段（`internal/eval/compare.go`）：

```go
ReplanThresholdDelta float64            `json:"replan_threshold_delta,omitempty"`
ToolPriorityDeltas   map[string]float64 `json:"tool_priority_deltas,omitempty"`
RLAvgRewardDelta     float64            `json:"rl_avg_reward_delta,omitempty"`
RLSuccessRateDelta   float64            `json:"rl_success_rate_delta,omitempty"`
```

`ToolPriorityDeltas` 只计算两次快照中都存在的工具的差值，避免因工具出现/消失产生误报。`FormatMarkdown()` 新增对应渲染行，并在有工具优先级变化时额外输出一张 Tool Priority Deltas 子表。

---

## 方向二：偏好质量评测 + 用户反馈注入

**分支**: `eval/preference-feedback`  
**修改/新增文件**: `internal/eval/harness.go`、`internal/eval/cognitive_runner.go`、`internal/evolution/preference.go`、`internal/eval/fixtures_preference.go`、`internal/eval/fixtures_preference_test.go`

### 问题

`PreferenceLearner` 学习三类偏好信号（`tool_preference`、`complexity_handling`、`replan_tendency`），但评测只捕获了总条目数 `PreferenceCount`，完全看不到：
- 偏好置信度分布（高/中/低置信分别有多少，平均置信度是多少）
- 各类别条目数（工具偏好学了多少工具，复杂度偏好覆盖了哪些级别）
- 用户反馈对奖励计算的影响（eval 中 `UserFeedback` 永远为 0）

### 新增 `UserFeedback` 到 `TaskCase` 和 `EvalResult`

**`TaskCase` 新增**（`internal/eval/harness.go`）：

```go
// UserFeedback simulates user rating for this task during eval.
// Range: -1.0 (negative) to 1.0 (positive). 0 means no feedback.
UserFeedback float64 `json:"user_feedback,omitempty" yaml:"user_feedback,omitempty"`
```

**`EvalResult` 新增**：

```go
UserFeedback float64 `json:"user_feedback,omitempty"`
```

**`RunTask()` 中的注入逻辑**（`internal/eval/cognitive_runner.go`）：

```go
// Override episode reward to include simulated user feedback when set.
if task.UserFeedback != 0 {
    result.UserFeedback = task.UserFeedback
    result.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
        Succeeded:    result.Success,
        Progress:     result.AssertionPassRate,
        DurationMs:   result.Duration.Milliseconds(),
        ReplanCount:  result.ReplanCount,
        UserFeedback: task.UserFeedback,
    })
}
```

> **设计选择**：不通过 evolution 引擎二次 dispatch `ReflectionEvent`（会引入副作用），而是在 `populateFromEvolution` 之后直接用反馈重算 `EpisodeReward`。这保持了 eval 的幂等性，同时让 reward 完整反映用户反馈。

### `EvolutionSnapshot` 新增偏好质量分布

```go
// PreferenceQuality captures the distribution of learned preferences.
PreferenceHighConfCount   int     `json:"pref_high_conf_count,omitempty"`   // confidence >= 0.8
PreferenceMedConfCount    int     `json:"pref_med_conf_count,omitempty"`    // 0.4 <= confidence < 0.8
PreferenceLowConfCount    int     `json:"pref_low_conf_count,omitempty"`    // confidence < 0.4
PreferenceAvgConfidence   float64 `json:"pref_avg_confidence,omitempty"`
PreferenceToolCount       int     `json:"pref_tool_count,omitempty"`
PreferenceComplexityCount int     `json:"pref_complexity_count,omitempty"`
```

由 `populatePreferenceQuality()` helper 填充，聚合 `tool_preference`、`complexity_handling`、`replan_tendency` 三类的所有条目：

```go
func populatePreferenceQuality(snap *EvolutionSnapshot, pl *evolution.PreferenceLearner) {
    toolEntries := pl.ListByCategory("tool_preference")
    complexityEntries := pl.ListByCategory("complexity_handling")
    // ... 计算 high/med/low confidence 分桶 + avgConfidence
}
```

### `PreferenceLearner.ListByCategory()` 新增公共方法

原有 `GetPreferences(category)` 会过滤掉低于 `MinConfidence` 的条目，不适合用于质量分析（需要看全分布）。新增 `ListByCategory(category string) []PreferenceEntry` 绕过置信度过滤，返回该类别下的所有条目（按置信度降序排列）。

### `fixtures_preference.go` 偏好评测任务集

```go
func PreferenceTasks() []TaskCase {
    return []TaskCase{
        {
            ID: "pref-tool-bash-preference", Complexity: "simple",
            UserFeedback: 0.8,  // 正反馈，强化 bash 工具偏好
        },
        {
            ID: "pref-complexity-simple", Complexity: "simple",
            UserFeedback: 1.0,  // 最大正反馈，强化 simple 复杂度处理偏好
        },
        {
            ID: "pref-negative-feedback", Complexity: "simple",
            UserFeedback: -0.5, // 负反馈，验证负信号不强化偏好
        },
    }
}
```

---

## 方向三：Insights 纵向闭环评测

**分支**: `eval/longitudinal-insights`  
**新增文件**: `internal/eval/longitudinal_runner.go`、`internal/eval/longitudinal_runner_test.go`  
**修改文件**: `internal/eval/cognitive_runner.go`

### 问题

进化引擎的 `RunInsightsCycle()` 是一个关键学习路径：读取近 7 天轨迹 → 生成洞察报告 → 调用 `StrategyOptimizer.ApplyInsights()` 和 `PreferenceLearner.ApplyInsights()`。代码注释明确说明"safe to call from external code (e.g. eval longitudinal)"，但评测框架从未调用过这个方法，导致无法验证：

- Insights 循环是否真的改善了后续任务表现
- 策略/偏好调整的方向是否正确
- 需要多少轨迹数据才能触发有意义的学习

### `InsightsTrigger` 接口

```go
// InsightsTrigger is implemented by runners that can trigger the evolution
// engine's insights cycle directly (without waiting for the 6-hour timer).
type InsightsTrigger interface {
    RunInsightsCycle() (ran bool, reason string)
    TrajectoryCount() int
}
```

> **设计选择**：`InsightsTrigger` 作为独立的可选接口，而非嵌入 `AgentRunner`。这样 `DryRunner`、mock runner 等不支持进化的 runner 可以正常参与纵向评测（Insights 触发会被跳过并附带说明原因），不需要实现任何新方法。

`CognitiveAgentRunner` 实现了该接口：

```go
func (r *CognitiveAgentRunner) RunInsightsCycle() (ran bool, reason string) {
    evo := r.agent.EvolutionEngine()
    if evo == nil { return false, "evolution engine not configured" }
    if !evo.IsEnabled() { return false, "evolution engine disabled" }
    evo.RunInsightsCycle()
    return true, "insights cycle completed"
}

func (r *CognitiveAgentRunner) TrajectoryCount() int {
    // 读取 evo.TrajectoryDir() 下近 7 天的轨迹文件计数
}
```

### `RunLongitudinal()` 多轮评测循环

```go
func RunLongitudinal(ctx context.Context, runner AgentRunner, tasks []TaskCase,
    cfg LongitudinalConfig) (*LongitudinalResult, error)
```

执行流程：

```
for round in [0, Rounds):
    suite = RunSuite(tasks)
    if prevSuite != nil:
        round.Comparison = Compare(prevSuite, suite)
    
    if round < Rounds-1:  // 最后一轮后不触发（没有后续轮次）
        if runner implements InsightsTrigger:
            if TrajectoryCount() >= MinTrajectories:
                RunInsightsCycle()  // 触发学习
            else:
                记录跳过原因
    
    if OutputDir != "":
        suite.SaveJSON(round_N.json)
    
    prevSuite = suite
```

关键配置：

```go
type LongitudinalConfig struct {
    Rounds          int    // 轮次数，默认 2
    MinTrajectories int    // 触发 Insights 的最低轨迹数，默认 5
    OutputDir       string // 每轮 JSON 输出目录（可选）
}
```

### `LongitudinalResult` 结构

```go
type LongitudinalResult struct {
    Config      LongitudinalConfig
    Rounds      []LongitudinalRound
    StartedAt   time.Time
    CompletedAt time.Time
}

type LongitudinalRound struct {
    RoundNumber    int               // 0-indexed
    Suite          *SuiteResult      // 本轮完整评测结果
    InsightsRan    bool              // Insights 是否实际触发
    InsightsReason string            // 跳过原因（轨迹不足 / runner 不支持等）
    Comparison     *ComparisonReport // 与上轮对比（Round 0 为 nil）
    Duration       time.Duration
}
```

`LongitudinalResult.FormatMarkdown()` 输出每轮的成功率、断言通过率、Insights 状态和 vs 上轮的 delta。

---

## 方向四：Model Router 路由评测 + Skill 合成器量化

**分支**: `eval/router-skill-metrics`  
**新增文件**: `internal/eval/fixtures_evolution.go`、`internal/eval/fixtures_evolution_test.go`  
**修改文件**: `internal/eval/harness.go`、`internal/eval/cognitive_runner.go`、`internal/eval/compare.go`

### 问题

`ModelRouter` 根据任务复杂度将请求路由到不同模型（如 simple→haiku、complex→sonnet），是影响任务质量和成本的重要机制。但评测结果中从未记录任何路由信息，无法回答：
- 复杂任务是否真的被路由到了能力更强的模型？
- 路由策略调整是否改变了任务质量？
- 不同复杂度任务的模型分布是否符合预期？

`SkillSynthesizer` 只有 `DraftCount()` 一个公开指标，经代码分析（`internal/evolution/synthesizer.go`），其内部 `generated map[string]bool` 仅存储去重标识，不暴露单个 draft 的质量分数，因此本次暂不扩展 Skill 质量评测，仅在 snapshot 中保留现有的 `SkillDraftCount`。

### `EvalResult.RoutedModel` — 每任务路由捕获

```go
// RoutedModel is the model name selected by the Model Router for this task's
// complexity. Empty when routing is disabled or the complexity is unrecognized.
RoutedModel string `json:"routed_model,omitempty"`
```

在 `RunTask()` 中，结果对象建立后立即捕获路由决策：

```go
if evo := r.agent.EvolutionEngine(); evo != nil {
    if rr := evo.Router().SelectModel(task.Complexity); rr.Routed {
        result.RoutedModel = rr.Model
    }
}
```

`SelectModel()` 返回 `RouteResult{Model, MaxTokens, Routed bool}`；当路由未启用时 `Routed == false`，字段保持空字符串，不产生虚假数据。

### Suite 级路由分布聚合

**`EvolutionSnapshot.RouterDecisions`** — 套件级路由分布快照：

```go
RouterDecisions map[string]int `json:"router_decisions,omitempty"` // model → 任务数
```

**`SuiteSummary.RouterDecisions`** — 汇总报告中的路由分布：

```go
RouterDecisions map[string]int `json:"router_decisions,omitempty"`
```

两者均由 `aggregateRouterDecisions()` 从 `[]EvalResult` 聚合，在 `RunSuite()`、`RunSuiteWithOptions()` 和 `Summary()` 中自动调用：

```go
func aggregateRouterDecisions(results []EvalResult) map[string]int {
    decisions := make(map[string]int)
    for _, r := range results {
        if r.RoutedModel != "" {
            decisions[r.RoutedModel]++
        }
    }
    return decisions
}
```

### 对比报告新增路由变化

`EvoSnapshotDiff.RouterModelChanges` 记录两次 suite 之间各模型路由数量的 delta：

```go
RouterModelChanges map[string]int `json:"router_model_changes,omitempty"`
```

只记录有变化的模型，零 delta 的模型被省略。`FormatMarkdown()` 在 Evolution Snapshot Delta 区域追加 Router Model Changes 子表。

### `fixtures_evolution.go` 进化系统专项任务集

覆盖三个复杂度级别，用于验证路由决策和触发 SkillSynthesizer：

```go
func EvolutionTasks() []TaskCase {
    return []TaskCase{
        {ID: "evo-simple-routing",   Complexity: "simple",   ...}, // 验证 simple 路由
        {ID: "evo-moderate-routing", Complexity: "moderate", ...}, // 验证 moderate 路由
        {ID: "evo-complex-routing",  Complexity: "complex",  ...}, // 验证 complex 路由
        {ID: "evo-skill-synthesis-trigger", Complexity: "moderate",
            SuccessFunc: func(r *EvalResult) bool {
                return r.AgentOutput != "" && len(r.AgentOutput) > 50
            }},
    }
}
```

---

## 集成修复：IterationPoint 纵向时序断链

在合并 4 个分支后，发现 `IterationPoint`（纵向时序报告的基本单元）只有原有的 4 个字段，4 个分支新增的所有指标都无法进入纵向时序。

**修复**（`internal/eval/harness.go`，`cmd/ironclaw/eval.go`）：

`IterationPoint` 新增 6 个字段：

```go
type IterationPoint struct {
    // 原有字段
    Iteration       int
    RunID           string
    Timestamp       time.Time
    Summary         SuiteSummary
    StrategyVersion int
    PreferenceCount int
    SkillDraftCount int
    TrajectoryCount int

    // 新增：扩展进化指标
    ReplanThreshold         float64        `json:"replan_threshold,omitempty"`
    RLAvgReward             float64        `json:"rl_avg_reward,omitempty"`
    RLSuccessRate           float64        `json:"rl_success_rate,omitempty"`
    PreferenceAvgConfidence float64        `json:"pref_avg_confidence,omitempty"`
    PreferenceHighConfCount int            `json:"pref_high_conf_count,omitempty"`
    RouterDecisions         map[string]int `json:"router_decisions,omitempty"`
}
```

CLI `longitudinal` 命令的 snapshot 捕获块同步更新，填充这 6 个新字段。

---

## 完整数据流

```
─── 每任务执行（RunTask）───────────────────────────────────────────────
  task.Complexity
    └─ evo.Router().SelectModel()        → EvalResult.RoutedModel

  task.UserFeedback != 0
    └─ evolution.ComputeReward(feedback) → EvalResult.EpisodeReward（覆盖）
                                         → EvalResult.UserFeedback

─── 套件结束（RunSuite / RunSuiteWithOptions）──────────────────────────
  sc.CaptureSnapshot()
    ├─ PreferenceLearnerHook()
    │    ├─ pl.EntryCount()               → snap.PreferenceCount
    │    └─ populatePreferenceQuality()   → snap.Pref{High/Med/Low}ConfCount
    │                                     → snap.PreferenceAvgConfidence
    │                                     → snap.Preference{Tool/Complexity}Count
    ├─ StrategyOptimizerHook()
    │    └─ so.GetStrategy()              → snap.StrategyVersion
    │                                     → snap.ReplanThreshold{,Prev,Reason}
    │                                     → snap.ToolPriorities
    ├─ SkillSynthesizerHook()
    │    └─ ss.DraftCount()               → snap.SkillDraftCount
    └─ TrajectoryRecorderHook()
         └─ ReadTrajectories(-7d)         → snap.TrajectoryCount
              └─ ConvertTrajectories()    → snap.RLEpisode{Count,AvgReward,
                                                SuccessRate,AvgProgress}

  aggregateRouterDecisions(results)       → snap.RouterDecisions
                                          → summary.RouterDecisions

─── 对比（Compare）────────────────────────────────────────────────────
  before.EvoAfter vs after.EvoAfter
    ├─ ReplanThresholdDelta
    ├─ ToolPriorityDeltas（仅共有工具）
    ├─ RLAvgRewardDelta / RLSuccessRateDelta
    └─ RouterModelChanges（仅有变化的模型）

─── 纵向时序（IterationPoint）─────────────────────────────────────────
  snapshot → point.ReplanThreshold
           → point.RLAvgReward / RLSuccessRate
           → point.PreferenceAvgConfidence / PreferenceHighConfCount
           → point.RouterDecisions

─── 纵向闭环（RunLongitudinal / longitudinal CLI）──────────────────────
  Round N:  RunSuite → SuiteResult
  Between:  if runner implements InsightsTrigger
               && TrajectoryCount() >= MinTrajectories
               → RunInsightsCycle()   ← 触发策略/偏好学习
  Round N+1: RunSuite → Compare(Round N, Round N+1)
```

---

## 涉及文件汇总

| 文件 | 变更类型 | 主要内容 |
|------|---------|---------|
| `internal/eval/harness.go` | 修改 | `EvalResult` 新增 `RoutedModel`、`UserFeedback`；`EvolutionSnapshot` 新增 12 个字段（策略参数、RL 统计、偏好质量、路由分布）；`SuiteSummary` 新增 `RouterDecisions`；`IterationPoint` 新增 6 个字段；新增 `aggregateRouterDecisions()` |
| `internal/eval/compare.go` | 修改 | `EvoSnapshotDiff` 新增 4 个 delta 字段（策略、RL、路由）；`Compare()` 和 `FormatMarkdown()` 同步更新 |
| `internal/eval/cognitive_runner.go` | 修改 | `RunTask()` 新增路由捕获 + 用户反馈覆盖；`CaptureSnapshot()` 新增策略参数 + RL 统计 + 偏好质量填充；新增 `computeRLStats()`、`populatePreferenceQuality()`、`RunInsightsCycle()`、`TrajectoryCount()` |
| `internal/eval/longitudinal_runner.go` | **新增** | `InsightsTrigger` 接口、`LongitudinalConfig/Result/Round` 类型、`RunLongitudinal()` 函数、`LongitudinalResult.FormatMarkdown()` |
| `internal/eval/longitudinal_runner_test.go` | **新增** | 5 个单元测试覆盖正常触发、轨迹不足跳过、runner 不支持、默认配置、markdown 输出 |
| `internal/eval/fixtures_preference.go` | **新增** | `PreferenceTasks()` — 3 个偏好评测任务（正/负反馈路径覆盖） |
| `internal/eval/fixtures_preference_test.go` | **新增** | 4 个测试：UserFeedback 正/负影响奖励验证、偏好质量分桶验证、fixture 结构验证 |
| `internal/eval/fixtures_evolution.go` | **新增** | `EvolutionTasks()` — 4 个进化系统专项任务（3 复杂度路由覆盖 + Skill 触发）|
| `internal/eval/fixtures_evolution_test.go` | **新增** | 8 个测试：复杂度覆盖、路由字段、聚合逻辑、diff 计算等 |
| `internal/eval/cognitive_runner_test.go` | 修改 | 新增 5 个测试：策略参数提取、RL 统计聚合、空轨迹处理、diff 计算、markdown 渲染 |
| `internal/evolution/preference.go` | 修改 | 新增 `ListByCategory(category string) []PreferenceEntry`（绕过 MinConfidence 过滤）|
| `cmd/ironclaw/eval.go` | 修改 | `longitudinal` 命令 snapshot 捕获块补充 6 个新字段填充 |

---

## 使用示例

### 查看策略参数和 RL 统计

```bash
ironclaw eval run --suite full --live -o eval_output/results.json

cat eval_output/results.json | jq '{
  strategy: {
    version: .evo_after.strategy_version,
    replan_threshold: .evo_after.replan_threshold,
    replan_threshold_reason: .evo_after.replan_threshold_reason,
    tool_priorities: .evo_after.tool_priorities
  },
  rl: {
    episodes: .evo_after.rl_episode_count,
    avg_reward: .evo_after.rl_avg_reward,
    success_rate: .evo_after.rl_success_rate
  }
}'
```

### 查看偏好质量分布

```bash
cat eval_output/results.json | jq '{
  total: .evo_after.preference_count,
  high_conf: .evo_after.pref_high_conf_count,
  med_conf: .evo_after.pref_med_conf_count,
  low_conf: .evo_after.pref_low_conf_count,
  avg_confidence: .evo_after.pref_avg_confidence,
  tool_prefs: .evo_after.pref_tool_count,
  complexity_prefs: .evo_after.pref_complexity_count
}'
```

### 查看路由分布

```bash
cat eval_output/results.json | jq '{
  router_decisions: .evo_after.router_decisions,
  per_task: [.results[] | {id: .task_id, complexity: .complexity, model: .routed_model}]
}'
```

### 运行带用户反馈的偏好评测

```bash
# 仅运行偏好相关任务
ironclaw eval run --suite preference --live

# 在 JSON 中查看反馈对奖励的影响
cat eval_output/results.json | jq '.results[] | select(.tags[]? == "preference") | {
  id: .task_id,
  user_feedback: .user_feedback,
  episode_reward: .episode_reward,
  success: .success
}'
```

### Insights 纵向闭环评测（library API）

```go
// 在 Go 代码中使用 RunLongitudinal
runner := gw.NewEvalRunner()
result, err := eval.RunLongitudinal(ctx, runner, tasks, eval.LongitudinalConfig{
    Rounds:          3,
    MinTrajectories: 5,
    OutputDir:       "eval_output/longitudinal",
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.FormatMarkdown())
```

### Insights 纵向闭环评测（CLI）

```bash
# 多轮评测 + 每轮之间注入学习任务 + 强制触发 Insights
ironclaw eval longitudinal \
  --suite full \
  --with-workload workload_tasks.yaml \
  --iterations 3 \
  --force-insights \
  --live \
  -o eval_output/longitudinal_$(date +%Y%m%d)

# 查看纵向报告
cat eval_output/longitudinal_*/longitudinal_report.json | jq '.iterations[] | {
  iter: .iteration,
  success_rate: .summary.success_rate,
  replan_threshold: .replan_threshold,
  rl_avg_reward: .rl_avg_reward,
  pref_avg_conf: .pref_avg_confidence,
  router: .router_decisions
}'
```

### 对比两次 suite 结果

```bash
ironclaw eval compare before.json after.json

# JSON 输出中查看新增的 delta 字段
cat comparison.json | jq '.evo_snapshot | {
  strategy_version: .strategy_version_delta,
  replan_threshold: .replan_threshold_delta,
  rl_avg_reward: .rl_avg_reward_delta,
  rl_success_rate: .rl_success_rate_delta,
  tool_priorities: .tool_priority_deltas,
  router_changes: .router_model_changes
}'
```

---

## 设计决策

### 为什么 `InsightsTrigger` 不放入 `AgentRunner` 接口？

`AgentRunner` 是评测框架的核心抽象，所有 runner（DryRunner、mock runner、CognitiveAgentRunner）都需要实现它。将 `InsightsTrigger` 嵌入 `AgentRunner` 会强迫所有 runner 实现与进化引擎相关的方法，而大多数 runner 在单元测试中根本没有进化引擎。

独立接口 + 类型断言（`runner.(InsightsTrigger)`）的模式让 runner 按需实现，不支持时静默跳过并记录原因，完全向后兼容。

### 为什么 RL 统计用 7 天轨迹而非 24h？

原有代码读取 24h 内轨迹计算 `TrajectoryCount`，存在问题：eval 本身就是一次集中运行，当天的轨迹可能不足以反映策略演化趋势。改为 7 天窗口与进化引擎 `RunInsightsCycle()` 内部的 `-7*24*time.Hour` 保持一致，确保 eval snapshot 与 Insights 循环看到的是同一批数据。

### 为什么用户反馈不通过 `DispatchReflection` 注入？

通过 evolution 引擎 dispatch 一个额外的 `ReflectionEvent` 会产生不可预期的副作用：重复触发 `PreferenceLearner.OnReflectionComplete()`、`StrategyOptimizer.OnEpisodeComplete()` 等 hooks，污染在 eval 期间积累的进化数据。

当前方案仅在 `RunTask()` 内部重算 `EpisodeReward`，只影响 eval 报告中的奖励分数，不触碰任何 evolution hook，保证 eval 对进化系统的影响是纯观测性的。

### 为什么不扩展 `SkillSynthesizer` 质量评测？

经代码分析，`SkillSynthesizer` 内部的 `generated map[string]bool` 和 `PatternTracker.ToolPattern.AvgReward` 均为私有字段，没有暴露质量维度的公共方法。若要支持 Skill 质量评测，需要先在 `SkillSynthesizer` 上增加 `ListDrafts() []SkillDraft` 等公共访问器，这属于进化引擎内部的 API 设计，独立于本次改动范围，保留为后续工作。

### `RouterDecisions` 在 Snapshot 中的填充时机

`CaptureSnapshot()` 由 runner 调用，代表进化引擎状态的快照，而 `RouterDecisions` 是任务执行层面的统计（与进化引擎状态无关）。因此 `RouterDecisions` 不在 `CaptureSnapshot()` 内填充，而是在 `RunSuite`/`RunSuiteWithOptions` 完成所有任务后，通过 `aggregateRouterDecisions(suite.Results)` 注入到 `suite.EvoAfter` 中。这将路由聚合与进化状态快照解耦，即使 `SnapshotCaptor` 未实现，路由数据仍然可用。
