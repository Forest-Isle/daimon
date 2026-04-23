# 纵向分析（Longitudinal Analysis）

> 源文件：`internal/eval/longitudinal_runner.go`、`internal/eval/learning_metrics.go`、`cmd/ironclaw/eval.go`（`runLongitudinal`）、`cmd/ironclaw/eval_visualize.go`

---

## 目录

1. [模块职责](#1-模块职责)
2. [RunLongitudinal — 库级纵向运行器](#2-runlongitudinal--库级纵向运行器)
3. [InsightsTrigger 接口](#3-insightstrigger-接口)
4. [CLI 级纵向运行（eval longitudinal）](#4-cli-级纵向运行-eval-longitudinal)
5. [IterationPoint — 轮次数据点](#5-iterationpoint--轮次数据点)
6. [LearningCurveAnalysis — 学习曲线分析](#6-learningcurveanalysis--学习曲线分析)
7. [StrategyConvergenceAnalysis — 策略收敛分析](#7-strategyconvergenceanalysis--策略收敛分析)
8. [CompositeScore — 综合进化能力分](#8-compositescore--综合进化能力分)
9. [可视化（eval visualize）](#9-可视化-eval-visualize)
10. [纵向数据持久化格式](#10-纵向数据持久化格式)

---

## 1. 模块职责

纵向分析模块解决单次评测无法回答的核心问题：

> **Agent 是否在随时间真正变强？**

通过多轮 eval → 洞见 → eval 的循环，量化以下维度的变化趋势：
- RL 奖励均值（是否上升？）
- 成功率（是否提升？）
- 技能草稿数（是否积累？）
- 偏好条目数（是否增长？）
- 重规划阈值（是否收敛？）

---

## 2. RunLongitudinal — 库级纵向运行器

```go
func RunLongitudinal(
    ctx context.Context,
    runner AgentRunner,
    tasks []TaskCase,
    cfg LongitudinalConfig,
) (*LongitudinalResult, error)
```

### 2.1 LongitudinalConfig

```go
type LongitudinalConfig struct {
    Rounds          int     // 循环轮次（默认 2）
    MinTrajectories int     // 触发 InsightsCycle 所需最少轨迹数（默认 5）
    OutputDir       string  // per-round JSON 写入目录（可选）
}
```

### 2.2 每轮执行逻辑

```
Round N:
│
├─ RunSuite(ctx, runID, tasks, runner)
│      → SuiteResult
│
├─ Compare(prevSuite, currentSuite)     ← 第 0 轮跳过
│      → ComparisonReport（回归/改进检测）
│
├─ InsightsTrigger.TrajectoryCount()   ← 检查轨迹数是否达到阈值
│
├─ if count >= MinTrajectories:
│      InsightsTrigger.RunInsightsCycle()
│          → 读取过去 7 天轨迹
│          → GenerateInsights
│          → 应用到 StrategyOptimizer + PreferenceLearner
│      r.InsightsRan = true
│
└─ 写入 OutputDir/round_{N}.json（若配置）
```

**最后一轮不触发 InsightsCycle**（避免在评测完成后仍有异步修改）。

### 2.3 LongitudinalResult

```go
type LongitudinalResult struct {
    Config      LongitudinalConfig
    Rounds      []LongitudinalRound
    StartedAt   time.Time
    CompletedAt time.Time
}

type LongitudinalRound struct {
    RoundNumber    int
    Suite          *SuiteResult
    InsightsRan    bool
    InsightsReason string            // 未触发时记录原因
    Comparison     *ComparisonReport // 与上一轮对比（第 0 轮为 nil）
    Duration       time.Duration
}
```

---

## 3. InsightsTrigger 接口

```go
type InsightsTrigger interface {
    RunInsightsCycle() (ran bool, reason string)
    TrajectoryCount() int
}
```

`CognitiveAgentRunner` 实现此接口，通过调用进化引擎的 `RunInsightsCycle` 触发洞见生成：

```go
func (r *CognitiveAgentRunner) RunInsightsCycle() (bool, string) {
    if r.agent.EvolutionEngine() == nil {
        return false, "no evolution engine"
    }
    return r.agent.EvolutionEngine().RunInsightsCycle(ctx)
}

func (r *CognitiveAgentRunner) TrajectoryCount() int {
    snap := r.CaptureSnapshot()
    if snap == nil { return 0 }
    return snap.TrajectoryCount
}
```

轨迹数阈值 `MinTrajectories=5` 与进化引擎内部阈值对齐，低于此阈值的轨迹数据不足以生成有意义的洞见。

---

## 4. CLI 级纵向运行（eval longitudinal）

CLI 的 `eval longitudinal` 命令提供比库级 `RunLongitudinal` 更丰富的功能：

```bash
ironclaw eval longitudinal \
  --suite builtin \
  --live \
  --judge \
  -n 5 \
  --with-workload \
  --force-insights \
  --output-dir ./eval_output/longitudinal
```

### 4.1 与 RunLongitudinal 的区别

| 特性 | `RunLongitudinal`（库） | `eval longitudinal`（CLI） |
|------|------------------------|---------------------------|
| 每轮使用套件 | 固定 tasks | 可混合初始套件 + 工作负载 |
| 洞见触发 | 轨迹数阈值 | `--force-insights`（强制每轮） |
| 结果格式 | `LongitudinalResult` | `[]IterationPoint` + HTML |
| 学习曲线分析 | 无 | `ComputeSelfLearningAnalysis` |
| HTML 可视化 | 无 | `GenerateLearningCurveHTML` |

### 4.2 CLI 流程

```
eval longitudinal
│
├─ initEvalGateway()                        // 创建评测 Gateway
├─ gateway.NewEvalRunner()                  // CognitiveAgentRunner
├─ loadSuite(suite)                         // 初始任务套件
│
├─ for i := 0; i < iterations; i++:
│   ├─ RunSuiteWithOptions(tasks, opts)
│   ├─ 记录 IterationPoint {
│   │      Iteration, RunID, Summary,
│   │      EvoSnapshot, RLAvgReward,
│   │      SkillDraftCount, PreferenceCount
│   │  }
│   │
│   ├─ if --force-insights:
│   │      runner.RunInsightsCycle()
│   │
│   ├─ if --with-workload:
│   │      WorkloadSuite() → RunSuiteWithOptions
│   │      （注入工作负载任务，增加轨迹量）
│   │
│   └─ Save iteration_{i}.json
│
├─ ComputeSelfLearningAnalysis(iterations)  // 学习曲线 + 策略收敛 + 综合分
├─ SaveLongitudinalJSON(points, output)     // 纵向 JSON
└─ GenerateLearningCurveHTML(points, html)  // HTML 可视化
```

---

## 5. IterationPoint — 轮次数据点

`IterationPoint` 是纵向分析的基本数据单元，每轮运行生成一个：

```go
type IterationPoint struct {
    Iteration     int
    RunID         string
    Summary       *SuiteSummary      // 当轮汇总（成功率/平均分/平均奖励等）
    EvoSnapshot   *EvolutionSnapshot // 当轮进化快照
    RLAvgReward   float64            // 当轮平均 RL 奖励（便于直接访问）
    SkillDraftCount int              // 当轮技能草稿数
    PreferenceCount int              // 当轮偏好条目数
    Timestamp     time.Time
}
```

时间序列 `[]IterationPoint` 是所有学习曲线分析的输入。

---

## 6. LearningCurveAnalysis — 学习曲线分析

```go
func ComputeLearningCurve(points []IterationPoint) *LearningCurveAnalysis
```

需要至少 2 个轮次数据点。使用**线性回归（最小二乘法）**计算趋势斜率。

### 6.1 分析维度

| 字段 | 来源 | 含义 |
|------|------|------|
| `RewardSlope` | `RLAvgReward` 线性回归斜率 | 正 = 奖励上升，负 = 下降 |
| `SuccessRateSlope` | `Summary.SuccessRate` 线性回归斜率 | 正 = 成功率上升 |
| `SkillGrowthPerIter` | SkillDraftCount 各轮差值均值 | 平均每轮新增技能草稿数 |
| `PreferenceGrowthPerIter` | PreferenceCount 各轮差值均值 | 平均每轮新增偏好条目数 |
| `RewardVelocity` | 奖励斜率分类 | improving / degrading / stable / insufficient_data |
| `SuccessVelocity` | 成功率斜率分类 | improving / degrading / stable / insufficient_data |

### 6.2 Velocity 分类阈值

```go
// 奖励趋势判断（阈值 0.01）
if slope > 0.01  → VelocityImproving
if slope < -0.01 → VelocityDegrading
else             → VelocityStable

// 成功率趋势判断（阈值 0.005）
if slope > 0.005  → VelocityImproving
if slope < -0.005 → VelocityDegrading
else              → VelocityStable
```

### 6.3 线性回归实现

```go
func lmLinearSlope(values []float64) float64 {
    // 最小二乘法：slope = Σ(x_i - x̄)(y_i - ȳ) / Σ(x_i - x̄)²
    // x_i = 轮次序号 (0, 1, 2, ...)
    // y_i = 对应指标值
}
```

---

## 7. StrategyConvergenceAnalysis — 策略收敛分析

```go
func ComputeStrategyConvergence(points []IterationPoint) *StrategyConvergenceAnalysis
```

评估 Agent 的重规划阈值（`ReplanThreshold`）是否趋于稳定：

### 7.1 分析指标

| 字段 | 含义 | 收敛判定 |
|------|------|----------|
| `ThresholdMean` | 所有轮次 ReplanThreshold 均值 | — |
| `ThresholdStdDev` | 标准差 | 低 = 稳定 |
| `OscillationScore` | 连续轮次变化的均绝对偏差（MAD） | < 0.02 = 收敛 |
| `ThresholdTrend` | "rising" / "falling" / "stable" | — |
| `IsConverged` | OscillationScore < 0.02 | true = 收敛 |

```go
// OscillationScore 计算
changes := []float64{}
for i := 1; i < len(thresholds); i++ {
    changes = append(changes, math.Abs(thresholds[i] - thresholds[i-1]))
}
oscillationScore = mean(changes)
isConverged = oscillationScore < 0.02
```

---

## 8. CompositeScore — 综合进化能力分

```go
func ComputeCompositeScore(
    curve *LearningCurveAnalysis,
    convergence *StrategyConvergenceAnalysis,
    lastSummary *SuiteSummary,
) float64
```

将三个维度归一化后加权求和（0.0 ～ 1.0）：

| 维度 | 权重 | 计算方式 |
|------|------|----------|
| 当前成功率 | 40% | `lastSummary.SuccessRate` |
| 学习趋势 | 40% | 奖励斜率 + 成功率斜率归一化 |
| 策略稳定性 | 20% | `1 - OscillationScore`（收敛 = 高分） |

**综合分 > 0.7** 代表 Agent 整体处于良性进化状态。

### 8.1 ComputeSelfLearningAnalysis

```go
func ComputeSelfLearningAnalysis(points []IterationPoint) *SelfLearningAnalysisSummary
```

整合三项分析，返回：

```go
type SelfLearningAnalysisSummary struct {
    LearningCurve       *LearningCurveAnalysis
    StrategyConvergence *StrategyConvergenceAnalysis
    CompositeScore      float64
    GeneratedAt         time.Time
}
```

---

## 9. 可视化（eval visualize）

```bash
ironclaw eval visualize \
  -i ./eval_output/longitudinal/longitudinal.json \
  -o ./eval_output/longitudinal/learning_curve.html
```

`GenerateLearningCurveHTML(points []IterationPoint, outputPath string)` 生成内嵌 Chart.js 的 HTML 文件，包含：

### 9.1 图表内容

| 图表 | X 轴 | Y 轴 |
|------|------|------|
| 成功率曲线 | 迭代轮次 | SuccessRate (%) |
| 平均 RL 奖励曲线 | 迭代轮次 | RLAvgReward |
| 平均最终分曲线 | 迭代轮次 | AvgFinalScore |
| 技能草稿增长曲线 | 迭代轮次 | SkillDraftCount |
| 偏好条目增长曲线 | 迭代轮次 | PreferenceCount |
| 重规划阈值趋势 | 迭代轮次 | ReplanThreshold |

### 9.2 自学习摘要卡片

HTML 还包含 `SelfLearningAnalysisSummary` 的摘要展示：
- 综合进化能力分（CompositeScore）
- 学习趋势（Improving / Stable / Degrading）
- 策略收敛状态（IsConverged）
- 每轮技能/偏好增长量

---

## 10. 纵向数据持久化格式

### 10.1 逐轮 JSON（OutputDir 模式）

```
eval_output/longitudinal/
├── round_0.json     ← LongitudinalRound（含 SuiteResult + Comparison）
├── round_1.json
├── round_2.json
└── report.json      ← LongitudinalResult（含所有 rounds）
```

### 10.2 CLI 模式纵向 JSON

```
eval_output/longitudinal/
├── iteration_0.json   ← SuiteResult
├── iteration_1.json
├── iteration_2.json
├── longitudinal.json  ← []IterationPoint（用于可视化）
└── learning_curve.html
```

### 10.3 IterationPoint JSON 示例

```json
{
  "iteration": 2,
  "run_id": "longitudinal-r2-1745000000",
  "summary": {
    "total_tasks": 20,
    "success_rate": 0.85,
    "avg_final_score": 0.78,
    "avg_episode_reward": 0.72
  },
  "evo_snapshot": {
    "preference_count": 15,
    "strategy_version": 3,
    "skill_draft_count": 4,
    "trajectory_count": 12,
    "replan_threshold": 0.62,
    "rl_avg_reward": 0.71
  },
  "rl_avg_reward": 0.71,
  "skill_draft_count": 4,
  "preference_count": 15,
  "timestamp": "2026-04-22T10:30:00Z"
}
```
