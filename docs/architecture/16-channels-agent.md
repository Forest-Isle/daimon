# 16 · channel + agent — 渠道与运行时集成

> 包路径 `internal/channel`、`internal/agent` · 蓝图 §4.13

本篇覆盖两件事：用户 IO 渠道（TUI/Telegram/scheduler），以及 agent 运行时如何把渠道、工具、审批、会话装配进认知内核。

## channel — 渠道

### 核心类型

```go
// internal/channel/channel.go
type Channel interface {
    Name() string
    Start(ctx context.Context, handler InboundHandler) error
    Send(ctx, OutboundMessage) error
}
type InboundMessage struct {
    MessageID string  // 源内唯一；"" 禁用去重
    Channel, ChannelID, Text, UserName string
    Metadata map[string]string
}
```

可选投递接口（被 episode/proposals/heart 用）：`NotificationSender`、`ApprovalSender`、`ProposalSender`、`StreamUpdater`、`ToolStreamWriter`。

### 三个渠道

| 渠道 | 目录 | 定位 |
|---|---|---|
| **Telegram** | `telegram/` | **主渠道**：提案 / 审批 / hold 撤回全部 inline 按钮化 |
| **TUI** | `tui/` | **调试控制台**：BubbleTea，全部现有能力 + 五只读检视命令 |
| **scheduler** | `scheduler/` | 定时任务渠道（cron 语义，原 channel/scheduler）|

**Telegram inline**（蓝图主渠道）：
- 审批：`SendApprovalRequest` → `[批准/拒绝]` callback（替内存 always-approve，由 trust_ledger 接管）。
- 提案：`SendProposal` → `[做/不做]` callback（[10-proposals.md](10-proposals.md)）。
- hold 撤回：`recall:<holdID>` callback（端到端 <1s）。
- `primaryNotifier`：单用户主权代理，Telegram addressed to first allowed user 即委托人（`heart_dispatch.go:196`）。

**TUI 五只读检视命令**（`inspect_commands.go`）：`/episodes`（近 15 条 outcome）`/trust`（trust_ledger）`/holds`（待执行 holds）`/proposals`（pending）`/replay`（语料汇总）。严格只读，变更操作仍走 `daimon` CLI（宪法第 4 条人签保留）。

### 每日早报

固定 timer 情节（`internal.daily_brief`），确定性装配过去 24h 摘要 + 提案队列 + 待审批，Telegram 投递（[14-gateway.md](14-gateway.md) brief.go）。

## agent — 运行时集成

`agent` 包是认知内核与 runtime 资产之间的桥。它**不知道 episode 的存在**，只持 `CognitiveKernel` 接口（[01-architecture.md](01-architecture.md)）。

### HandleMessage — 单一入口

`agent.go` 的 `HandleMessage` 是所有模式（chat/heart/subagent）的唯一入口：

```
HandleMessage(ch, msg):
  per-session 互斥锁（sessionLocks）串行化同会话
  session.Get/Create + AddMessage(user)
  priorTranscript = session 历史
  kernelEnabled？
    是 → runKernel（走 episode 内核）
    否 → 回退 legacy LinearLoop（绞杀残留）
  session.Persist + 发布 SessionEnded
```

### runKernel — 路由一个 chat 回合

```
runKernel(ch, sess, msg, transcript):
  ParentEpisodeID = EpisodeIDFromCtx(ctx)   父子情节链
  构建 CognitiveRequest{Goal:"Respond...", Persona, Rules, Memories, Invoke, ...}
  kernel.Execute(req)
  子代理（SubagentContextFromCtx）→ 暂存 LastKernelOutcome（供 Spawn 结果传播）
  kernel 错误 / status="failed" → 回退 legacy loop
  否则 → handled=true，回复经 channel
```

`Invoke` 闭包 = `invokeTool`：经拦截链（权限/审批/hook/verify/action/审计）+ 事件录制。这保证 episode 工具调用与 legacy 路径同治理。

### RunInternalEpisode — 自治事件路径

```
RunInternalEpisode(idempotencyKey, goal, trigger, activityClass):
  内核可用？否 → error
  内部 session（channelID = "evt_" + eventID，幂等）
  CognitiveRequest{EpisodeID:idempotencyKey, Goal, Trigger, ActivityClass, Invoke(nil channel)}
  kernel.Execute(req)
  → nil channel → 需审批工具被拒（自治无人签）
```

由 `heart_dispatch` cognize 分支调用。事件 id 作幂等键，事件 kind 作 activity class。

### 子代理（in-process）

```go
type SubAgentManager struct { deps; kernel CognitiveKernel; episodeEnabled bool }
func (m *SubAgentManager) Spawn(ctx, req SpawnRequest) (*SubAgentResult, error)
func (m *SubAgentManager) SetEpisodeKernel(kernel, enabled)
```

Spawn：fresh sessionID/agentID/chainID → scoped tool registry（**排除 `agent_*` 工具防递归**）→ 覆盖配置（model/persona）→ `SubagentContext` 入 ctx（AgentID/ParentID/Depth/ChainID）→ `HandleMessage(ctx, nil, task)` → 读 `LastKernelOutcome` → `SubAgentResult`。

**flag 门控**：`EpisodeEnabled && cfg.Agent.SubagentEpisodeEnabled`（`gateway.go:194`）。开 → 子代理走 episode 内核（强制 Outcome 交账 + 治理 + 成本归 subagent 类 + 父子链）；关 → legacy。子代理内核运行终态（失败 surfaces summary 不回退 legacy = 无双跑/无治理绕过）。

### CognitiveOutcome 状态传播（§4.3 slice 2）

子代理路径写 `lastKernelOutcome`（仅子代理路径，避共享 chat agent 竞态）→ `buildResult` 设 `EpisodeStatus`：`failed → StatusError`（触熔断 + 合成 error）；`done/blocked/handed_off → Success`（经 `formatResultForParent` surface）。nil → legacy 逐字节不变。

## 数据

`sessions` / `messages`（迁移 001/016/017，[18-supporting.md](18-supporting.md)）；session_chain 父链、previous_summary 增量压缩。

## 跨包接缝

- **→ episode**：`SetKernel(EpisodeRunner)`；`CognitiveRequest.Invoke` 装配工具治理。
- **← channel**：`HandleMessage(ch, msg)`；回复经 channel.Send。
- **→ heart**：`RunInternalEpisode` 被 cognize 分支调用。
- **→ tool**：`invokeTool` 经拦截链。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| agent 不依赖 episode | 宪法 2 | 内核经接口注入，换模型不动 agent |
| Telegram inline 替 always-approve | 宪法 4 | 审批持久化进 trust_ledger |
| 子代理 episode 强制交账 | 宪法 3 | 失败不回退 legacy，无治理绕过 |
| 自治 nil channel 拒审批 | 宪法 4 | 无人签则需审批工具不运行 |

下一篇：[17-skills-workflow.md](17-skills-workflow.md) — 反射执行底座。
