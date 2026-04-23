# 诊断与自适应（Diagnosis & Adaptive）

> 源文件：`internal/eval/classifier.go`、`internal/eval/diagnosis.go`、`internal/eval/adaptive.go`

---

## 目录

1. [模块职责](#1-模块职责)
2. [失败分类器（FailureClassifier）](#2-失败分类器-failureclassifier)
3. [诊断引擎（Diagnose）](#3-诊断引擎-diagnose)
4. [WeaknessReport — 弱点报告](#4-weaknessreport--弱点报告)
5. [内置优化建议映射表](#5-内置优化建议映射表)
6. [自适应任务生成（AdaptiveGenerator）](#6-自适应任务生成-adaptivegenerator)
7. [RunAdaptiveLoop — 自适应评测循环](#7-runadaptiveloop--自适应评测循环)
8. [eval diagnose 命令流程](#8-eval-diagnose-命令流程)

---

## 1. 模块职责

诊断与自适应模块将「评测结果」转化为「可执行的改进建议」，并形成**自动化的弱点补强循环**：

```
SuiteResult
    │
    ▼
FailureClassifier.ClassifyAll()   ← 分类每个失败任务的根因
    │  []EvalResult (with FailureCategory)
    ▼
Diagnose()                        ← 聚合分析 → WeaknessReport
    │  []Weakness + []Recommendation
    ▼
AdaptiveGenerator.Generate()      ← 针对弱点生成新任务
    │  []GeneratedTask
    ▼
RunSuiteWithOptions(new tasks)    ← 再次运行，验证改进效果
    │
    └── 循环 N 轮 (RunAdaptiveLoop)
```

---

## 2. 失败分类器（FailureClassifier）

### 2.1 结构

```go
type FailureClassifier struct {
    provider    agent.Provider  // nil = 纯规则模式（不调用 LLM）
    maxDuration time.Duration   // 超时判定阈值（默认 5 分钟）
}
```

### 2.2 失败类别（12 类）

| 类别 | 标识 | 触发规则 |
|------|------|----------|
| 规划错误 | `planning_error` | complex 任务 / planning 维度且 FinalScore < 0.5 |
| 工具误用 | `tool_misuse` | 期望工具未使用（ExpectTools vs ToolsUsed 集合差） |
| 工具缺失 | `tool_missing` | 需要但不存在的工具（LLM 分类） |
| 错误无恢复 | `error_no_recovery` | Error 非空 + ReplanCount == 0 |
| 重试死循环 | `error_loop_retry` | ReplanCount > 3 |
| 幻觉 | `hallucination` | `must_not_contain` 检查失败 或 JudgeResult.Weaknesses 含 "hallucin" |
| 不完整回答 | `incomplete_answer` | JudgeResult.Weaknesses 含 "incomplete" 或 Overall < 0.4 |
| 错误答案 | `wrong_answer` | JudgeResult.Weaknesses 含 "wrong"/"incorrect" 或 VerifyResult.Score < 0.5 |
| 超时 | `timeout` | Duration > maxDuration |
| 上下文丢失 | `context_lost` | LLM 分类 |
| 过度工程 | `over_engineering` | ToolsUsed 数量 > ExpectTools * 3 |
| 未知 | `unknown` | 规则全部不匹配且 LLM 分类失败 |

### 2.3 两阶段分类策略

```go
func (c *FailureClassifier) Classify(ctx, task, result) FailureCategory {
    // 阶段 1：规则启发式（快速，无 LLM 调用）
    if cat := c.classifyByRules(task, result); cat != FailUnknown {
        return cat
    }
    // 阶段 2：LLM 分类（慢，仅当 provider != nil）
    if c.provider != nil {
        if cat := c.classifyByLLM(ctx, task, result); cat != "" {
            return cat
        }
    }
    return FailUnknown
}
```

**规则优先**：绝大多数失败可以通过规则快速分类，LLM 分类仅作为兜底，避免大量 API 调用。

### 2.4 LLM 分类 Prompt

Prompt 包含：任务 ID/目标/维度/期望工具、结果（成功标志/FinalScore/Error/工具列表/重规划次数/输出截断 500 字符），要求 LLM 仅返回一个类别名。LLM 返回值会与 `AllFailureCategories()` 做合法性验证，非法值视为分类失败。

### 2.5 批量分类

```go
func (c *FailureClassifier) ClassifyAll(ctx, tasks []TaskCase, results []EvalResult) []EvalResult
```

返回拷贝的 results 列表（不修改原始），每个失败的 `EvalResult.FailureCategory` 被填充。已有 `FailureCategory` 的结果跳过。

---

## 3. 诊断引擎（Diagnose）

```go
func Diagnose(ctx context.Context, suite *SuiteResult, opts *DiagnoseOptions) *WeaknessReport
```

### 3.1 DiagnoseOptions

```go
type DiagnoseOptions struct {
    Classifier *FailureClassifier  // nil = 跳过分类步骤
    Tasks      []TaskCase          // 与 suite.Results 对应的任务定义
}
```

### 3.2 执行流程

```
Diagnose(ctx, suite, opts)
│
├─ 1. [可选] FailureClassifier.ClassifyAll(tasks, results)
│         → 为每个失败结果填充 FailureCategory
│
├─ 2. AggregateDimensions(results)
│         → DimensionReport（各维度成功率/平均分/Top失败类别）
│
├─ 3. 计算全局指标
│         → OverallScore (平均 FinalScore)
│         → FailedTasks (Success=false 或 FinalScore < 0.5)
│
├─ 4. identifyWeaknesses(results, dimReport)
│         → 按 (FailureCategory, Dimension) 分组
│         → 每组生成一个 Weakness（含严重度/证据/描述）
│
└─ 5. generateRecommendations(weaknesses)
          → 按内置映射表生成可操作建议
```

### 3.3 弱点严重度判定

| 出现次数 | 严重度 |
|---------|--------|
| ≥ 3 | `critical` |
| ≥ 2 | `major` |
| = 1 | `minor` |

弱点列表按严重度降序、频次降序排列。

---

## 4. WeaknessReport — 弱点报告

```go
type WeaknessReport struct {
    GeneratedAt     time.Time
    OverallScore    float64          // 平均 FinalScore
    TotalTasks      int
    FailedTasks     int
    DimReport       *DimensionReport  // 各维度聚合
    Weaknesses      []Weakness        // 识别出的弱点（按严重度排序）
    Recommendations []Recommendation  // 优化建议
}

type Weakness struct {
    ID          string           // "W-001", "W-002", ...
    Severity    string           // "critical" | "major" | "minor"
    Category    FailureCategory
    Dimension   Dimension
    Description string           // 自然语言描述
    Evidence    []string         // 触发此弱点的任务 ID 列表
    Frequency   int
}

type Recommendation struct {
    TargetWeakness string  // 指向哪个 Weakness ID
    Priority       int     // 从 1 开始，越小越优先
    Action         string  // 简短动作描述
    Component      string  // 涉及的源码组件
    Detail         string  // 详细优化说明
}
```

### 4.1 FormatMarkdown

`WeaknessReport.FormatMarkdown()` 输出包含：

1. 概要（总分/任务数/失败数）
2. 维度分数表（Dimension / Tasks / SuccessRate / AvgScore / AvgReplan / TopFailures）
3. 弱点列表（按严重度排序，每条含维度/类别/频次/证据）
4. 优化建议表（Priority / Target / Action / Component / Detail）
5. 失败分布表（FailureCategory → Count，按频次降序）

---

## 5. 内置优化建议映射表

每个失败类别都有预定义的优化建议，指向具体源码组件：

| 失败类别 | 优化动作 | 目标组件 |
|----------|----------|----------|
| `error_loop_retry` | 降低 maxReplans，优化 REFLECT Prompt | `agent/cognitive.go (REFLECT phase)` |
| `tool_misuse` | 增强 PERCEIVE 阶段工具描述，利用进化工具偏好 | `agent/cognitive.go (PERCEIVE), evolution/preference.go` |
| `hallucination` | 强化 OBSERVE 断言，系统 Prompt 加"不确定时承认不知道" | `agent/assertion.go, system prompt` |
| `planning_error` | 优化 PLAN Prompt，调整 ContextBudgetAllocator | `agent/cognitive.go (PLAN), context_budget.go` |
| `timeout` | 调整 LLM 超时，考虑 SubAgentManager 任务拆分 | `config (llm.timeout), agent/cognitive.go` |
| `incomplete_answer` | REFLECT 阶段增加完整性自检 | `agent/cognitive.go (REFLECT phase)` |
| `context_lost` | 调整 CompressionPipeline 阈值，增加对话上下文预算 | `agent/context_manager.go` |
| `over_engineering` | 系统 Prompt 加 YAGNI 原则 | `system prompt, agent/cognitive.go (PLAN)` |
| `error_no_recovery` | 改善 ACT 阶段错误处理 | `agent/cognitive.go (ACT), tool/interceptor.go` |
| `wrong_answer` | OBSERVE 阶段加验证步骤 | `agent/cognitive.go (OBSERVE)` |

---

## 6. 自适应任务生成（AdaptiveGenerator）

### 6.1 设计

`AdaptiveGenerator` 使用 LLM 基于 `WeaknessReport` 自动生成**针对弱点**的新评测任务，实现"哪里弱就测哪里"的自适应评测策略。

```go
type AdaptiveGenerator struct {
    provider agent.Provider  // 必须非 nil（任务生成需要 LLM）
}
```

### 6.2 Generate 策略

```go
func (g *AdaptiveGenerator) Generate(ctx, report *WeaknessReport, count int) ([]GeneratedTask, error)
```

- 取 `report.Weaknesses` 中严重度最高的 Top-3 弱点
- 每个弱点生成 `count / 3` 个任务（至少 1 个）
- 超出 count 的部分截断

### 6.3 LLM Prompt 构造

为每个 Weakness 构造 Prompt，要求 LLM 生成 JSON 数组，每个元素包含：

| 字段 | 说明 |
|------|------|
| `id` | 以 "adaptive-" 前缀，确保唯一 |
| `goal` | 自然语言任务描述 |
| `complexity` | simple / moderate / complex |
| `tags` | 必须包含 "adaptive" 和维度名 |
| `expect_tools` | 期望工具列表 |
| `dimension` | 继承自 Weakness.Dimension |
| `verify_method` | deterministic / llm_judge / hybrid |
| `must_contain` | 确定性验证时期望的输出词 |
| `rationale` | 解释为什么这个任务能测到该弱点 |

### 6.4 GeneratedTask

```go
type GeneratedTask struct {
    TaskCase                      // 内嵌标准 TaskCase
    TargetWeakness string         // 指向的 Weakness ID
    Rationale      string         // 生成理由
}
```

生成的 `TaskCase` 会自动从 `must_contain` 字段构建 `Reference.MustContain`，并根据 `verify_method` 设置 `VerifyMethod`。

---

## 7. RunAdaptiveLoop — 自适应评测循环

### 7.1 流程

```go
func RunAdaptiveLoop(ctx context.Context, opts AdaptiveLoopOptions) (*AdaptiveSummary, error)
```

```
初始套件
│
▼
Round 1: RunSuiteWithOptions(initial suite)
    │  SuiteResult
    ▼
Diagnose() → WeaknessReport
    │
    ▼
AdaptiveGenerator.Generate(report, tasksPerRound)
    │  []GeneratedTask
    ▼
Round 2: RunSuiteWithOptions(generated tasks)
    │  SuiteResult
    ▼
... 重复 N 轮 ...
    │
    ▼
AdaptiveSummary {
    Rounds []RoundResult
    ImprovedWeaknesses []string
    RemainingWeaknesses []string
}
```

### 7.2 AdaptiveLoopOptions

```go
type AdaptiveLoopOptions struct {
    Runner        AgentRunner
    InitialTasks  []TaskCase
    Generator     *AdaptiveGenerator
    Classifier    *FailureClassifier
    Judge         *LLMJudge
    Rounds        int    // 最大轮次（默认 3）
    TasksPerRound int    // 每轮生成任务数（默认 5）
    RunID         string
}
```

### 7.3 AdaptiveSummary

```go
type AdaptiveSummary struct {
    TotalRounds         int
    InitialSuccessRate  float64
    FinalSuccessRate    float64
    ImprovedWeaknesses  []string  // 已改善的弱点 ID
    RemainingWeaknesses []string  // 仍存在的弱点 ID
    RoundResults        []RoundResult
}

type RoundResult struct {
    Round       int
    TaskCount   int
    SuccessRate float64
    WeaknessIDs []string  // 本轮针对的弱点
}
```

---

## 8. eval diagnose 命令流程

```bash
ironclaw eval diagnose --suite builtin --live --judge -o ./output
```

内部执行流程：

```
1. initEvalGateway()                    // 创建评测专用 Gateway
2. loadSuite("builtin")                 // 加载任务套件
3. gateway.NewEvalRunner()              // 创建 CognitiveAgentRunner
4. NewLLMJudge(provider)               // 创建 LLM Judge
5. NewFailureClassifier(provider, 5min) // 创建失败分类器
6. RunSuiteWithOptions(tasks, {Judge})  // 执行套件 + 评分
7. Diagnose(suite, {Classifier, tasks}) // 诊断分析
8. report.FormatMarkdown() → stdout    // 输出报告
9. GenerateRadarHTML(dimReport) → file // 生成雷达图 HTML
```

输出文件：
- `{output}/weakness_report.md` — Markdown 格式弱点报告
- `{output}/radar.html` — 维度雷达图 HTML（SVG 渲染）
- `{output}/suite_results.json` — 完整 JSON 结果
