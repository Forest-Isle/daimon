# 14 · gateway — 组合根与子系统装配

> 包路径 `internal/gateway` · 蓝图 §6「保留：接线对象换血」

## 职责

唯一的组合根（composition root）。装配所有子系统、显式接线、驱动生命周期、分发事件、调度 timer。这是绞杀者改造中"保留并逐 Phase 换接线"的对象。

## 装配模式

每个子系统是 `subsystem_*.go` 中的一个类型，实现 `Name()/Start()/Stop()`。`InitXxx()` 构造函数负责实际接线，依赖用 `agent.DepsBuilder` 后期回填。

### 装配序（`gateway.New()`，`gateway.go:75`）

| 序 | 调用 | 产出 |
|---|---|---|
| 0 | 硬前置：`heart_enabled ⇒ episode_enabled` | 否则报错（心脏需内核）|
| 1 | `agent.NewEventBus()` | 贯穿全局事件总线 |
| 2 | `InitConfig` | 配置 + 热重载钩子 |
| 3 | `InitTelemetry` | 订阅 bus 写 replays/ |
| 4 | `InitFeatures` | 特性开关注册表 |
| 5 | `InitDatabase` | SQLite + 迁移 + `session.Manager` |
| 6 | `taskruntime.NewLedger` | 任务账本 |
| 7 | `InitTools` | 注册表 + 拦截链 + world/values/action store |
| 8 | `DepsBuilder`（Core + Security）| agent 依赖 |
| 9 | `InitAgentRuntime` | `mind.Provider` |
| 10 | `episode.NewRunner` + SetValues + SetCostRecorder | 认知内核 |
| 11 | `sleep.NewRunner(...)` | 全整固作业 |
| 12 | `InitMemorySystem` + cortex | 检索 + memory 工具 |
| 13 | `InitSkills` / `InitMultiAgent` / workflow 工具 | 技能 + 子代理 |
| 14 | `InitMCP` | MCP 子系统 |
| 15 | `agent.NewAgent` + SetApprovalFunc + **SetKernel** | 主代理 |
| 16 | `wireProposals` / `InitHealth` / `InitCommands` / `InitScheduler` | 提案/健康/命令/调度 |
| 17 | heart 启用：`InitHeart` + dispatcher + followup + timer 源 + mail/fs 源 + synthesize 作业 | 自治事件路径 |
| 18 | `config.OnReload(...)` | 热重载回调 |
| 19 | 组装 `subsystems` 列表 | 统一生命周期 |

### 子系统清单

| 子系统 | 文件 | 职责 |
|---|---|---|
| `ConfigSubsystem` | `subsystem_config.go` | 配置 + watcher + reload 路由 |
| `DatabaseSubsystem` | `subsystem_database.go` | SQLite + sessions |
| `ToolSubsystem` | `subsystem_tool.go` | 注册表 + 拦截链 + world/values/action store + 沙箱后端 |
| `MemorySubsystem` | `subsystem_memory.go` | autobiography/procedural/facts + cortex |
| `SkillSubsystem` | `subsystem_skill.go` | 技能加载 + promote/demote |
| `FeatureSubsystem` | `subsystem_feature.go` | 布尔特性开关 |
| `MultiAgentSubsystem` | (init) | 子代理 spawn + context mgr |
| `MCPSubsystem` | `subsystem_mcp.go` | MCP server 管理 |
| `HealthSubsystem` | `subsystem_health.go` | 健康看门狗 |
| `CommandSubsystem` | `subsystem_command.go` | CLI/slash 命令分发 |
| `SchedulerSubsystem` | (init) | 定时任务渠道 |
| `TelemetrySubsystem` | `subsystem_telemetry.go` | EventBus → JSONL 录制 |
| `HeartSubsystem` | `subsystem_heart.go` | 事件心脏（仅 heart_enabled）|

## 入站处理（handleInbound）

`gateway.go:432`——聊天的同步路径：

```
handleInbound(msg):
  空文本 → 返回
  ToolStreamWriter → 装 WithStreamCallback（bash 流式）
  commands.Dispatch → 斜杠命令短路
  heart.RecordChatEvent → 统一事件流去重（重发同 id 跳过）
  agent.HandleMessage → 核心
  finishInbound → task checkpoint + scheduler 状态转移
```

`handleApproval`：`ch==nil`（自治内部情节）→ 无人签 → **拒绝需审批的工具**，只有自动批准/只读工具自治运行。

## 事件分发（heart_dispatch.go）

`eventDispatcher`（小、依赖注入闭包、可隔离单测）：

```
handle(ev):
  Kind == "message"      → skipped（聊天同步处理，绝不在此自治分发）
  internal.daily_brief   → brief()        确定性早报，离认知路径
  internal.health        → health()       健康看门狗，离认知路径
  internal.sleep         → sleep()        整固 cycle，独立 goroutine
  非 internal.*          → recordActivity()（更新 lastEventAt，idle 检测）
  route(ev) → Verdict（错误 → 兜底 Cognize）
  Ignore   → 跳过
  Reflex   → reflex(stub，workflow 后续增量)
  WakeUser → wakeUser(primary channel 通知)
  Cognize  → throttle 检查 → RunInternalEpisode(ev.ID, goalForEvent(ev), ev.Payload, ev.Kind)
```

`goalForEvent`：
- `internal.heartbeat` → "Review active commitments... 无事则静默关闭"。
- `internal.followup` → payload 作 goal（续跑）。
- 其它 → "Handle internal event: \<kind\>"。

`throttle` enforcement **只门控自治 Cognize**——WakeUser/Reflex/确定性分支/chat 结构性不受影响。事件 id 作幂等键（heart at-least-once 重投，内核跳过已完成情节）；事件 kind 作 activity class（成本归因）。

## timer 源

heart 启用时按配置注册（`gateway.go:240-282`）：

| timer | config | 触发 |
|---|---|---|
| `internal.heartbeat` | `heartbeat_interval_minutes` | 巡检 commitments |
| `internal.daily_brief` | `daily_brief_interval_minutes` | 早报投递 |
| `internal.health` | `health_interval_minutes`（+ feature selfops）| 健康巡检 |
| `internal.sleep` | `sleep_interval_minutes` | 自治整固 |
| FSSource | `fs_watch_dirs` | 文件监视 |
| MailSource | `mail.enabled && imap_host` | IMAP 轮询 |
| FollowUpSource | 总是（heart 启用）| 续跑队列 |

## 生命周期（Start / Stop）

`Start`（`gateway.go:334`）：健康 server → MCP server → result store cleanup ticker → **hold drain ticker**（先 `RecoverStaleHolds` 再 `drainHolds`，hold_enabled 时）→ `registerProposalHandler`（先于 channel 启动关 race）→ channels `Start(handleInbound)` → heart `Start`（channels 之后，wake 路径要触达渠道）。

`Stop`：`subsystems.StopAll` → toolSub Stop（停后台索引 goroutine）→ MCP close → db close。

## 其它 gateway 文件

- `brief.go`：`buildDailyBrief`（纯确定性 formatter，过去 24h + 提案 + holds）/ `gatherDailyBrief`（部分失败容忍）/ `deliverDailyBrief`（fail-closed）。
- `throttle.go`：`throttleGate`（[12-economy.md](12-economy.md)）。
- `inspect_commands.go`：五只读 slash 命令（`/episodes` `/trust` `/holds` `/proposals` `/replay`），严格只读，nil-guard 友好降级。
- `trust_notify.go`：`gatewayTrustNotifier` fire-and-forget 升级通知 + journal 审计。
- `hold_runner.go`：hold 执行环（drain ticker）。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 显式装配 + 后期回填 | 可测/低耦合 | subsystem + init_*.go，依赖用指针 |
| internal.* 离认知路径 | 宪法 5/7 | 确定性分支零 LLM，不阻塞、不计成本 |
| heart ⇒ episode 硬前置 | 正确性 | 缺内核则路由事件必失败，提前拒绝 |
| 自治审批拒绝（ch==nil）| 宪法 4 | 无人签则需审批工具不自治运行 |

下一篇：[15-tools.md](15-tools.md) — 工具层。
