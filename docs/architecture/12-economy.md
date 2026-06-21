# 12 · economy — 经济系统

> 包路径 `internal/economy` · gateway 节流 `throttle.go` · 蓝图 §4.11

## 职责

让代理能核算自己的价值，为更深授权提供依据。每情节成本写台账，按 activity class 月度 ROI 报表（成本 vs 产出），负 ROI 类自动节流。

## 成本台账

```go
// internal/economy/economy.go
type Entry struct {
    ID, EpisodeID, Model, Provider, ActivityClass string
    InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens int
    OccurredAt int64  // epoch 秒，调用方供（store 保持 clock-free）
}
func (s *Store) Record(ctx, e Entry) error
func (s *Store) ByModelSince(ctx, cutoff) ([]ModelTotals, error)
func (s *Store) ByActivityClassAndModelSince(ctx, cutoff) ([]ClassModelTotals, error)
```

成本归因路径（`gateway.go` 的 `costRecorderAdapter` / `routeCostAdapter`）：

- **情节成本**：`episode.Runner` 累计每次 provider 调用的 `Usage` → `EpisodeCost{EpisodeID, Model, Provider, ActivityClass, Usage}` → 台账。**离返回路径异步写**（`go func` + 5s 有界 ctx + panic 守卫），成本记录是 observational，慢/锁定的 DB 绝不延迟或扰动情节。幂等 per episode，重试不双记。
- **路由成本**：attention 小模型分诊 token，activity class `"routing"`，无 episode id 用随机 id 兜底。

## 定价

```go
type Price struct { InputPerMTok, OutputPerMTok, CacheReadPerMTok, CacheCreationPerMTok float64 }
// lookup: 先精确匹配，再最长子串（"claude-opus-4-8" 由 "claude-opus" 定价）
func CostUSD(model string, totals Totals) (usd float64, priced bool)  // 无费率 → priced=false
```

配置 `economy.prices`（per-million-token 费率）。无费率的 model 报表显示 token 数（不强标 $0）。

## ROI 报表

`activity class`（路由 kind 聚合）月度：成本 vs 产出。三维核算：

1. **token 成本**（台账）。
2. **clean-rate 代理**（verified 行动数 / salvage 率，从 world outcome）。
3. **自报美元价值**：情节 `Outcome.value_created_usd`（迁移 038）→ journal `value_created_usd` 列 → ROI 报表 VALUE$ 列按 class 求和。

**价值走 world 不走成本台账**（`recordCost` 是 token-only，value 是 Outcome 字段 → 宪法第 1 条 world 唯一真相）。CLI `daimon costs` 出月报（[21-cli-reference.md](21-cli-reference.md)）。

## 节流（advisor + enforcement）

`throttle.go`（gateway）：

```go
type throttleGate struct {
    throttled map[string]bool  // 被节流的 class
    overrides map[string]bool  // 用户否决
}
func (g *throttleGate) ShouldSkip(class string) bool  // throttled && !overrides
```

- **advisor（observe-only）**：`throttleEvalJob` 每 refresh 重建 gate——`gatherClassValues`（成本台账 ⋈ world outcome quality，联结在 gateway 保经济不耦合 world）→ `Evaluate` → 剔除高风险 kind（`attention.DefaultHighRiskKinds`）→ 新节流 class 通知一次。
- **enforcement（gated）**：`config.ThrottleConfig.Enforce`（默认 false）。`throttleWindow = 30*24h` 滚动窗口。**enforcement gate 只在 heart_dispatch 的 cognize 闭包**——仅自治 Cognize 受影响，WakeUser/Reflex/chat 结构性不受影响。
- `daimon throttle list|off|on`：用户查看 + 否决。

策略：某 class 连续两月 ROI 为负且无 WakeUser 记录 → 自动降级该类 watch 并通知（可否决）。

## 数据

`costs`（迁移 034）；journal 扩列 `value_created_usd`（038）。详见 [19-data-layer.md](19-data-layer.md)。

## 跨包接缝

- **← episode**：`CostRecorder` 收每情节 Usage。
- **← attention**：`RouteCostSink` 收路由成本。
- **→ world**：ROI 联结读 outcome quality + value_created；advisor 不耦合 world（联结在 gateway）。
- **← gateway**：节流 gate 在 cognize 闭包门控自治情节。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 成本异步 observational | 宪法 3/5 | 记录失败/慢绝不影响情节 |
| 价值走 world 不走台账 | 宪法 1 | world 唯一真相，台账只记 token |
| enforcement 仅自治 Cognize | 安全 | WakeUser/chat 结构性不受节流 |
| enforce 默认 off | 零行为变更 | 观察态成熟后再开 |

蓝图验收：月报能回答"它这个月花了多少、值不值"；节流触发有通知可否决。

下一篇：[13-selfops.md](13-selfops.md) — 自我运维。
