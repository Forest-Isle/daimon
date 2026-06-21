# 10 · proposals — 预期引擎

> 包路径 `internal/proposals` · gateway 接线 `proposals_wiring.go` · 蓝图 §4.9

## 职责

从"它答"到"它提"。极限态的标志性器官——不等用户要求，主动找出"LO 接下来需要但还没要求的事"。

机制：sleep 的 `ProposalsJob`（[09-sleep.md](09-sleep.md)）每日窗口扫 commitments（horizon 72h）→ 生成提案入队 → Telegram 投递 inline 按钮 [做 / 不做] → accepted 点燃执行情节，dismissed 记入冷却（预期质量训练信号）。

## 核心类型

```go
// internal/proposals/proposals.go
type Proposal struct {
    ID, Title, Body  string
    ActionPlan       string  // 被采纳后点燃的情节 Goal
    ActionKind       string  // "episode"（默认）| "promote_skill"
    ActionRef        string  // episode goal | staged skill slug
    Urgency          int     // 0..3，排序队列（最紧急在前）
    SourceCommitment string  // 触发它的 commitment
    State            string  // pending | accepted | dismissed | expired
    CreatedAt, ExpiresAt, DecidedAt int64  // epoch 秒
}
```

## 状态机

```
              ┌──accepted──▶ 点燃 episode / promote skill
pending ──────┤
              ├──dismissed─▶ 14 天冷却抑制同题
              └──expired───▶ 窗口过期（投递经 ListPending 时间检查门控）
```

- `ListPending(ctx, now)`：返回 now 时刻仍 live 的提案（`expires_at=0` 或 `> now`）。
- `Accept(ctx, id)`：转 accepted，`DecidedAt=now`。
- `Dismiss(ctx, id, cooldown)`：转 dismissed，冷却阻断同类提案。

## 闭环（4 个切片）

gateway `wireProposals` + `registerProposalHandler` 接线：

### slice 1 — 决策协调器（`proposalCoordinator`）

`Accept` → `Decide` 原子门控（exactly-once）→ 按 `ActionKind` 分派：

- `"episode"` → `RunInternalEpisode(ActionRef as goal)`（点燃执行情节）。
- `"promote_skill"` → **先于 `store.Decide`** 校验 `action_ref` + 调 `skill.PromoteDraft` 确定性文件移动（绝不走 episode，无 §706 风险），再 Decide。
- accept exactly-once + fire-failure 自愈。unknown kind → error 不消费（fail-closed）。

### slice 2 — 投递驱动（`proposalDeliverer`）

迁移 036 加 `delivered_at`：send-then-mark（at-least-once）+ mutex 串行化避免双发窗口。在 sleep 后触发投递。

### slice 3 — Telegram inline（`channel.ProposalSender`）

`SendProposal` plain-text + `[做 / 不做]` callback → `handleCallback` 异步路由。`proposalHandler` 用 `atomic.Value`，register-before-start 关 race。

### slice 4 — dismiss 学习

`RecentlyDismissedTitles`（14 天冷却）抑制同题，fail-closed。dismissed 记入 attention feedback 作预期质量训练信号。

## typed accept（episode vs promote_skill）

迁移 039 加 `action_kind`（默认 'episode'）+ `action_ref`，promote 提案 30 天 TTL。这是 distill 自治闭环的收口——distill-screen 产 `promote_skill` 提案，coordinator typed accept 把"接受提案"分成两条确定性路径，**promote 路径绝不经 LLM/episode**（确定性文件移动，无 §706）。

## 数据

`proposals`（迁移 033）；扩列 `delivered_at`（036）、`action_kind`/`action_ref`（039）。详见 [19-data-layer.md](19-data-layer.md)。

## 跨包接缝

- **← sleep**：`ProposalsJob` / `DistillScreenJob` 入队。
- **→ agent**：coordinator accept → `RunInternalEpisode`（episode kind）。
- **→ skill**：accept → `PromoteDraft`（promote_skill kind）。
- **→ channel**：`ProposalSender` Telegram 投递；TUI `/proposals` 只读列出。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| typed accept 分派 | 宪法 4 + §706 | promote 走确定性文件移动，不经 LLM |
| dismiss 冷却 | 北极星指标 1 | 被拒同类提案频次自动下降，防骚扰 |
| 每日提案硬上限 | 防骚扰 | 默认上限 |
| accept exactly-once | 正确性 | 原子门控 + 自愈 |

蓝图验收：提案采纳率 >30% 起步（目标 >50%）；被 dismiss 的同类提案频次自动下降。

下一篇：[11-replay.md](11-replay.md) — 回放评测。
