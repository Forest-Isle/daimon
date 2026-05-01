# Sub-agent Summary-Only Return

**日期**: 2026-05-01  
**范围**: `internal/agent/subagent_result.go` + `internal/agent/subagent.go` + `internal/agent/subagent_result_test.go`

## 概述

在多层嵌套的 agent 场景中，子 agent 的完整对话历史如果直接注入父 agent 的 context，会导致 context 快速膨胀。Claude Code 将这种保护机制称为 "summary-only return"：子 agent 只向父 context 返回摘要，完整历史保留在子 agent 自己的 session 里。

本次改动在 IronClaw 的 `SubAgentManager` 上实现了这一机制。

## 问题背景

`SubAgentManager.Spawn()` 已经通过 `extractStructuredResult()` 提取 `<result>` XML 块，`AgentTool.Execute()` 也只把 `SubAgentResult.Summary` 注入父 context，而不是完整输出。但存在两个遗漏：

1. **LLM fallback 路径没有长度约束**：当 XML 提取失败时，`summarizeWithLLM` 的 prompt 没有明确要求摘要长度，LLM 可能返回很长的文本
2. **Summary 字段没有硬截断**：即使 XML 提取成功，`Summary` 也可能超长（子 agent 在 `<result>` 块里写了大量内容）
3. **没有明确的 SummaryOnly 标记**：调用方无法区分"这是摘要"和"这是完整输出"

## 改动内容

### 1. `SummaryOnly` 标记

```go
type SubAgentResult struct {
    AgentName   string         `json:"agent_name"`
    Status      SubAgentStatus `json:"status"`
    Summary     string         `json:"summary"`
    SummaryOnly bool           `json:"summary_only"`  // 新增
    Output      string         `json:"output"`
    // ...
}
```

`SummaryOnly = true` 表示 `Summary` 是经过截断/摘要处理的，不是原始完整输出。LLM fallback 路径和并行失败路径都会设置此标记。

### 2. Summary 硬截断（2000 字）

```go
func truncateSubAgentSummary(summary string, maxRunes int) string
```

`buildResult()` 在写入 `Summary` 前调用此函数，超过 2000 rune 的内容截断并追加 `...[truncated]`。截断在 rune 边界进行，不会破坏 Unicode 字符。

### 3. LLM Fallback Prompt 收紧

```diff
- "Summarize this agent output into JSON with fields: status, summary (1 paragraph), artifacts..."
+ "Summarize this agent output into JSON with fields: status, summary (1 paragraph, no more than 500 characters), artifacts..."
```

明确要求 LLM 返回不超过 500 字的摘要，从源头控制 fallback 路径的输出长度。

## 数据流

```
SubAgentManager.Spawn()
  └── runtime.HandleMessage()  ← 子 agent 完整历史写入独立 session（DB 保留）
        └── buildResult()
              ├── extractStructuredResult()  ← 提取 <result> XML
              │     ├── 成功 → truncateSubAgentSummary(summary, 2000)
              │     └── 失败 → summarizeWithLLM()  ← prompt 限 500 字
              └── SubAgentResult{SummaryOnly: true, Summary: "<2000 rune"}

AgentTool.Execute()
  └── formatResultForParent(result)  ← 只注入 Summary，不注入 Output/完整历史
```

子 agent 的完整 session 历史仍然完整保存在 SQLite，可通过 session ID 查询，只是不传给父 runtime。

## 效果

| 场景 | 改动前 | 改动后 |
|------|--------|--------|
| 子 agent 输出 10000 字 | 全部注入父 context | 最多 2000 rune 摘要 |
| XML 提取失败走 LLM fallback | LLM 可能返回任意长度 | 限制在 500 字以内 |
| 调用方判断是否为摘要 | 无法区分 | `SummaryOnly == true` |
| 子 agent 历史可查 | 是 | 是（session DB 完整保留） |

## 文件清单

| 文件 | 改动 |
|------|------|
| `internal/agent/subagent_result.go` | 新增 `SummaryOnly` 字段、`truncateSubAgentSummary`、收紧 LLM fallback prompt |
| `internal/agent/subagent.go` | 并行失败路径设置 `SummaryOnly: true` |
| `internal/agent/subagent_result_test.go` | 补充截断和 SummaryOnly 相关测试 |
