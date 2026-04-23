# 核心框架（Harness）

> 源文件：`internal/eval/harness.go`、`internal/eval/dimension.go`、`internal/eval/taskset.go`、`internal/eval/compare.go`

---

## 目录

1. [模块职责](#1-模块职责)
2. [TaskCase — 任务定义](#2-taskcase--任务定义)
3. [EvalResult — 单任务结果](#3-evalresult--单任务结果)
4. [EvolutionSnapshot — 进化快照](#4-evolutionsnapshot--进化快照)
5. [SuiteResult — 套件结果](#5-suiteresult--套件结果)
6. [AgentRunner 接口体系](#6-agentrunner-接口体系)
7. [RunSuite vs RunSuiteWithOptions](#7-runsuite-vs-runsuitewwithoptions)
8. [Dimension 枚举与 VerifyMethod](#8-dimension-枚举与-verifymethod)
9. [任务套件文件加载（taskset.go）](#9-任务套件文件加载-tasksetgo)
10. [比较与回归检测（compare.go）](#10-比较与回归检测-comparego)
11. [纵向报告结构](#11-纵向报告结构)

---

## 1. 模块职责

Harness 是整个评测系统的**核心骨架**，定义：

- 所有数据结构（`TaskCase`、`EvalResult`、`SuiteResult` 等）
- 执行协调逻辑（`RunSuite`、`RunSuiteWithOptions`）
- 横向接口抽象（`AgentRunner`、`SnapshotCaptor`、`CogHealthCaptor`）
- 结果汇总、持久化和回归比较

其他模块（Runner、Scorer、Diagnosis）都依赖 harness 的类型定义，但 harness 本身不依赖它们——保持单向依赖。

---

## 2. TaskCase — 任务定义

```go
type TaskCase struct {
    ID          string       // 唯一标识，如 "plan-01"
    Goal        string       // 自然语言任务目标，直接发送给 Agent
    Complexity  string       // "simple" | "moderate" | "complex"
    Tags        []string     // 自由标签，用于过滤和分析
    ExpectTools []string     // 期望 Agent 使用的工具列表

    // 验证配置
    Dimension    Dimension    // 评测维度（见第 8 节）
    VerifyMethod VerifyMethod // 验证方式（deterministic/llm_judge/hybrid）
    Reference    *Reference   // 规则验证配置（可选）
    Rubric       *Rubric      // LLM Judge 评判标准（可选）

    // 生命周期钩子
    SetupFunc    func() error
    CleanupFunc  func() error
    SetupWithRunner   func(ctx, runner) error  // 优先于 SetupFunc
    CleanupWithRunner func(ctx, runner) error  // 优先于 CleanupFunc
    SuccessFunc  func(*EvalResult) bool        // 程序化成功判定（可覆盖 Agent 自评）

    UserFeedback float64  // 模拟用户评分：-1.0 ～ +1.0（0 = 无反馈）
}
```

### 2.1 Reference（规则验证配置）

```go
type Reference struct {
    Answer         string      // 期望的完整输出（精确匹配）
    MustContain    []string    // 输出中必须包含的字符串
    MustNotContain []string    // 输出中不得包含的字符串
    FileChecks     []FileCheck // 文件系统状态检查
    ExitCode       *int        // 期望的 bash 退出码
}

type FileCheck struct {
    Path        string  // 文件路径
    MustExist   bool    // 是否必须存在
    Contains    string  // 文件内容必须包含
    NotContains string  // 文件内容不得包含
}
```

### 2.2 Rubric（LLM 评判标准）

```go
type Rubric struct {
    Criteria []JudgeCriterion
}

type JudgeCriterion struct {
    Name        string  // 维度名称，如 "correctness"
    Description string  // 评判说明（传给 LLM）
    Weight      float64 // 权重（各维度权重之和建议为 1.0）
}
```

### 2.3 SetupWithRunner 的意义

当 setup 需要注入记忆 Fixture（`MemoryAwareRunner.InjectMemory`）时，必须使用 `SetupWithRunner` 而非 `SetupFunc`，因为后者没有 runner 引用：

```go
SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
    mr, ok := runner.(MemoryAwareRunner)
    if !ok { return nil }
    return mr.InjectMemory(ctx, memory.Entry{ID: "fact-01", Content: "..."})
},
CleanupWithRunner: func(ctx context.Context, runner AgentRunner) error {
    mr, ok := runner.(MemoryAwareRunner)
    if !ok { return nil }
    return mr.CleanupMemory(ctx, "fact-01")
},
```

---

## 3. EvalResult — 单任务结果

```go
type EvalResult struct {
    // 基础信息
    TaskID     string
    Goal       string
    Complexity string
    Dimension  Dimension
    Timestamp  time.Time

    // 执行结果
    Success     bool
    Duration    time.Duration
    Error       string          // 非空表示执行异常
    AgentOutput string          // Agent 最终输出文本

    // 工具与循环指标
    ToolsUsed    []string
    ReplanCount  int
    ToolExecStats []ToolExecStat  // 按工具名聚合的调用统计

    // Assertion 统计（来自 OBSERVE 阶段）
    AssertionTotal    int
    AssertionPassed   int
    AssertionPassRate float64

    // 模型路由
    RoutedModel string           // ModelRouter 选出的模型名

    // 评分
    VerifyResult *VerifyResult   // 规则检查结果（Reference 验证后填充）
    JudgeResult  *JudgeResult    // LLM Judge 结果（Rubric 评分后填充）
    FinalScore   float64         // 0.0 ～ 1.0 综合分
    Confidence   float64         // Agent 自报信心值

    // 进化与压缩
    EpisodeReward     float64          // ComputeReward 计算的 RL 奖励值
    UserFeedback      float64          // 来自 TaskCase 的模拟用户评分
    CompressionCount  int              // 上下文压缩触发次数
    CompressionEvents []CompressionEvent

    FailureCategory string  // 失败分类（FailureClassifier 填充）
}
```

### 3.1 ToolExecStat

```go
type ToolExecStat struct {
    ToolName        string
    CallCount       int
    SuccessCount    int
    FailCount       int
    SuccessRate     float64
    AvgDurationMs   float64
    TotalDurationMs int64
}
```

### 3.2 CompressionEvent

```go
type CompressionEvent struct {
    Reason    string   // 触发原因（"proactive" | "reactive_413" 等）
    LayersRun int      // 运行的压缩层数
    BeforePct float64  // 压缩前上下文占用百分比
    AfterPct  float64  // 压缩后上下文占用百分比
}
```

---

## 4. EvolutionSnapshot — 进化快照

在套件运行**前后**各采集一次，用于量化进化增益。

```go
type EvolutionSnapshot struct {
    // 数量指标
    PreferenceCount int  // 已学习的偏好条目数
    StrategyVersion int  // 策略优化器版本号
    SkillDraftCount int  // 技能草稿数量
    TrajectoryCount int  // 轨迹文件数量

    // 策略参数
    ReplanThreshold      float64             // 当前重规划阈值
    ReplanThresholdPrev  float64             // 上一版本重规划阈值
    ReplanThresholdReason string             // 阈值变化原因
    ToolPriorities       map[string]float64  // 工具优先级权重

    // RL 统计（来自近期轨迹）
    RLEpisodeCount int
    RLAvgReward    float64
    RLSuccessRate  float64
    RLAvgProgress  float64

    // 偏好质量分布
    PreferenceHighConfCount   int     // confidence >= 0.8
    PreferenceMedConfCount    int     // 0.4 <= confidence < 0.8
    PreferenceLowConfCount    int     // confidence < 0.4
    PreferenceAvgConfidence   float64
    PreferenceToolCount       int     // 工具偏好条目数
    PreferenceComplexityCount int     // 复杂度处理偏好条目数

    // 路由决策（EvoAfter 才有）
    RouterDecisions map[string]int  // model_name → 路由次数
}
```

### 4.1 EvoSnapshotDiff

`Compare(before, after)` 计算差异，`EvoSnapshotDiff` 包含 4 个 delta 字段：

```go
type EvoSnapshotDiff struct {
    PreferenceCountDelta int     // 新增偏好数
    StrategyVersionDelta int     // 策略版本增量
    SkillDraftDelta      int     // 新增技能草稿数
    TrajectoryCountDelta int     // 新增轨迹数
}
```

---

## 5. SuiteResult — 套件结果

```go
type SuiteResult struct {
    RunID     string
    Results   []EvalResult
    StartedAt time.Time
    Duration  time.Duration

    EvoBefore *EvolutionSnapshot      // 套件执行前快照
    EvoAfter  *EvolutionSnapshot      // 套件执行后快照
    CogHealth *cogmetrics.HealthReport // 认知健康报告（evolution 开启时）
    FeatureState map[string]bool       // 当次运行的 Feature 开关状态
}
```

### 5.1 Summary()

`SuiteResult.Summary()` 返回 `SuiteSummary`：

| 字段 | 含义 |
|------|------|
| `TotalTasks` | 任务总数 |
| `PassedTasks` | 通过数 |
| `FailedTasks` | 失败数 |
| `SuccessRate` | 通过率（0.0 ～ 1.0） |
| `AvgDuration` | 平均耗时 |
| `AvgFinalScore` | 平均最终分 |
| `AvgAssertionPassRate` | 平均 Assertion 通过率 |
| `AvgEpisodeReward` | 平均 RL 奖励 |
| `AvgReplanCount` | 平均重规划次数 |
| `AvgCompressionCount` | 平均压缩次数 |
| `DimScores` | 按维度聚合的分数（`[]DimensionScore`） |

### 5.2 SaveJSON(path)

自动创建父目录，将完整 `SuiteResult` 序列化为 JSON 保存。

---

## 6. AgentRunner 接口体系

```
AgentRunner (必须实现)
├── RunTask(ctx, task) (*EvalResult, error)

SnapshotCaptor (可选)
├── CaptureSnapshot() *EvolutionSnapshot

CogHealthCaptor (可选)
├── CaptureCogHealth() *cogmetrics.HealthReport

MemoryAwareRunner (可选)
├── InjectMemory(ctx, entries...) error
└── CleanupMemory(ctx, ids...) error
```

`RunSuite` 通过类型断言检测可选接口，按需调用：

```go
if sc, ok := runner.(SnapshotCaptor); ok {
    suite.EvoBefore = sc.CaptureSnapshot()
}
// ... 任务循环 ...
if sc, ok := runner.(SnapshotCaptor); ok {
    suite.EvoAfter = sc.CaptureSnapshot()
}
if chc, ok := runner.(CogHealthCaptor); ok {
    suite.CogHealth = chc.CaptureCogHealth()
}
```

---

## 7. RunSuite vs RunSuiteWithOptions

| 特性 | `RunSuite` | `RunSuiteWithOptions` |
|------|-----------|----------------------|
| 进化快照 | ✅ | ✅ |
| 认知健康报告 | ✅ | ✅ |
| 规则验证（Reference） | ❌ | ✅（有 Reference 时自动触发） |
| LLM Judge（Rubric） | ❌ | ✅（opts.Judge 不为 nil 时） |
| FinalScore 计算 | ❌ | ✅ |
| FailureCategory | ❌ | ✅（可选配） |
| Dimension 默认填充 | ❌ | ✅ |

`RunOptions` 结构：

```go
type RunOptions struct {
    Judge *LLMJudge  // nil = 禁用 LLM Judge
}
```

### 7.1 FinalScore 计算逻辑（ComputeFinalScore）

根据 `VerifyMethod` 混合三个信号：

| VerifyMethod | FinalScore 计算方式 |
|---|---|
| `deterministic` | `VerifyResult.Score`（规则验证分） |
| `llm_judge` | `JudgeResult.Overall`（LLM Judge 分） |
| `hybrid` | `0.5 * VerifyResult.Score + 0.5 * JudgeResult.Overall` |
| 无（空） | `AssertionPassRate`（Assertion 通过率） |

---

## 8. Dimension 枚举与 VerifyMethod

### 8.1 Dimension

```go
const (
    DimTaskExecution   Dimension = "task_execution"   // 通用任务执行（默认）
    DimPlanning        Dimension = "planning"          // 规划与分解
    DimErrorRecovery   Dimension = "error_recovery"   // 错误恢复
    DimToolSelection   Dimension = "tool_selection"   // 工具选择准确性
    DimConversation    Dimension = "conversation"      // 对话理解
    DimMemory          Dimension = "memory"            // 跨会话记忆
    DimKnowledge       Dimension = "knowledge"         // 知识库检索
    DimMultiAgent      Dimension = "multi_agent"       // 多 Agent 协作
    DimSkillLearning   Dimension = "skill_learning"   // 技能迁移
    DimPreferenceAdherence Dimension = "preference_adherence" // 偏好遵循
    DimMemoryRetention Dimension = "memory_retention" // 记忆留存
)
```

`DefaultDimension(dim)` 在 dim 为空时返回 `DimTaskExecution`。

### 8.2 VerifyMethod

```go
const (
    VerifyDeterministic VerifyMethod = "deterministic" // 仅规则检查
    VerifyLLMJudge      VerifyMethod = "llm_judge"     // 仅 LLM 评分
    VerifyHybrid        VerifyMethod = "hybrid"         // 规则 + LLM 各半
)
```

### 8.3 AggregateDimensions

```go
func AggregateDimensions(results []EvalResult) *DimensionReport
```

按维度分组，计算每个维度的：
- `TaskCount`：任务数
- `SuccessRate`：成功率
- `AvgScore`：平均 FinalScore
- `AvgReplan`：平均重规划次数
- `TopFailures`：Top-3 失败类别

`DimensionReport.Weakest`（AvgScore < 0.7）和 `Strongest`（AvgScore >= 0.8）各取 Top-3，用于雷达图渲染。

---

## 9. 任务套件文件加载（taskset.go）

```go
// 从 YAML 文件加载任务套件
func LoadTaskSetYAML(path string) ([]TaskCase, error)

// 从 JSON 文件加载任务套件
func LoadTaskSetJSON(path string) ([]TaskCase, error)
```

外部文件格式（YAML 示例）：

```yaml
- id: "code-review-01"
  goal: "Review the following Go function and identify potential issues"
  complexity: "moderate"
  dimension: "task_execution"
  verify_method: "llm_judge"
  rubric:
    criteria:
      - name: correctness
        description: "Correctly identifies real bugs"
        weight: 0.6
      - name: clarity
        description: "Explanation is clear and actionable"
        weight: 0.4
  tags: ["code", "review"]
```

**注意**：`SetupFunc`、`CleanupFunc`、`SuccessFunc` 等函数类型无法从文件加载，只能在 Go 代码中定义（Fixture 库）。

---

## 10. 比较与回归检测（compare.go）

```go
func Compare(before, after *SuiteResult) *ComparisonReport
```

### 10.1 ComparisonReport 结构

```go
type ComparisonReport struct {
    Before        *SuiteSummary
    After         *SuiteSummary
    SuccessRateDelta float64       // after - before
    AvgScoreDelta    float64
    Regressions   []string        // 由 PASS 变为 FAIL 的任务 ID
    Improvements  []string        // 由 FAIL 变为 PASS 的任务 ID
    EvoSnapshotDiff *EvoSnapshotDiff
}
```

### 10.2 FormatMarkdown()

输出包含：

- 核心指标对比表（成功率、平均分、平均 Assertion 率）
- 进化快照变化表（4 个 delta 字段）
- 回归任务列表（Regressions）
- 改进任务列表（Improvements）

### 10.3 CI 回归判定

```bash
ironclaw eval compare \
  --before eval_output/baseline.json \
  --after  eval_output/ci_results.json \
  --fail-on-regression
# 若有 Regressions，exit code = 1
```

---

## 11. 纵向报告结构

```go
// 单次迭代记录点
type IterationPoint struct {
    Iteration int
    RunID     string
    Summary   *SuiteSummary
    EvoSnapshot *EvolutionSnapshot
    Timestamp time.Time
}

// 跨轮次纵向学习曲线报告
type LongitudinalReport struct {
    Iterations []IterationPoint
    // ComputeSelfLearningAnalysis 结果嵌入
    LearningVelocity    float64    // 每轮学习速率
    StrategyConvergence float64    // 策略收敛得分
    CompositeScore      float64    // 综合进化能力分
}
```

纵向 JSON 文件通过 `SaveLongitudinalJSON` / `LoadLongitudinalJSON` 读写，供 `eval visualize` 渲染为 HTML 图表。
