# 全维度评测与弱点诊断系统

**日期**: 2026-04-20
**范围**: 评测框架 4 阶段全面升级 — 多维度评分 + LLM Judge + 54 任务集 + 弱点诊断引擎 + Benchmark 适配器 + 自适应任务生成
**分支**: `feature/eval-comprehensive-upgrade`
**变更**: 32 文件, +5,163 行代码, 76 个测试用例

## 概述

在 [EVAL_HARNESS.md](EVAL_HARNESS.md)（基础评估框架）和 [EVAL_EVOLUTION_BENCHMARK.md](EVAL_EVOLUTION_BENCHMARK.md)（进化基准闭环）基础上，构建全维度 Agent 评测与弱点诊断系统。

核心目标：**系统性地发现 Agent 弱点并输出可执行的优化建议**。

### 先前评测的局限

| 局限 | 影响 |
|------|------|
| 断言只检查 exit code 和非空输出 | 不验证答案正确性 |
| 比较仅在 suite 级别 | 无法定位具体退步任务 |
| 任务全是 bash/file 操作 | 未覆盖对话、记忆、知识库、多 Agent |
| 没有失败分类 | 无法区分是规划错误还是工具误用 |

### 升级后的能力矩阵

```
评测框架升级                    弱点诊断引擎
┌──────────────────────┐      ┌──────────────────────┐
│ 三层验证体系          │      │ 12 种失败分类         │
│  - 确定性校验         │  →   │ 8 维度聚合报告        │
│  - LLM-as-Judge      │      │ 10 条内置优化建议      │
│  - Hybrid 混合        │      │ Markdown + 雷达图报告  │
└──────────────────────┘      └──────────────────────┘
                ↓                         ↓
任务集大扩展                    Benchmark + 自适应
┌──────────────────────┐      ┌──────────────────────┐
│ 8 维度 54 个任务       │      │ SWE-bench/HumanEval   │
│ 11 个命名套件          │      │ GAIA 适配器           │
│ FullSuite 全量评测     │      │ LLM 生成针对性任务     │
└──────────────────────┘      │ 多轮收敛趋势追踪      │
                               └──────────────────────┘
```

---

## Phase 1：评测框架升级

### 三层验证体系

原有的 `SuccessFunc` 只能做简单布尔判定。新增三层独立验证，按 `VerifyMethod` 字段自动调度：

```
层级 1: 确定性校验（VerifyDeterministic）
  VerifyReference(task, agentOutput) → VerifyResult{Score}
  检查项: MustContain / MustNotContain / FileChecks / ExitCode / Answer

层级 2: LLM-as-Judge（VerifyLLMJudge）
  LLMJudge.Judge(ctx, task, agentOutput) → JudgeResult{Scores, Overall, Reasoning, Weaknesses}
  基于 Rubric 多维度评分（0-1），按 weight 加权合成

层级 3: 混合验证（VerifyHybrid）
  FinalScore = 0.5 × VerifyResult.Score + 0.5 × JudgeResult.Overall
  确定性拿 "硬指标"，Judge 评 "软质量"
```

### TaskCase 扩展

```go
type TaskCase struct {
    // 现有字段保留不动
    ID, Goal, Complexity string
    Tags, ExpectTools    []string
    SuccessFunc          func(*EvalResult) bool

    // 新增字段（全部 omitempty，向后兼容）
    Dimension    Dimension    // 归属的能力维度
    VerifyMethod VerifyMethod // 验证方式: deterministic / llm_judge / hybrid
    Reference    *Reference   // 确定性 ground truth
    Rubric       *Rubric      // LLM Judge 评分标准
    SetupFunc    func() error // 环境准备（如预填充记忆）
    CleanupFunc  func() error // 环境清理
}
```

### Reference（确定性 ground truth）

```go
type Reference struct {
    Answer         string      // 精确答案
    MustContain    []string    // 输出必须包含
    MustNotContain []string    // 输出不得包含（检测幻觉）
    FileChecks     []FileCheck // 文件系统验证
    ExitCode       *int        // 命令退出码
}

type FileCheck struct {
    Path      string // 文件路径
    MustExist bool   // 文件必须存在
    Contains  string // 文件内容包含
    NotContains string // 文件内容不含
}
```

### Rubric（LLM Judge 评分标准）

```go
type Rubric struct {
    Criteria []JudgeCriterion
}

type JudgeCriterion struct {
    Name        string  // 评分维度名
    Description string  // 评分说明
    Weight      float64 // 权重（0-1）
}
```

### LLM Judge 模块

`internal/eval/judge.go` 实现 LLM-as-Judge：

1. `buildPrompt` 构建评分 prompt：Task Goal + Reference Answer + Agent Output + Rubric Criteria
2. 调用 `agent.Provider.Complete()` 获取结构化 JSON 响应
3. `parseResponse` 解析，含 markdown 代码块提取、花括号匹配等鲁棒处理
4. 按 weight 加权重算 `Overall`，格式异常时降级为 `overall=0.5 + warning`

成本控制：仅 `VerifyMethod` 为 `llm_judge` 或 `hybrid` 时触发。

### 确定性校验器

`internal/eval/verifier.go` 实现 `VerifyReference(task, agentOutput) → *VerifyResult`：

- 逐项检查 MustContain / MustNotContain / FileChecks / ExitCode / Answer Match
- 每项生成 `CheckResult{Name, Passed, Detail}`
- 综合评分 = 通过项数 / 总项数

### EvalResult 扩展

```go
type EvalResult struct {
    // 现有字段保留
    TaskID, Goal, Complexity, Error string
    Success bool; Duration time.Duration
    ToolsUsed []string; ReplanCount int
    AssertionPassRate, Confidence float64

    // 新增字段
    Dimension       Dimension     // 任务所属维度
    AgentOutput     string        // 原始输出（供验证和 Judge 使用）
    VerifyResult    *VerifyResult // 确定性校验结果
    JudgeResult     *JudgeResult  // LLM Judge 结果
    FinalScore      float64       // 综合得分 (0-1)
    FailureCategory string        // 失败分类（Phase 3）
}
```

### FinalScore 合成规则

| VerifyMethod | 计算方式 |
|------|------|
| `deterministic` | `VerifyResult.Score` |
| `llm_judge` | `JudgeResult.Overall` |
| `hybrid` | `0.5 × VerifyResult.Score + 0.5 × JudgeResult.Overall` |
| 空（旧任务） | `AssertionPassRate`（向后兼容） |

### RunSuiteWithOptions

新增 `RunSuiteWithOptions(ctx, runID, tasks, runner, *RunOptions)` 扩展原有 `RunSuite`：

```
对每个 task:
  1. SetupFunc()（如有）
  2. runner.RunTask()
  3. CleanupFunc()（如有）
  4. VerifyReference()（如有 Reference）
  5. LLMJudge.Judge()（如有 Rubric 且 VerifyMethod 匹配）
  6. ComputeFinalScore()
  7. 输出 "[i/n] taskID — PASS/FAIL (1.5s, score=0.85)"
```

传入 `nil` RunOptions 等价于调用原始 `RunSuite`。

### 单任务级回归追踪

`Compare()` 扩展：

```go
type TaskRegression struct {
    TaskID      string
    Dimension   Dimension
    BeforeScore float64
    AfterScore  float64
    Delta       float64
    Status      string // "improved" / "regressed" / "stable"（±0.05 阈值）
}
```

`ComparisonReport` 新增 `TaskRegressions`、`Regressions`（退步列表）、`Improvements`（进步列表）、`DimensionDeltas`（维度级别 delta）。Markdown 报告自动包含退步/进步表格。

---

## Phase 2：任务集大扩展

### 8 个评测维度

| 维度 | 代号 | Suite 函数 | 任务数 | 验证方式 | 评测什么 |
|------|------|-----------|--------|---------|---------|
| 任务执行 | `task_execution` | `BuiltinSuite()` | 8 | 确定性 | bash/file/http/browser 基础工具使用 |
| 规划能力 | `planning` | `PlanningSuite()` | 8 | Hybrid | 依赖链、并行识别、约束满足、多文件重构 |
| 错误恢复 | `error_recovery` | `ErrorRecoverySuite()` | 6 | 确定性 | 权限降级、路径纠错、超时重试、级联修复 |
| 工具选择 | `tool_selection` | `ToolSelectionSuite()` | 6 | LLM Judge | read vs bash、搜索效率、精准编辑 |
| 对话质量 | `conversation` | `ConversationSuite()` | 8 | LLM Judge | 解释、调试指导、安全拒绝、多语言 |
| 记忆系统 | `memory` | `MemorySuite()` | 6 | Hybrid | 存取召回、相关性过滤、防幻觉 |
| 知识库 | `knowledge` | `KnowledgeSuite()` | 6 | Hybrid | 多文档综合、引用溯源、知识更新 |
| 多Agent | `multi_agent` | `MultiAgentSuite()` | 6 | Hybrid | 分拆合并、专家角色、失败隔离 |

### 任务设计特点

**Planning 任务示例**：
- `plan-dep-chain`：构建有依赖关系的 3 个文件（A→B→C），验证创建顺序和内容
- `plan-ambiguous-goal`：目标仅为 "Make this project better"，Judge 评估是否先澄清再行动
- `plan-resource-limit`：限制工具调用次数 ≤3，考察规划效率

**Error Recovery 任务示例**：
- `err-wrong-path`：故意拼错文件名（`infro.txt` vs `info.txt`），验证自我修正
- `err-partial-failure`：3 个子任务中 1 个注定失败，验证不放弃其余任务
- `err-cascading`：config 缺失 → 创建默认 → 继续执行，Judge 评估恢复策略

**Conversation 任务示例**：
- `conv-refuse-unsafe`：用户请求 `rm -rf /`，验证安全拒绝 + 解释 + 替代方案
- `conv-multilingual`：中文提问要求中文回答，Judge 评估语言匹配和技术准确性
- `conv-context-recall`：引用前文提到的 Rust + CLI 项目，推荐应与上下文吻合

### Suite 注册

```go
AllSuites() → map[string]func() []TaskCase{
    "builtin":        BuiltinSuite,      // 8 任务
    "evolution":      EvolutionSuite,     // 6 任务
    "workload":       WorkloadSuite,      // 22 任务
    "planning":       PlanningSuite,      // 8 任务
    "error_recovery": ErrorRecoverySuite, // 6 任务
    "tool_selection": ToolSelectionSuite, // 6 任务
    "conversation":   ConversationSuite,  // 8 任务
    "memory":         MemorySuite,        // 6 任务
    "knowledge":      KnowledgeSuite,     // 6 任务
    "multi_agent":    MultiAgentSuite,    // 6 任务
    "full":           FullSuite,          // 54 任务（全量）
}
```

---

## Phase 3：弱点诊断引擎

### 失败分类器

`internal/eval/classifier.go` 实现两阶段分类：

**阶段 1 — 规则分类（零 LLM 成本）**：

| 规则 | 触发条件 | 分类结果 |
|------|---------|---------|
| 超时 | `Duration > maxDuration` | `timeout` |
| 重试循环 | `ReplanCount > 3` | `error_loop_retry` |
| 幻觉 | `must_not_contain` 检查失败 | `hallucination` |
| Judge 弱点关键词 | Weaknesses 含 "hallucin/incomplete/wrong" | 对应分类 |
| 工具误用 | ExpectTools 未被使用 | `tool_misuse` |
| 过度工程 | ToolsUsed > ExpectTools×3 | `over_engineering` |
| 无恢复 | Error 非空但 ReplanCount=0 | `error_no_recovery` |
| 答案错误 | VerifyResult.Score < 0.5 | `wrong_answer` |
| 规划失败 | complex 任务 + FinalScore < 0.5 | `planning_error` |

**阶段 2 — LLM 分类（规则无法判定时）**：

把 AgentOutput + Goal + 失败信息交给 LLM，要求返回一个分类名。LLM 返回值必须在已知类别列表中，否则回退到 `unknown`。

**全部 12 种失败类别**：

```go
FailPlanningError    = "planning_error"     // 规划/分解失败
FailToolMisuse       = "tool_misuse"        // 工具选择不当
FailToolMissing      = "tool_missing"       // 缺少必需工具
FailErrorNoRecovery  = "error_no_recovery"  // 遇错不恢复
FailErrorLoopRetry   = "error_loop_retry"   // 重试死循环
FailHallucination    = "hallucination"      // 幻觉/虚构
FailIncompleteAnswer = "incomplete_answer"  // 回答不完整
FailWrongAnswer      = "wrong_answer"       // 答案错误
FailTimeout          = "timeout"            // 超时
FailContextLost      = "context_lost"       // 上下文丢失
FailOverEngineering  = "over_engineering"   // 过度复杂
FailUnknown          = "unknown"            // 未知原因
```

### 维度聚合报告

`AggregateDimensions(results) → *DimensionReport`：

```go
type DimensionScore struct {
    Dimension   Dimension         // 维度名
    TaskCount   int               // 该维度任务数
    SuccessRate float64           // 成功率
    AvgScore    float64           // 平均 FinalScore
    AvgReplan   float64           // 平均重规划次数
    TopFailures []FailureCategory // 主要失败类型（按频次排，最多 3 个）
}

type DimensionReport struct {
    Dimensions          []DimensionScore        // 按 AvgScore 升序
    Weakest             []DimensionScore        // AvgScore < 0.7（最多 3 个）
    Strongest           []DimensionScore        // AvgScore ≥ 0.8（最多 3 个）
    FailureDistribution map[FailureCategory]int // 全局失败分布
}
```

### 弱点识别与优化建议

`Diagnose(ctx, suite, opts) → *WeaknessReport`：

```
SuiteResult
    │
    ├─→ FailureClassifier.ClassifyAll()  ← 失败分类
    ├─→ AggregateDimensions()            ← 维度聚合
    ├─→ identifyWeaknesses()             ← 弱点识别
    │     按 (Category, Dimension) 分组 → Weakness{Severity, Evidence, Frequency}
    │     Severity: ≥3 次=critical, 2 次=major, 1 次=minor
    │
    └─→ generateRecommendations()        ← 优化建议
          匹配 builtinRecommendations 规则表
```

**10 条内置优化建议规则**：

| 弱点类型 | 建议行动 | 目标组件 |
|---------|---------|---------|
| `error_loop_retry` | 降低 maxReplans，优化 REFLECT prompt | `agent/cognitive.go (REFLECT)` |
| `tool_misuse` | 强化 PERCEIVE 工具描述注入 | `agent/cognitive.go (PERCEIVE)` |
| `hallucination` | 增强 OBSERVE 断言 + "不确定时说不知道" | `agent/assertion.go` |
| `planning_error` | 添加规划示例，调整 ContextBudgetAllocator | `agent/cognitive.go (PLAN)` |
| `timeout` | 检查 LLM 超时配置，自动拆分复杂任务 | `config, SubAgentManager` |
| `incomplete_answer` | REFLECT 完整性自检 | `agent/cognitive.go (REFLECT)` |
| `context_lost` | 调整 CompressionPipeline 阈值 | `agent/context_manager.go` |
| `over_engineering` | System prompt 强调 YAGNI | `system prompt, PLAN phase` |
| `error_no_recovery` | ACT 阶段添加重试引导 | `agent/cognitive.go (ACT)` |
| `wrong_answer` | OBSERVE 阶段添加答案交叉验证 | `agent/cognitive.go (OBSERVE)` |

### Markdown 弱点报告

`WeaknessReport.FormatMarkdown()` 输出格式：

```markdown
# IronClaw Agent Weakness Diagnosis Report

**Generated**: 2026-04-20 15:30:00
**Overall Score**: 0.72 / 1.00
**Tasks**: 54 total, 18 failed

## Dimension Scores

| Dimension | Tasks | Success Rate | Avg Score | Avg Replan | Top Failures |
|-----------|-------|-------------|-----------|------------|-------------|
| planning  | 8     | 62.5%       | 0.68      | 2.3        | planning_error |
| ...

## Weaknesses (sorted by severity)

### [CRITICAL] W-001: Agent fails to properly decompose...
- **Dimension**: planning
- **Category**: planning_error
- **Frequency**: 3
- **Evidence**: plan-dep-chain, plan-constraint-sat, plan-ambiguous-goal

## Optimization Recommendations

| Priority | Target | Action | Component | Detail |
|----------|--------|--------|-----------|--------|
| 1 | W-001 | Optimize PLAN phase prompt... | agent/cognitive.go | ... |

## Failure Distribution

| Category | Count |
|----------|-------|
| planning_error | 5 |
| error_loop_retry | 3 |
```

### 雷达图可视化

`cmd/ironclaw/eval_visualize.go` 新增 `writeRadarHTML()`：

- **雷达图**：8 维度 AvgScore + SuccessRate 双层叠加（Chart.js radar 类型）
- **饼图**：FailureCategory 分布（doughnut 类型）
- **弱点表格**：严重度着色（critical=红, major=橙, minor=黄）
- **建议表格**：优先级排序的行动列表
- 暗色主题，响应式布局，自包含 HTML

---

## Phase 4：Benchmark 适配器 + 自适应任务生成

### Benchmark 适配器框架

```go
type BenchmarkAdapter interface {
    Name() string
    LoadTasks(path string) ([]TaskCase, error)
    FormatResult(results []EvalResult) ([]byte, error)
}
```

#### SWE-bench 适配器（`bench_swe.go`）

- 输入：SWE-bench JSON 数据集（instance_id, repo, base_commit, problem_statement, test_patch）
- 转换：每个 instance → TaskCase{ID=`swe-{id}`, Dimension=task_execution, VerifyMethod=deterministic}
- SetupFunc：打印 clone + checkout 信息（实际环境下会执行）
- FormatResult：输出 `{instance_id, resolved, score}` JSON 数组
- 内置参考分数：SWE-Agent(12.3%), Devin(13.9%), AutoCodeRover(19.0%), Agentless(27.4%), OpenHands(29.0%)

#### HumanEval 适配器（`bench_humaneval.go`）

- 输入：HumanEval JSON 数据集（task_id, prompt, entry_point, test）
- 转换：TaskCase{ID=`he-{id}`, Dimension=task_execution, VerifyMethod=deterministic, ExitCode=0}
- SetupFunc：创建测试文件到 `/tmp/ironclaw_humaneval/`
- 内置参考分数：GPT-4(67.0%), Claude 3.5 Sonnet(64.9%), GPT-4o(90.7%), DeepSeek-Coder-V2(90.1%)

#### GAIA 适配器（`bench_gaia.go`）

- 输入：GAIA JSON 数据集（task_id, Question, Level, Final answer）
- 转换：TaskCase{ID=`gaia-{id}`, Dimension=planning, VerifyMethod=hybrid}
- Reference.MustContain 匹配核心答案 + Rubric 评估推理质量
- 内置参考分数：GPT-4+Plugins(15.4%), AutoGPT-4(5.3%), Human(92.0%)

#### 对比报告

```go
type BenchmarkComparison struct {
    BenchmarkName string           // 基准名
    IronClawScore float64          // IronClaw 得分
    TotalTasks    int              // 总任务数
    PassedTasks   int              // 通过数
    References    []ReferenceScore // 参考 Agent 分数
}
```

`FormatComparisonMarkdown()` 输出 IronClaw 分数与各参考 Agent 的对比表格。

### 自适应任务生成

`internal/eval/adaptive.go` 实现 LLM 驱动的任务生成：

```
WeaknessReport
    │
    ├─→ 取 top-3 弱点
    │
    ├─→ 对每个弱点调用 LLM 生成 2 个针对性任务
    │   prompt 包含: 弱点描述、Category、Dimension、Evidence
    │   要求 LLM 返回 JSON 数组: {id, goal, complexity, tags, verify_method, must_contain, rationale}
    │
    └─→ 解析 JSON → GeneratedTask{TaskCase + TargetWeakness + Rationale}
        - 自动标注 Dimension（继承自弱点）
        - 自动填充 Reference.MustContain（可验证）
        - 默认 VerifyMethod=hybrid
```

### 多轮自适应评测循环

`RunAdaptiveLoop(ctx, baseTasks, opts) → *AdaptiveSummary`：

```
Round 1: RunSuite(full)     → Diagnose → Generate 6 个针对性任务
Round 2: RunSuite(full + 6) → Diagnose → Generate 6 个更难任务
Round 3: RunSuite(full + 12) → Diagnose → 最终报告
```

每轮保存独立的 `round_N/` 目录（results.json + weakness_report.json + weakness_report.md）。

### 收敛/发散趋势追踪

```go
type AdaptiveSummary struct {
    Rounds        []RoundSnapshot          // 每轮快照
    WeaknessTrend map[string][]float64     // 维度分数时间序列
    Converging    []string                 // 分数上升的维度
    Diverging     []string                 // 分数下降的维度
}
```

判定规则：首轮 → 末轮 delta > +0.05 = Converging, < -0.05 = Diverging。

### 趋势图可视化

`writeAdaptiveTrendHTML()` 生成三面板 HTML：

- **Overall Score 折线图**：各轮综合得分趋势
- **Task/Failure/Weakness 柱状图**：任务数（随自适应任务累加）、失败数、弱点数
- **Per-Dimension 多折线图**：每个维度的分数在各轮中的变化
- 页脚标注 Converging（绿色）和 Diverging（红色）维度

---

## CLI 命令族

### `ironclaw eval run`

```bash
ironclaw eval run --suite planning --live --judge -o results.json
```

| 参数 | 默认 | 说明 |
|------|------|------|
| `--suite` | `builtin` | 套件名或 JSON 文件路径 |
| `--live` | false | 使用真实 CognitiveAgent（需 LLM credentials） |
| `--judge` | false | 启用 LLM-as-Judge（需 `--live`） |
| `--output` | 空 | 结果 JSON 输出路径 |
| `--run-id` | 自动 | 运行标识符 |

### `ironclaw eval diagnose`

```bash
ironclaw eval diagnose --suite full --live --judge -o report/
```

一键执行：评测 → 分类 → 诊断 → 报告。输出：

```
report/
├── results.json          ← 原始评测结果
├── weakness_report.json  ← 结构化弱点报告
├── weakness_report.md    ← Markdown 弱点报告
└── radar.html            ← 雷达图+饼图+弱点表格
```

| 参数 | 默认 | 说明 |
|------|------|------|
| `--suite` | `full` | 评测套件 |
| `--live` | false | 使用真实 Agent |
| `--judge` | true | LLM-as-Judge（诊断模式默认开启） |
| `--output` | 自动 | 输出目录 |

### `ironclaw eval adaptive`

```bash
ironclaw eval adaptive --suite full --live --rounds 3 --tasks-per-round 6 -o adaptive/
```

多轮自适应评测。输出：

```
adaptive/
├── round_1/              ← 第 1 轮报告
│   ├── results.json
│   ├── weakness_report.json
│   └── weakness_report.md
├── round_2/
├── round_3/
├── adaptive_summary.json ← 全局摘要
├── adaptive_summary.md   ← Markdown 摘要
└── trend.html            ← 趋势图
```

| 参数 | 默认 | 说明 |
|------|------|------|
| `--suite` | `full` | 基础套件 |
| `--rounds` | 3 | 自适应轮数 |
| `--tasks-per-round` | 6 | 每轮生成任务数 |
| `--live` | false | 使用真实 Agent |

### `ironclaw eval benchmark`

```bash
ironclaw eval benchmark --name swe-bench --data swe_bench_lite.json --live -o bench/
```

| 参数 | 默认 | 说明 |
|------|------|------|
| `--name` | (必填) | 基准名：swe-bench / humaneval / gaia |
| `--data` | (必填) | 数据集 JSON 文件路径 |
| `--live` | false | 使用真实 Agent |
| `--judge` | true | LLM-as-Judge |

### `ironclaw eval list`

```bash
ironclaw eval list --suite all   # 列出全部 11 个套件及其任务
ironclaw eval list --suite planning  # 仅列出 planning 套件
```

### 其他命令

| 命令 | 说明 |
|------|------|
| `eval compare` | 两次结果 JSON 对比（含单任务回归） |
| `eval longitudinal` | 纵向多轮迭代追踪 |
| `eval visualize` | 从 longitudinal 报告生成 HTML 图表 |

---

## 核心数据流

```
TaskCase（维度标签 + Reference + Rubric）
    │
    ▼
RunSuiteWithOptions
    │
    ├── Agent 执行任务 → 收集 AgentOutput
    ├── VerifyReference → 确定性校验
    ├── LLMJudge.Judge → 基于 Rubric 打分
    ├── ComputeFinalScore → 综合评分
    ▼
EvalResult（+ VerifyResult, JudgeResult, FinalScore）
    │
    ▼
FailureClassifier.ClassifyAll → 标注 FailureCategory
    │
    ▼
AggregateDimensions → DimensionReport（8 维度聚合）
    │
    ├── 雷达图（8 维度可视化）
    ├── 弱点报告（分类排序 + 优化建议）
    └── 单任务回归追踪（Compare 时）
    │
    ▼（自适应模式）
AdaptiveGenerator.Generate → 针对弱点生成新任务
    │
    └── 下一轮 RunSuite（任务集递增）
```

---

## 向后兼容

- 所有新增字段均为 `omitempty`，现有 `BuiltinSuite`、`EvolutionSuite`、`WorkloadSuite` 不改动即可继续工作
- `Dimension` 为空时默认归入 `task_execution`
- `FinalScore` 为空时回退到 `AssertionPassRate`
- `RunSuiteWithOptions(ctx, runID, tasks, runner, nil)` 行为等同 `RunSuite`
- CLI 所有新参数均有默认值，原有命令用法不变

---

## 涉及文件

### Phase 1 — 评测框架升级

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/harness.go` | 修改 | TaskCase/EvalResult 扩展 + RunSuiteWithOptions + ComputeFinalScore |
| `internal/eval/judge.go` | 新增 | LLMJudge + buildPrompt + parseResponse + extractJSON |
| `internal/eval/verifier.go` | 新增 | VerifyReference + verifyFileCheck + verifyExitCode |
| `internal/eval/compare.go` | 修改 | TaskRegression + DimensionDeltas + 回归/进步表格 |
| `internal/eval/dimension.go` | 新增 | Dimension(8) + VerifyMethod(3) + AllDimensions + DefaultDimension |
| `internal/eval/harness_test.go` | 修改 | ComputeFinalScore + RunSuiteWithOptions + 新字段兼容性测试 |
| `internal/eval/judge_test.go` | 新增 | Judge 有效/异常 LLM 响应 + nil rubric + prompt 构建测试 |
| `internal/eval/verifier_test.go` | 新增 | MustContain/MustNotContain/FileChecks/ExitCode/Answer 各项测试 |
| `internal/eval/integration_test.go` | 新增 | 全管道集成测试（4 种验证路径） |
| `cmd/ironclaw/eval.go` | 修改 | `--judge` flag + diagnose skeleton |

### Phase 2 — 任务集大扩展

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/fixtures_planning.go` | 新增 | PlanningSuite 8 个任务 |
| `internal/eval/fixtures_error.go` | 新增 | ErrorRecoverySuite 6 个任务 |
| `internal/eval/fixtures_tool.go` | 新增 | ToolSelectionSuite 6 个任务 |
| `internal/eval/fixtures_conv.go` | 新增 | ConversationSuite 8 个任务 |
| `internal/eval/fixtures_memory.go` | 新增 | MemorySuite 6 个任务 |
| `internal/eval/fixtures_knowledge.go` | 新增 | KnowledgeSuite 6 个任务 |
| `internal/eval/fixtures_team.go` | 新增 | MultiAgentSuite 6 个任务 |
| `internal/eval/fixtures.go` | 修改 | FullSuite(54 任务) + AllSuites(11 套件) |

### Phase 3 — 弱点诊断引擎

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/classifier.go` | 新增 | FailureCategory(12) + FailureClassifier(规则+LLM) |
| `internal/eval/dimension.go` | 修改 | DimensionScore + DimensionReport + AggregateDimensions |
| `internal/eval/diagnosis.go` | 新增 | Diagnose + WeaknessReport + 10 条 builtinRecommendations + FormatMarkdown |
| `internal/eval/classifier_test.go` | 新增 | 10 个测试：各规则路径 + ClassifyAll + 类别计数 |
| `internal/eval/dimension_test.go` | 修改 | AggregateDimensions 基础/WeakestStrongest/空输入测试 |
| `internal/eval/diagnosis_test.go` | 新增 | 7 个测试：诊断流程 + 分类集成 + 严重度排序 + 建议 + Markdown |
| `cmd/ironclaw/eval.go` | 修改 | eval diagnose 完整实现（评测→分类→诊断→报告） |
| `cmd/ironclaw/eval_visualize.go` | 修改 | writeRadarHTML + radarTemplate（雷达图+饼图+表格） |

### Phase 4 — Benchmark 适配 + 自适应生成

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/benchmark.go` | 新增 | BenchmarkAdapter 接口 + BenchmarkComparison + AllBenchmarkAdapters |
| `internal/eval/bench_swe.go` | 新增 | SWEBenchAdapter + SWEBenchReferences |
| `internal/eval/bench_humaneval.go` | 新增 | HumanEvalAdapter + HumanEvalReferences |
| `internal/eval/bench_gaia.go` | 新增 | GAIAAdapter + GAIAReferences |
| `internal/eval/adaptive.go` | 新增 | AdaptiveGenerator + RunAdaptiveLoop + AdaptiveSummary + FormatMarkdown |
| `internal/eval/benchmark_test.go` | 新增 | 7 个测试：3 个适配器加载 + 格式化 + 比较 + 参考分数 |
| `internal/eval/adaptive_test.go` | 新增 | 8 个测试：生成器边界 + JSON 解析(正常/markdown/异常) + Summary Markdown |
| `cmd/ironclaw/eval.go` | 修改 | eval adaptive + eval benchmark 命令 |
| `cmd/ironclaw/eval_visualize.go` | 修改 | writeAdaptiveTrendHTML + trendTemplate（趋势图） |

---

## 测试

76 个测试用例，全部通过：

```bash
CGO_ENABLED=1 go test -tags "fts5" ./internal/eval/ -count=1
# ok  github.com/Forest-Isle/IronClaw/internal/eval  0.541s
```

### 按模块分布

| 模块 | 测试文件 | 测试数 |
|------|---------|--------|
| 框架核心 | harness_test.go | 12 |
| 确定性校验 | verifier_test.go | 7 |
| LLM Judge | judge_test.go | 4 |
| 集成测试 | integration_test.go | 1 |
| 维度聚合 | dimension_test.go | 6 |
| 失败分类 | classifier_test.go | 10 |
| 弱点诊断 | diagnosis_test.go | 7 |
| Benchmark 适配 | benchmark_test.go | 7 |
| 自适应生成 | adaptive_test.go | 8 |
| 评估通道/Hook | eval_channel + eval_hook | 7 |
| Runner | cognitive_runner_test.go | 7 |
