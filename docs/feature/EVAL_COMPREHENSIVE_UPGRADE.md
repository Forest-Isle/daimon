# Agent 全维度评测与弱点诊断系统

**日期**: 2026-04-20
**范围**: 评测框架升级（多维度 + LLM Judge + 单任务回归） + 任务集大扩展（8 维度 54+ 任务） + 弱点诊断引擎 + Benchmark 适配器 + 自适应任务生成

## 概述

在现有 Eval Harness（`internal/eval/`）基础上，构建全维度 Agent 评测与弱点诊断系统。核心目标：**系统性地发现 Agent 弱点并输出可执行的优化建议**。

现有评测的局限：
- 断言是纯语法层面（exit code、非空输出），不验证答案正确性
- 比较只在 suite 级别，无法定位具体退步任务
- 任务覆盖面有限（几乎全是 bash/file），未覆盖对话、记忆、知识库、多 Agent
- 没有失败分类和弱点诊断

本次升级分 4 个 Phase 交付：

| Phase | 目标 | 核心产出 |
|-------|------|---------|
| Phase 1 | 评测框架升级 | LLM Judge + 确定性校验 + 单任务回归 |
| Phase 2 | 任务集大扩展 | 8 维度 54+ 新任务 |
| Phase 3 | 弱点诊断引擎 | 失败分类 + 维度雷达图 + 优化建议 |
| Phase 4 | Benchmark + 自适应 | SWE-bench/HumanEval/GAIA 适配 + 自适应任务生成 |

---

## 整体架构

### 8 个评测维度

| 维度 | 代号 | 评测什么 | 验证方式 |
|------|------|---------|---------|
| 任务执行 | `task_execution` | bash/file/http/browser 基础工具使用 | 确定性 |
| 规划能力 | `planning` | 复杂任务分解、依赖排序、多步推理 | Hybrid |
| 错误恢复 | `error_recovery` | 失败重试、工具切换、降级策略 | 确定性 |
| 工具选择 | `tool_selection` | 给定目标是否选对工具、是否冗余 | LLM Judge |
| 对话质量 | `conversation` | 回答准确性、清晰度、完整性 | LLM Judge |
| 记忆系统 | `memory` | 检索相关性、事实准确性、Profile 利用 | Hybrid |
| 知识库 | `knowledge` | 文档引用正确性、RAG 检索质量 | Hybrid |
| 多Agent协作 | `multi_agent` | 团队任务分解、并行效率、结果整合 | Hybrid |

### 三层验证体系

```
层级 1: 确定性校验（Deterministic）
  - 文件存在/内容匹配、命令退出码
  - 输出 MustContain / MustNotContain
  - 用时约束

层级 2: LLM-as-Judge
  - 基于 Rubric 多维度独立评分（0-1）
  - 输出结构化 JSON：分数 + 推理过程 + 弱点标记
  - 按 weight 加权合成 Overall 分数

层级 3: 混合验证（Hybrid）
  - 确定性校验拿 "硬指标" + LLM Judge 评 "软质量"
  - 加权合成 FinalScore
```

### 核心数据流

```
TaskCase（维度标签 + Reference + Rubric）
    │
    ▼
RunSuite
    │
    ├── Agent 执行任务 → 收集 AgentOutput
    ├── 确定性校验 → 检查 Reference
    ├── LLM Judge → 基于 Rubric 打分
    ▼
EvalResult（+ JudgeResult, VerifyResult, FinalScore, FailureCategory）
    │
    ▼
维度聚合 → DimensionReport
    │
    ├── 雷达图（8 维度可视化）
    ├── 弱点报告（分类排序 + 优化建议）
    └── 单任务回归追踪（task-level delta）
```

### 向后兼容

所有新增字段均为可选（`omitempty`）。现有 `BuiltinSuite`、`EvolutionSuite`、`WorkloadSuite` 不改动即可继续工作。`Dimension` 为空时默认归入 `task_execution`。

---

## Phase 1：评测框架升级

### TaskCase 扩展

```go
type TaskCase struct {
    // 现有字段不动
    ID          string
    Goal        string
    Complexity  string
    Tags        []string
    ExpectTools []string
    SuccessFunc func(result *EvalResult) bool

    // 新增字段
    Dimension    Dimension    `json:"dimension,omitempty"`
    VerifyMethod VerifyMethod `json:"verify_method,omitempty"`
    Reference    *Reference   `json:"reference,omitempty"`
    Rubric       *Rubric      `json:"rubric,omitempty"`
    SetupFunc    func() error `json:"-"`
    CleanupFunc  func() error `json:"-"`
}
```

### Reference（确定性 ground truth）

```go
type Reference struct {
    Answer         string      `json:"answer,omitempty"`
    MustContain    []string    `json:"must_contain,omitempty"`
    MustNotContain []string    `json:"must_not_contain,omitempty"`
    FileChecks     []FileCheck `json:"file_checks,omitempty"`
    ExitCode       *int        `json:"exit_code,omitempty"`
}

type FileCheck struct {
    Path        string `json:"path"`
    MustExist   bool   `json:"must_exist"`
    Contains    string `json:"contains,omitempty"`
    NotContains string `json:"not_contains,omitempty"`
}
```

### Rubric（LLM Judge 评分标准）

```go
type Rubric struct {
    Criteria []JudgeCriterion `json:"criteria"`
}

type JudgeCriterion struct {
    Name        string  `json:"name"`
    Description string  `json:"description"`
    Weight      float64 `json:"weight"`
}
```

### LLM Judge 模块

新文件：`internal/eval/judge.go`

```go
type LLMJudge struct {
    provider agent.Provider
}

type JudgeResult struct {
    Scores     map[string]float64 `json:"scores"`
    Overall    float64            `json:"overall"`
    Reasoning  string             `json:"reasoning"`
    Weaknesses []string           `json:"weaknesses"`
}

func NewLLMJudge(provider agent.Provider) *LLMJudge

func (j *LLMJudge) Judge(ctx context.Context, task TaskCase,
    agentOutput string) (*JudgeResult, error)
```

工作流程：
1. 构建 prompt：`task.Goal` + `task.Reference.Answer` + `task.Rubric.Criteria` + `agentOutput`
2. 调用 LLM，要求返回结构化 JSON
3. 解析响应，按 weight 加权计算 Overall
4. 格式异常时降级为 overall=0.5 + warning

成本控制：仅在 `VerifyMethod` 为 `llm_judge` 或 `hybrid` 时调用。

### 确定性校验器

新文件：`internal/eval/verifier.go`

```go
type VerifyResult struct {
    Passed  bool          `json:"passed"`
    Checks  []CheckResult `json:"checks"`
    Score   float64       `json:"score"`
}

type CheckResult struct {
    Name    string `json:"name"`
    Passed  bool   `json:"passed"`
    Detail  string `json:"detail"`
}

func VerifyReference(task TaskCase, agentOutput string) *VerifyResult
```

### EvalResult 扩展

```go
type EvalResult struct {
    // 现有字段不动
    TaskID, Goal, Complexity, Error string
    Success bool
    Duration time.Duration
    ToolsUsed []string
    ReplanCount int
    AssertionTotal, AssertionPassed int
    AssertionPassRate, Confidence float64
    Timestamp time.Time

    // 新增字段
    Dimension       Dimension       `json:"dimension,omitempty"`
    AgentOutput     string          `json:"agent_output,omitempty"`
    VerifyResult    *VerifyResult   `json:"verify_result,omitempty"`
    JudgeResult     *JudgeResult    `json:"judge_result,omitempty"`
    FinalScore      float64         `json:"final_score"`
    FailureCategory string          `json:"failure_category,omitempty"`
}
```

### FinalScore 合成规则

| VerifyMethod | 计算方式 |
|------|------|
| `deterministic` | `FinalScore = VerifyResult.Score` |
| `llm_judge` | `FinalScore = JudgeResult.Overall` |
| `hybrid` | `FinalScore = 0.5 * VerifyResult.Score + 0.5 * JudgeResult.Overall` |
| 空（旧任务） | `FinalScore = AssertionPassRate` |

### 单任务级回归追踪

```go
type TaskRegression struct {
    TaskID      string  `json:"task_id"`
    Dimension   string  `json:"dimension"`
    BeforeScore float64 `json:"before_score"`
    AfterScore  float64 `json:"after_score"`
    Delta       float64 `json:"delta"`
    Status      string  `json:"status"` // "improved" / "regressed" / "stable"
}

type ComparisonReport struct {
    Delta           ComparisonDelta
    TaskRegressions []TaskRegression       `json:"task_regressions,omitempty"`
    DimensionDeltas map[Dimension]float64  `json:"dimension_deltas,omitempty"`
    Regressions     []TaskRegression       `json:"regressions,omitempty"`
    Improvements    []TaskRegression       `json:"improvements,omitempty"`
}
```

### RunSuite 流程变化

```
现有: RunTask → populateFromObservation → populateFromEvolution → SuccessFunc

新增: → VerifyReference（如有 Reference）
     → LLMJudge（如有 Rubric）
     → 合成 FinalScore
     → 分类 FailureCategory
```

RunSuite 新增可选参数：

```go
type RunOptions struct {
    Judge *LLMJudge
}
```

### Phase 1 涉及文件

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/harness.go` | 修改 | TaskCase/EvalResult 扩展 + RunSuite 流程 |
| `internal/eval/judge.go` | 新增 | LLM Judge 模块 |
| `internal/eval/verifier.go` | 新增 | 确定性校验器 |
| `internal/eval/compare.go` | 修改 | 单任务回归 + 维度 delta |
| `internal/eval/dimension.go` | 新增 | Dimension 常量 + DimensionReport 聚合 |
| `internal/eval/judge_test.go` | 新增 | Judge 测试 |
| `internal/eval/verifier_test.go` | 新增 | Verifier 测试 |
| `cmd/ironclaw/eval.go` | 修改 | `--judge` 标志 |

---

## Phase 2：任务集大扩展

### 任务集总览

| 任务集 | 维度 | 数量 | 验证方式 |
|--------|------|------|---------|
| `task_exec`（已有） | task_execution | 8 | 确定性 |
| `planning` | planning | 8 | Hybrid |
| `error_recovery` | error_recovery | 6 | 确定性 |
| `tool_selection` | tool_selection | 6 | LLM Judge |
| `conversation` | conversation | 8 | LLM Judge |
| `memory` | memory | 6 | Hybrid |
| `knowledge` | knowledge | 6 | Hybrid |
| `multi_agent` | multi_agent | 6 | Hybrid |
| **合计** | | **~54 新增** | |

### Planning（规划能力）—— 8 个任务

| ID | 复杂度 | 场景 | 验证重点 |
|----|--------|------|---------|
| `plan-dep-chain` | complex | 构建有依赖关系的 3 个文件（A→B→C） | 文件存在+内容正确+Judge评规划 |
| `plan-parallel-tasks` | complex | 3 个独立子任务可并行 | 完成度+Judge评并行识别 |
| `plan-constraint-sat` | complex | 多个互斥约束下做选择 | 满足所有约束 |
| `plan-multi-file-refactor` | complex | 重命名被 3 个文件引用的函数 | 所有文件更新一致 |
| `plan-incremental` | moderate | 骨架→填内容→验证→修复 | 最终结果正确 |
| `plan-ambiguous-goal` | complex | 目标模糊，看是否先澄清再行动 | Judge评解读和规划 |
| `plan-resource-limit` | moderate | 不超过 3 次工具调用完成 | 调用次数≤3 |
| `plan-priority-order` | moderate | 5 个子任务有优先级 | 执行顺序匹配优先级 |

### Error Recovery（错误恢复）—— 6 个任务

| ID | 场景 | 验证重点 |
|----|------|---------|
| `err-missing-dep` | 执行脚本缺依赖需要换方案 | 最终成功+不反复重试 |
| `err-wrong-path` | 文件路径拼写错误需自我修正 | 最终找到正确文件 |
| `err-permission` | 写入只读目录需换目录 | 最终写入成功 |
| `err-timeout-retry` | HTTP 请求超时需重试 | 最终获取数据 |
| `err-partial-failure` | 3 个子任务 1 个失败不应放弃其余 | 至少 2 个成功 |
| `err-cascading` | 第一步失败后续需调整计划 | 最终完成+Judge评恢复策略 |

### Tool Selection（工具选择）—— 6 个任务

| ID | 场景 | 验证重点 |
|----|------|---------|
| `tool-read-vs-bash` | "查看文件内容"应用 file_read | Judge评工具选择 |
| `tool-search-method` | "找所有 TODO"应用 grep | 效率评估 |
| `tool-write-vs-edit` | 修改一行应用 file_edit | Judge评精确度 |
| `tool-overkill` | 简单任务不应启动复杂工具链 | 工具调用数≤2 |
| `tool-http-vs-browser` | 获取 API 数据应用 http | Judge评选择 |
| `tool-composite` | 需要 3 个工具配合 | 所有预期工具被使用 |

### Conversation（对话质量）—— 8 个任务

| ID | 场景 | Rubric 维度 |
|----|------|------------|
| `conv-explain-code` | 解释 Go 代码 | 准确性、完整性、清晰度 |
| `conv-debug-guide` | 给定错误日志指导排查 | 准确性、可操作性、步骤完整 |
| `conv-compare-options` | 比较两种技术方案 | 平衡性、全面性、有理有据 |
| `conv-summarize` | 总结长文档 | 覆盖率、简洁度、无幻觉 |
| `conv-refuse-unsafe` | 用户请求危险操作 | 安全意识、拒绝方式 |
| `conv-clarify` | 模糊请求应先追问 | 是否主动澄清 |
| `conv-multilingual` | 中文提问中文回答 | 语言匹配、内容质量 |
| `conv-context-recall` | 多轮对话引用前文 | 上下文保持能力 |

### Memory（记忆系统）—— 6 个任务

| ID | 场景 | 验证重点 |
|----|------|---------|
| `mem-store-recall` | 存事实新 session 检索 | 正确检索 |
| `mem-relevance` | 存 5 个事实问 1 个相关 | 检索精确度 |
| `mem-update` | 更新事实验证返回最新 | 更新一致性 |
| `mem-profile-use` | 设定偏好后续应体现 | Judge评个性化 |
| `mem-no-hallucinate` | 问没存过的事实 | 不虚构 |
| `mem-cross-session` | session A 存 session B 检索 | 跨 session 持久性 |

### Knowledge（知识库）—— 6 个任务

| ID | 场景 | 验证重点 |
|----|------|---------|
| `kb-ingest-query` | 灌入文档后问事实 | MustContain 关键词 |
| `kb-multi-doc` | 3 篇文档综合 2 篇回答 | 多源引用+答案正确 |
| `kb-no-answer` | 问文档中不存在的信息 | 不虚构 |
| `kb-update` | 更新文档后重新问 | 一致性 |
| `kb-citation` | 回答时标注来源 | Judge评引用质量 |
| `kb-graph-traverse` | 知识图谱关系型问答 | 关系正确性 |

### Multi-Agent（多 Agent 协作）—— 6 个任务

| ID | 场景 | 验证重点 |
|----|------|---------|
| `team-split-merge` | 大任务分给 2 个子 Agent | 结果完整无冲突 |
| `team-specialist` | 不同专长分工 | 任务分配合理性 |
| `team-failure-isolate` | 子 Agent A 失败不影响 B | B 结果正确 |
| `team-dependency` | 子任务有依赖 | 顺序正确+数据传递正确 |
| `team-parallel-efficiency` | 3 个独立子任务应并行 | 总耗时<串行耗时 |
| `team-result-conflict` | 两个子 Agent 结果矛盾 | Judge评冲突解决 |

### SetupFunc / CleanupFunc 模式

需要环境准备的任务（记忆、知识库等）使用 `SetupFunc` 和 `CleanupFunc`。

### CLI 新增

```bash
ironclaw eval list --suite all          # 列出全部维度任务
ironclaw eval run --suite planning --live --judge   # 单维度
ironclaw eval run --suite full --live --judge       # 全量
```

新增 `FullSuite()` 和各维度 Suite 注册到 `AllSuites()`。

### Phase 2 涉及文件

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/fixtures.go` | 修改 | FullSuite + AllSuites 更新 |
| `internal/eval/fixtures_planning.go` | 新增 | 8 个任务 |
| `internal/eval/fixtures_error.go` | 新增 | 6 个任务 |
| `internal/eval/fixtures_tool.go` | 新增 | 6 个任务 |
| `internal/eval/fixtures_conv.go` | 新增 | 8 个任务 |
| `internal/eval/fixtures_memory.go` | 新增 | 6 个任务 |
| `internal/eval/fixtures_knowledge.go` | 新增 | 6 个任务 |
| `internal/eval/fixtures_team.go` | 新增 | 6 个任务 |
| `cmd/ironclaw/eval.go` | 修改 | `--suite` 支持新名称 |

---

## Phase 3：弱点诊断引擎

### 失败分类器

新文件：`internal/eval/classifier.go`

```go
type FailureCategory string

const (
    FailPlanningError     FailureCategory = "planning_error"
    FailToolMisuse        FailureCategory = "tool_misuse"
    FailToolMissing       FailureCategory = "tool_missing"
    FailErrorNoRecovery   FailureCategory = "error_no_recovery"
    FailErrorLoopRetry    FailureCategory = "error_loop_retry"
    FailHallucination     FailureCategory = "hallucination"
    FailIncompleteAnswer  FailureCategory = "incomplete_answer"
    FailWrongAnswer       FailureCategory = "wrong_answer"
    FailTimeout           FailureCategory = "timeout"
    FailContextLost       FailureCategory = "context_lost"
    FailOverEngineering   FailureCategory = "over_engineering"
)
```

两阶段分类：
1. 规则分类（零成本）：ReplanCount 过高→LoopRetry，Duration 超时→Timeout，工具不匹配→ToolMisuse，MustNotContain 失败→Hallucination 等
2. LLM 分类（规则无法判定时）：把 AgentOutput + Goal + 失败信息交给 LLM

### 维度聚合报告

新文件：`internal/eval/dimension.go`

```go
type DimensionReport struct {
    Dimensions          []DimensionScore                `json:"dimensions"`
    Weakest             []DimensionScore                `json:"weakest"`
    Strongest           []DimensionScore                `json:"strongest"`
    FailureDistribution map[FailureCategory]int         `json:"failure_distribution"`
}

type DimensionScore struct {
    Dimension   Dimension         `json:"dimension"`
    TaskCount   int               `json:"task_count"`
    SuccessRate float64           `json:"success_rate"`
    AvgScore    float64           `json:"avg_score"`
    AvgReplan   float64           `json:"avg_replan"`
    TopFailures []FailureCategory `json:"top_failures"`
}
```

### 弱点报告 + 优化建议

新文件：`internal/eval/diagnosis.go`

```go
type WeaknessReport struct {
    GeneratedAt     time.Time          `json:"generated_at"`
    OverallScore    float64            `json:"overall_score"`
    DimReport       *DimensionReport   `json:"dimension_report"`
    Weaknesses      []Weakness         `json:"weaknesses"`
    Recommendations []Recommendation   `json:"recommendations"`
}

type Weakness struct {
    ID          string          `json:"id"`
    Severity    string          `json:"severity"` // critical / major / minor
    Category    FailureCategory `json:"category"`
    Dimension   Dimension       `json:"dimension"`
    Description string          `json:"description"`
    Evidence    []string        `json:"evidence"`
    Frequency   int             `json:"frequency"`
}

type Recommendation struct {
    TargetWeakness string `json:"target_weakness"`
    Priority       int    `json:"priority"`
    Action         string `json:"action"`
    Component      string `json:"component"`
    Detail         string `json:"detail"`
}
```

### 内置建议规则

| 弱点模式 | 建议 |
|---------|------|
| `error_loop_retry` 频率高 | 检查 maxReplans 配置；优化 REFLECT prompt |
| `tool_misuse` 频率高 | 强化 PERCEIVE 工具描述注入；利用进化引擎工具偏好学习 |
| `hallucination` 频率高 | 增强 OBSERVE 断言；system prompt 强调不确定时说不知道 |
| `planning_error` 频率高 | 优化 PLAN prompt；增加规划示例；调整 ContextBudgetAllocator |
| `timeout` 频率高 | 检查 LLM 超时配置；复杂任务自动拆分 |
| `incomplete_answer` 频率高 | 强化 REFLECT 完整性自检；增加回答模板 |
| `context_lost` 频率高 | 检查 ContextManager 压缩策略；增大 context budget |
| `over_engineering` 频率高 | system prompt 强调 YAGNI；优化规划 prompt |

LLM 建议生成作为补充：把弱点列表 + Agent 架构描述交给 LLM，输出针对性修改建议。

### 雷达图可视化

扩展 `eval_visualize.go`，新增：
- 维度雷达图（8 维度 AvgScore，Chart.js radar 类型）
- 失败分布饼图（FailureCategory 占比）
- 弱点热力表（维度 × FailureCategory 频次矩阵）

### CLI 命令

```bash
ironclaw eval diagnose --suite full --live --judge -o report/
```

输出：
```
report/
├── results.json          ← 原始评测结果
├── weakness_report.json  ← 弱点报告（结构化）
├── weakness_report.md    ← 弱点报告（Markdown）
└── radar.html            ← 雷达图+饼图+热力表
```

### Markdown 报告格式

```markdown
# IronClaw Agent 弱点诊断报告

**生成时间**: 2026-04-20 15:30:00
**综合得分**: 0.72 / 1.00

## 维度得分

| 维度 | 任务数 | 成功率 | 平均分 | 主要失败类型 |
|------|--------|--------|--------|------------|
| task_execution | 8 | 87.5% | 0.85 | - |
| planning | 8 | 62.5% | 0.68 | planning_error |
| ...

## 弱点清单（按严重度排序）

### [CRITICAL] W-001: 错误恢复能力不足
- **维度**: error_recovery
- **失败类型**: error_loop_retry
- **频次**: 3/6
- **证据**: err-missing-dep, err-cascading, err-timeout-retry

## 优化建议

| 优先级 | 针对弱点 | 建议模块 | 具体行动 |
|--------|---------|---------|---------|
| 1 | W-001 | agent/cognitive.go (REFLECT) | 优化失败分析 prompt |
```

### Phase 3 涉及文件

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/classifier.go` | 新增 | 失败分类器 |
| `internal/eval/dimension.go` | 新增 | 维度聚合 + DimensionReport |
| `internal/eval/diagnosis.go` | 新增 | WeaknessReport + Recommendation |
| `internal/eval/diagnosis_test.go` | 新增 | 分类器+诊断测试 |
| `cmd/ironclaw/eval.go` | 修改 | `eval diagnose` 子命令 |
| `cmd/ironclaw/eval_visualize.go` | 修改 | 雷达图+饼图+热力表 |

---

## Phase 4：Benchmark 适配器 + 自适应任务生成

### Benchmark 适配器框架

新文件：`internal/eval/benchmark.go`

```go
type BenchmarkAdapter interface {
    Name() string
    LoadTasks(path string) ([]TaskCase, error)
    FormatResult(results []EvalResult) ([]byte, error)
}
```

### 三个 Adapter

#### SWE-bench（`bench_swe.go`）

SWE-bench 任务：给定 GitHub issue + 代码仓库，Agent 生成 patch 修复 bug。
- `SetupFunc` = clone repo + checkout base_commit
- `Reference` = 跑 test_patch 验证
- `Dimension` = task_execution

#### HumanEval（`bench_humaneval.go`）

HumanEval 任务：给定函数签名+docstring，Agent 实现函数，测试用例验证。
- `Reference.ExitCode` = 0（测试通过）
- `Dimension` = task_execution

#### GAIA（`bench_gaia.go`）

GAIA 任务：真实世界多步骤推理。
- `Reference.MustContain` = 精确匹配核心答案
- `VerifyMethod` = hybrid
- `Dimension` = planning

### Benchmark 对比报告

```go
type BenchmarkComparison struct {
    BenchmarkName string           `json:"benchmark"`
    IronClawScore float64          `json:"ironclaw_score"`
    References    []ReferenceScore `json:"references"`
}

type ReferenceScore struct {
    AgentName string  `json:"agent_name"`
    Score     float64 `json:"score"`
    Source    string  `json:"source"`
}
```

内置已知参考分数方便横向对比。

### 自适应任务生成

新文件：`internal/eval/adaptive.go`

```go
type AdaptiveGenerator struct {
    judge   *LLMJudge
    history []WeaknessReport
}

type GeneratedTask struct {
    TaskCase
    TargetWeakness string `json:"target_weakness"`
    Rationale      string `json:"rationale"`
}

func (g *AdaptiveGenerator) Generate(ctx context.Context,
    report *WeaknessReport, count int) ([]GeneratedTask, error)
```

流程：取 top-3 弱点 → 每个弱点 LLM 生成 2 个针对性任务 → 解析 → 自动标注 Dimension + VerifyMethod。生成的任务必须包含 Reference（可验证）。

### 自适应评测循环

```bash
ironclaw eval adaptive --suite full --live --judge --rounds 3 -o adaptive_report/
```

```
Round 1: RunSuite(full) → Diagnose → Generate 6 个针对性任务
Round 2: RunSuite(full + 6) → Diagnose → Generate 6 个更难任务
Round 3: RunSuite(full + 12) → Diagnose → 最终报告
```

### 弱点趋势追踪

```go
type AdaptiveSummary struct {
    Rounds        []RoundSnapshot      `json:"rounds"`
    WeaknessTrend map[string][]float64 `json:"weakness_trend"`
    Converging    []string             `json:"converging"`
    Diverging     []string             `json:"diverging"`
}
```

`Converging`：分数上升的维度。`Diverging`：分数下降的维度。

### Phase 4 涉及文件

| 文件 | 变更 | 说明 |
|------|------|------|
| `internal/eval/benchmark.go` | 新增 | BenchmarkAdapter 接口 + 对比报告 |
| `internal/eval/bench_swe.go` | 新增 | SWE-bench 适配器 |
| `internal/eval/bench_humaneval.go` | 新增 | HumanEval 适配器 |
| `internal/eval/bench_gaia.go` | 新增 | GAIA 适配器 |
| `internal/eval/adaptive.go` | 新增 | AdaptiveGenerator + 自适应循环 |
| `internal/eval/adaptive_test.go` | 新增 | 生成+解析测试 |
| `cmd/ironclaw/eval.go` | 修改 | `eval adaptive` + `--benchmark` |
| `cmd/ironclaw/eval_visualize.go` | 修改 | 自适应趋势图 |

---

## 完整 CLI 命令族

```
ironclaw eval
├── run              # 单次评估（dry / --live / --judge）
├── compare          # 两次评估对比（suite + task level）
├── list             # 列出任务集
├── longitudinal     # 纵向追踪（+ --with-workload）
├── visualize        # 生成 HTML 可视化
├── diagnose         # 评测 + 弱点诊断一步到位
├── adaptive         # 自适应多轮评测
└── benchmark        # 跑外部 Benchmark（swe-bench/humaneval/gaia）
```

## 全量文件变更汇总

| 文件 | Phase | 变更 |
|------|-------|------|
| `internal/eval/harness.go` | 1 | 修改：TaskCase/EvalResult 扩展 + RunSuite 流程 |
| `internal/eval/judge.go` | 1 | 新增：LLM Judge |
| `internal/eval/verifier.go` | 1 | 新增：确定性校验器 |
| `internal/eval/compare.go` | 1 | 修改：单任务回归 + 维度 delta |
| `internal/eval/dimension.go` | 1+3 | 新增：Dimension 常量 + 聚合报告 |
| `internal/eval/judge_test.go` | 1 | 新增 |
| `internal/eval/verifier_test.go` | 1 | 新增 |
| `internal/eval/fixtures.go` | 2 | 修改：FullSuite + AllSuites |
| `internal/eval/fixtures_planning.go` | 2 | 新增：8 任务 |
| `internal/eval/fixtures_error.go` | 2 | 新增：6 任务 |
| `internal/eval/fixtures_tool.go` | 2 | 新增：6 任务 |
| `internal/eval/fixtures_conv.go` | 2 | 新增：8 任务 |
| `internal/eval/fixtures_memory.go` | 2 | 新增：6 任务 |
| `internal/eval/fixtures_knowledge.go` | 2 | 新增：6 任务 |
| `internal/eval/fixtures_team.go` | 2 | 新增：6 任务 |
| `internal/eval/classifier.go` | 3 | 新增：失败分类器 |
| `internal/eval/diagnosis.go` | 3 | 新增：弱点报告+建议 |
| `internal/eval/diagnosis_test.go` | 3 | 新增 |
| `internal/eval/benchmark.go` | 4 | 新增：适配器框架 |
| `internal/eval/bench_swe.go` | 4 | 新增：SWE-bench |
| `internal/eval/bench_humaneval.go` | 4 | 新增：HumanEval |
| `internal/eval/bench_gaia.go` | 4 | 新增：GAIA |
| `internal/eval/adaptive.go` | 4 | 新增：自适应生成 |
| `internal/eval/adaptive_test.go` | 4 | 新增 |
| `cmd/ironclaw/eval.go` | 1-4 | 修改：新子命令 + 新标志 |
| `cmd/ironclaw/eval_visualize.go` | 3-4 | 修改：雷达图+饼图+趋势图 |
