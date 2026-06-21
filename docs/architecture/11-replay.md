# 11 · replay — 回放评测

> 包路径 `internal/replay` · 录制端 `internal/telemetry` · 蓝图 §4.10 · **免疫系统**

## 职责

一切改动（prompt / 模型 / 规则 / 技能）的裁判。没有它，前面所有改进无法证明有效。这是宪法第 2 条「换脑无感」的判据来源——换模型 → 回放回归数为零。

## 录制

每个情节全量落盘。`episode.Runner` 经 EventBus 发布事件，`telemetry` 子系统订阅并写 `~/.daimon/replays/YYYY-MM-DD.jsonl`：

| 事件 | 内容 |
|---|---|
| `ProviderExchange` | 每轮：model/provider/system prompt/messages JSON/响应文本/tool calls JSON/stop reason/耗时 |
| `ToolRoundTrip` | 工具调用 + 结果 |
| `TurnClosed` | 情节完成（final reply） |
| `EpisodeSalvaged` | 框架兜底标记 |

`telemetry/jsonl.go` 是录制器的导出端。录制是回放保真的前提——[04-episode.md](04-episode.md) 的"上下文逐字节可复现"保证回放喂的是真实历史上下文。

## 核心类型

```go
// internal/replay/replay.go
type Session struct {
    SessionID  string
    Exchanges  []ProviderExchange
    Tools      []ToolRoundTrip
    FinalReply string
    Salvaged   bool
}
type SessionMetrics struct {
    Exchanges, ToolCalls, ToolFailures, AbnormalStops, MaxTokenStops int
    Salvaged bool
}
type Report struct {
    Sessions, Exchanges, ToolCalls, ToolFailures, AbnormalStops, MaxTokenStops int
    Salvaged, SkippedLines int
    PerSession []SessionMetrics
}
func LoadDir(dir string) ([]Session, int, error)  // 读 JSONL 重建 session
func Analyze(sessions []Session, skipped int) Report
```

## 三种回放模式

### 1) 离线健康分析（默认）

`daimon replay` → `LoadDir` → `Analyze` → 健康报告（exchanges / tool failures / salvage 率 / abnormal stops）。读侧重建 session + 健康指标。

### 2) 离线重打分（`--against`）

`rescore.go`：`Rescore(ctx, candidate, sessions, judge)`——把历史情节的录制上下文原样喂给新配置，haiku 档裁判对比新旧 Outcome（**行动 dry-run**，不真执行）。报告质量分 / 成本 / 回归数 + `quality_per_1k_tok`（也是影子周报的基础，[05-mind.md](05-mind.md)）。

**忠实 action 重打分**（increment 1）：关键洞察是候选 `Complete` 只生成不执行 → 读 `resp.ToolCalls` = 候选工具选择决策、零执行 → 绕过 §706。`rescore.go` 三态分类：
- 载荷解码失败 → fail-closed `SkippedAction`（不调 candidate）。
- 空 → 文本路径不变。
- `len>0` → action：`judgeActionExchange` 判 baseline-recorded vs candidate-proposed 工具调用。
- 确定性兜底：baseline 有行动但 candidate 无工具 → 强制 `Indeterminate`（非 regression，高效模型少调非更差）使 Canary 兜底失败不被宽松 judge 放行。

这是**单步决策保真度**（非完整多步轨迹——多步无诚实 dry-run 形态，§706 墙，inc2 跳过）。

### 3) 金丝雀（`--canary`）

`regression.go`：`Canary` 在最近 N 个情节上回放，对比 ground truth 门控自我修改转正。`SelectRegression(sessions, corrections)` 选基线候选 = corrected ∪ salvaged，fail-closed（`!Passed` 退出码非零）。

## 回归集（随纠正自动增长）

```go
// internal/replay/corrections.go
type CorrectionStore struct { ... }  // 用户标记的纠正会话，按录制 session id 键控
```

- `daimon correct <session-id>`：把一个会话标为用户纠正（生产者）。
- 用户纠正过的情节（`journal.kind=correction` 关联）自动入回归集，改动必须全过。
- `regression_corrections` 表（迁移 037）按录制 session id 键控，绕过 episode→session 映射（`EpisodeID=idempotencyKey ≠ SessionID=sess.ID`）。

## 数据

`agent_replays`（迁移 021，扩列）+ `execution_events`（020）+ `regression_corrections`（037）；录制文件 `~/.daimon/replays/*.jsonl`。

## 跨包接缝

- **← telemetry**：订阅 EventBus 写 JSONL。
- **← episode**：录制保真依赖"上下文可复现"。
- **← mind**：`--against` 用 `NewProviderFromConfig` 构造候选 provider（离线工具共享构造点）。
- **CLI**：`daimon replay [--against <config>] [--canary]` / `daimon correct <session-id>`（[21-cli-reference.md](21-cli-reference.md)）。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 全量录制 | 宪法 2 判据 | 换脑回归数靠真实历史上下文重放裁决 |
| 重打分 action dry-run | §706 墙 | 读候选工具决策不执行，绕过副作用 |
| 回归集随纠正增长 | 免疫 | 用户纠正过的情节改动必须全过 |
| Canary fail-closed | 安全 | 自我修改转正前必须过金丝雀 |

蓝图验收：`daimon replay --against <config>` 产出可对比报告（质量分/成本/回归数）；回归集随纠正自动增长。

下一篇：[12-economy.md](12-economy.md) — 经济系统。
