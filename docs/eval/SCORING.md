# 评分流水线（Scoring）

> 源文件：`internal/eval/verifier.go`、`internal/eval/judge.go`、`internal/eval/dimension.go`、`internal/eval/harness.go`（`ComputeFinalScore`）

---

## 目录

1. [模块职责](#1-模块职责)
2. [三层评分架构](#2-三层评分架构)
3. [VerifyReference — 规则验证](#3-verifyreference--规则验证)
4. [LLMJudge — LLM 评分](#4-llmjudge--llm-评分)
5. [ComputeFinalScore — 综合评分](#5-computefinalscore--综合评分)
6. [VerifyMethod 决策树](#6-verifymethod-决策树)
7. [Dimension 聚合分析](#7-dimension-聚合分析)
8. [评分在 RunSuiteWithOptions 中的调用时序](#8-评分在-runsuitewwithoptions-中的调用时序)

---

## 1. 模块职责

评分层将 Agent 的原始输出转化为可量化的分数，支持三种机制的组合：

| 机制 | 适用场景 | 依赖 |
|------|----------|------|
| 规则验证（`VerifyReference`） | 有明确正确答案的任务（代码、命令输出等） | 无（纯字符串匹配 + 文件系统检查） |
| LLM Judge（`LLMJudge`） | 开放式任务（代码质量、解释、规划等） | LLM Provider |
| Assertion 通过率 | 认知循环自评（OBSERVE 阶段的断言检查） | CognitiveAgent |

---

## 2. 三层评分架构

```
Agent 输出（agentOutput: string）
│
├── ① VerifyReference(task, output)      → VerifyResult{Score: 0.0-1.0}
│       └── 规则检查：字符串包含、文件状态、退出码
│
├── ② LLMJudge.Judge(ctx, task, output)  → JudgeResult{Overall: 0.0-1.0}
│       └── LLM 按 Rubric 多维度评分，加权求和
│
└── ③ AssertionPassRate                  → float64 (0.0-1.0)
        └── OBSERVE 阶段 Agent 自评断言通过率

            ↓ ComputeFinalScore(VerifyMethod, ①, ②, ③)

        FinalScore: float64 (0.0-1.0)
```

---

## 3. VerifyReference — 规则验证

### 3.1 函数签名

```go
func VerifyReference(task TaskCase, agentOutput string) *VerifyResult
```

**无副作用**，纯函数，不调用任何外部服务。

### 3.2 检查类型

每项检查生成一个 `CheckResult`，最终分 = 通过数 / 总检查数：

| 检查项 | 配置字段 | 说明 |
|--------|----------|------|
| 精确包含 | `Reference.Answer` | `strings.Contains(output, answer)` |
| 必须包含 | `Reference.MustContain[]` | 每个词独立检查 |
| 不得包含 | `Reference.MustNotContain[]` | 每个词独立检查（取反） |
| 文件存在 | `FileCheck.MustExist` | `os.Stat(path)` |
| 文件内容包含 | `FileCheck.Contains` | 读文件 + `strings.Contains` |
| 文件内容排除 | `FileCheck.NotContains` | 读文件 + 取反检查 |
| 退出码 | `Reference.ExitCode` | 解析 Agent 输出中的 JSON `exit_code` 字段 |

### 3.3 退出码检查的特殊处理

退出码检查假设 Agent 的输出是 bash 工具返回的 JSON 格式：

```go
var parsed struct {
    ExitCode *int `json:"exit_code"`
}
json.Unmarshal([]byte(agentOutput), &parsed)
// 对比 *parsed.ExitCode == expected
```

若 Agent 输出无法解析为 JSON，该检查记为失败。

### 3.4 VerifyResult

```go
type VerifyResult struct {
    Passed bool           // 所有检查通过才为 true
    Checks []CheckResult  // 详细检查列表
    Score  float64        // 通过数 / 总检查数（0.0-1.0）
}

type CheckResult struct {
    Name   string  // 检查名称（如 "must_contain:error"）
    Passed bool
    Detail string  // 调试信息
}
```

---

## 4. LLMJudge — LLM 评分

### 4.1 构造

```go
type LLMJudge struct {
    provider agent.Provider  // 任意 LLM Provider（Claude/OpenAI）
}

func NewLLMJudge(provider agent.Provider) *LLMJudge
```

CLI 中由 `--judge` 标志控制：不传 `--judge` 时 `opts.Judge = nil`，跳过 LLM 评分。

### 4.2 Prompt 构造（buildPrompt）

发送给 Judge LLM 的 Prompt 包含以下节：

```
## Task
{task.Goal}

## Reference Answer          ← 仅当 Reference.Answer 非空时包含
{reference answer}

## Agent Output
{agentOutput}

## Tools Used by Agent
[bash, file_read, ...]       ← 来自真实执行元数据（非 Agent 文本）

## Scoring Criteria
- **correctness** (weight 0.6): ...
- **clarity** (weight 0.4): ...

## Instructions
Score each criterion from 0.0 to 1.0. Respond with JSON:
{"scores": {"criterion_name": 0.0-1.0}, "overall": 0.0-1.0,
 "reasoning": "...", "weaknesses": ["..."]}
```

**注意**：工具列表来自 `result.ToolsUsed`（运行时元数据），并在 Prompt 中明确说明"来自执行元数据"，引导 Judge 正确判断工具选择。

### 4.3 Response 解析（parseResponse）

LLM 输出可能包含 markdown 代码块，解析采用三层降级策略：

```
1. 提取 ```json ... ``` 块 → json.Unmarshal
2. 提取 {...} 块 → json.Unmarshal（处理无代码块格式）
3. 激进搜索 "scores" key → 向前找 { 向后找匹配 }（处理格式严重损坏的输出）
4. 全部失败 → 返回 Overall=0.5（fallback）
```

### 4.4 Overall 重算

解析成功后，Judge 用 Rubric 的 `Weight` 字段重新计算 `Overall`（不信任 LLM 的加权计算）：

```go
weighted := 0.0
totalWeight := 0.0
for _, c := range rubric.Criteria {
    if s, ok := result.Scores[c.Name]; ok {
        weighted += s * c.Weight
        totalWeight += c.Weight
    }
}
result.Overall = weighted / totalWeight  // 重算加权平均
```

### 4.5 JudgeResult

```go
type JudgeResult struct {
    Scores     map[string]float64  // 各维度分数
    Overall    float64             // 加权平均总分（0.0-1.0）
    Reasoning  string              // Judge 的评分理由
    Weaknesses []string            // 标识出的弱点
}
```

---

## 5. ComputeFinalScore — 综合评分

```go
func ComputeFinalScore(
    method VerifyMethod,
    vr *VerifyResult,
    jr *JudgeResult,
    assertionPassRate float64,
) float64
```

| VerifyMethod | FinalScore |
|---|---|
| `deterministic` | `vr.Score`（规则验证分，无 vr 则为 0） |
| `llm_judge` | `jr.Overall`（LLM Judge 分，无 jr 则为 0） |
| `hybrid` | `0.5 * vr.Score + 0.5 * jr.Overall`（各半混合） |
| 空（默认） | `assertionPassRate`（OBSERVE 阶段 Assertion 通过率） |

**降级处理**：
- `deterministic` + 无 `vr` → 0.0（不惩罚，视为"无法验证"）
- `hybrid` + 无 `jr` → 退化为 `vr.Score`
- `hybrid` + 无 `vr` → 退化为 `jr.Overall`

---

## 6. VerifyMethod 决策树

如何为任务选择合适的 VerifyMethod：

```
任务有确定性输出？
├─ 是 → 设置 Reference（Answer/MustContain/FileChecks）
│        └─ VerifyMethod = "deterministic"
│
└─ 否 → 任务有 Rubric？
         ├─ 是 → 同时有 Reference？
         │        ├─ 是 → VerifyMethod = "hybrid"（两者各半）
         │        └─ 否 → VerifyMethod = "llm_judge"
         │
         └─ 否 → VerifyMethod 留空
                  └─ FinalScore = AssertionPassRate（CognitiveAgent 自评）
```

**最佳实践**：
- 代码执行类任务 → `deterministic`（检查文件内容、退出码）
- 解释/规划类任务 → `llm_judge`
- 有参考答案 + 质量要求 → `hybrid`
- 能力探测（不关注输出内容）→ 留空

---

## 7. Dimension 聚合分析

`AggregateDimensions(results []EvalResult) *DimensionReport` 用于生成维度级别的分析：

### 7.1 DimensionScore

```go
type DimensionScore struct {
    Dimension   Dimension
    TaskCount   int
    SuccessRate float64   // 通过数 / 总任务数
    AvgScore    float64   // 平均 FinalScore
    AvgReplan   float64   // 平均重规划次数（高 = 任务较难）
    TopFailures []FailureCategory  // Top-3 失败类别（来自 FailureClassifier）
}
```

### 7.2 DimensionReport

```go
type DimensionReport struct {
    Dimensions          []DimensionScore         // 所有维度（按 AvgScore 升序）
    Weakest             []DimensionScore         // Top-3 最弱（AvgScore < 0.7）
    Strongest           []DimensionScore         // Top-3 最强（AvgScore >= 0.8）
    FailureDistribution map[FailureCategory]int  // 全局失败类别分布
}
```

### 7.3 雷达图数据来源

`Diagnose` 调用 `AggregateDimensions` 生成 `DimensionReport`，再将各维度的 `AvgScore` 序列化为雷达图数据点，最终渲染为 HTML SVG。

---

## 8. 评分在 RunSuiteWithOptions 中的调用时序

```
RunSuiteWithOptions
└── for each TaskCase:
    ├── runner.RunTask(ctx, task)       → result (含 AssertionPassRate)
    │
    ├── if task.Reference != nil:
    │   └── VerifyReference(task, result.AgentOutput)  → result.VerifyResult
    │
    ├── if opts.Judge != nil && task.Rubric != nil
    │        && (VerifyLLMJudge || VerifyHybrid):
    │   └── opts.Judge.Judge(ctx, task, output, tools)  → result.JudgeResult
    │
    ├── ComputeFinalScore(method, vr, jr, assertionPassRate)  → result.FinalScore
    │
    └── result.Dimension = DefaultDimension(task.Dimension)
```

**LLM Judge 触发条件**（同时满足）：
1. `opts != nil && opts.Judge != nil`（CLI 传了 `--judge`）
2. `task.Rubric != nil`（任务定义了评判标准）
3. `task.VerifyMethod == VerifyLLMJudge || task.VerifyMethod == VerifyHybrid`

不满足任一条件时跳过 LLM Judge，避免不必要的 API 调用。
