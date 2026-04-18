# 评估框架（Eval Harness）

**日期**: 2026-04-18
**范围**: 新增 `internal/eval/` 包 + `ironclaw eval` CLI 命令族，提供可复现的 Agent 性能评估和前后对比能力

## 概述

新增独立的评估框架，支持以固定任务集量化认知 Agent 的表现。核心目标是让自进化的效果可衡量——用同一批任务在进化前后各跑一遍，对比成功率、断言通过率、重规划次数和耗时的 delta。

## 核心架构

### TaskCase 与 EvalResult

```go
type TaskCase struct {
    ID          string
    Goal        string
    Complexity  string
    Tags        []string
    ExpectTools []string
    SuccessFunc func(result *EvalResult) bool  // 可选的编程式成功判定
}

type EvalResult struct {
    TaskID            string
    Success           bool
    Duration          time.Duration
    ToolsUsed         []string
    ReplanCount       int
    AssertionTotal    int
    AssertionPassed   int
    AssertionPassRate float64
    Confidence        float64
}
```

### AgentRunner 接口

评估框架通过 `AgentRunner` 接口与 Agent 解耦：

```go
type AgentRunner interface {
    RunTask(ctx context.Context, task TaskCase) (*EvalResult, error)
}
```

- **DryRunner**: 内置的模拟运行器，不调用真实 LLM，用于测试框架本身和验证任务定义
- **CognitiveAgentRunner**: 由 Gateway 注入，连接真实的认知 Agent（未来集成）

### RunSuite 执行流

```
TaskCase[] → RunSuite(ctx, runID, tasks, runner)
               │
               ├── 逐个执行 runner.RunTask()
               │   ├── 如有 SuccessFunc → 覆盖 agent 自身判定
               │   └── 记录 EvalResult
               │
               └── SuiteResult{Results, Duration}
                     │
                     ├── Summary()  → SuiteSummary{SuccessRate, AvgAssertionPassRate, ...}
                     └── SaveJSON() → results.json
```

### 对比分析

`Compare(before, after *SuiteResult)` 生成 `ComparisonReport`，计算每个维度的 delta：

| 指标 | 说明 | 方向 |
|------|------|------|
| SuccessRateDelta | 成功率变化 | 正值=改进 |
| AvgAssertPassRateDelta | 断言通过率变化 | 正值=改进 |
| AvgConfidenceDelta | 反思置信度变化 | 正值=改进 |
| AvgReplanCountDelta | 重规划次数变化 | 负值=改进 |
| DurationDelta | 总耗时变化 | 负值=改进 |

报告支持 Markdown 和 JSON 两种输出格式。

## 内置任务集

`BuiltinSuite()` 返回 8 个确定性评估任务：

| 任务 ID | 复杂度 | 涵盖工具 | 说明 |
|---------|--------|---------|------|
| `bash-echo` | simple | bash | 基础命令执行 |
| `bash-multi-step` | moderate | bash | 创建目录、写文件、读取验证 |
| `file-write-read` | moderate | file_write, file_read | 文件写入-读取往返 |
| `bash-error-recovery` | moderate | bash | 错误处理和回退 |
| `bash-pipeline` | moderate | bash | 多步 pipeline 操作 |
| `multi-tool-compose` | complex | file_write, bash | 跨工具组合 |
| `bash-script-gen` | complex | file_write, bash | 代码生成+执行 |
| `file-edit-flow` | complex | file_write, bash | 多步编辑验证流 |

所有任务不依赖网络，可在任意环境重复运行。

## CLI 命令

### `ironclaw eval run`

```bash
ironclaw eval run --suite builtin --output results.json --run-id "baseline"
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--suite` | `builtin` | 任务集名称或 JSON 文件路径 |
| `--output` / `-o` | (空) | 结果输出 JSON 文件 |
| `--run-id` | 自动生成 | 运行标识符 |

### `ironclaw eval compare`

```bash
ironclaw eval compare --before baseline.json --after after.json [--json]
```

### `ironclaw eval list`

```bash
ironclaw eval list   # 列出内置任务集中的所有任务
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/eval/harness.go` | 新增 | RunSuite、TaskCase、EvalResult、SuiteResult、SuiteSummary |
| `internal/eval/compare.go` | 新增 | Compare、ComparisonReport、FormatMarkdown |
| `internal/eval/fixtures.go` | 新增 | BuiltinSuite 内置 8 个评估任务 |
| `internal/eval/dry_runner.go` | 新增 | DryRunner 模拟运行器 |
| `internal/eval/harness_test.go` | 新增 | 5 个测试用例覆盖 RunSuite、Compare、Summary |
| `cmd/ironclaw/eval.go` | 新增 | eval run / compare / list CLI 命令 |
| `cmd/ironclaw/main.go` | 修改 | 注册 `newEvalCmd()` |

## 测试

5 个测试用例：

- `TestRunSuite_Basic` — 正常运行带混合结果的任务集
- `TestRunSuite_Empty` — 空任务集返回错误
- `TestRunSuite_SuccessFunc` — 编程式成功判定覆盖 agent 结果
- `TestCompare` — 两次运行的 delta 计算和 Markdown 输出
- `TestSuiteSummary_ZeroResults` — 空结果集的边界处理
