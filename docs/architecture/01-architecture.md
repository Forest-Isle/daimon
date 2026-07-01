# 01 · 总体架构

本篇讲清四件事：**包怎么分层、依赖往哪流、组合根怎么装配、一个事件怎么端到端走完**。读完应能在脑中复现"一封邮件 / 一条消息如何让代理醒来、思考、落账"。

## 包布局

```
cmd/daimon/                 CLI 入口（cobra）：tui / replay / world / skill / holds / undo / trust / ...
internal/
  ── 核心认知路径 ──
  heart/        事件心脏：Event 持久化·去重·崩溃恢复·感官源（mail/fs/timer/followup）
  attention/    注意力路由：三级责任链·硬白名单·误判回流
  episode/      情节内核：Composer 组装 + Runner 裸 ReAct + 退出契约 + salvage
  mind/         模型层：Provider 抽象（Claude/OpenAI）·缓存协商·重试熔断·影子脑
  agent/        运行时集成：CognitiveKernel 接口·HandleMessage·子代理·工具调用装配
  ── 状态与行动 ──
  world/        世界模型（唯一事实源）：identity 文件层 + commitments + journal + 混合检索
  values/       价值模型：markdown 条目·ask-once·漂移
  action/       行动层：可逆性分类·信任账本·hold 队列·undo·AST·沙箱
  vcs/          git 门面：EnsureRepo/Commit/Log/RevertFileToPrevious
  ── 离线 / 元系统 ──
  sleep/        睡眠整固：reconcile/rollup/digest/drift/distill/synthesize/proposals 作业
  proposals/    预期引擎：提案队列 + 状态机
  replay/       回放评测：录制读回·重打分·回归集·金丝雀
  economy/      经济系统：成本台账·ROI·节流
  selfops/      自我运维：健康看门狗·错误聚类
  ── 基础设施 ──
  gateway/      组合根：子系统装配·事件分发·timer 源·生命周期
  tool/         工具实现 + 拦截链骨架 + 权限引擎 + 沙箱后端
  channel/      渠道：tui / telegram / scheduler
  skill/        技能库（蒸馏输出端，懒加载）
  workflow/     确定性编排（反射执行底座）
  mcp/          Model Context Protocol：client + server
  hook/         内置 + 用户 YAML 钩子·审计·安全门
  config/       分层配置合并·env 展开·热重载
  feature/      运行时特性开关
  session/      聊天 transcript 存储（已降格）
  memory/       遗留检索引擎（绞杀中，被 world 吸收）
  store/        SQLite 打开 + 嵌入式迁移
  telemetry/    JSONL 录制导出（replay 的写端）
  taskruntime/  任务/情节账本
  appdir/ userdir/  ~/.daimon 路径与用户目录初始化
  errors/ netdial/ util/  小工具包
```

## 依赖方向：单向、零环

宪法第 2 条「换脑无感」要求模型细节只住在 `mind`；整体依赖遵循 **外层依赖内层**（接口下沉，依赖倒置）：

```
gateway（组合根，依赖所有人）
   │
   ├─→ agent ──→ mind            （agent 持 CognitiveKernel 接口，episode 实现它）
   │     │
   │     └─→ tool, session, memory
   │
   ├─→ episode ──→ world, mind, agent(EventBus/接口)
   ├─→ heart ──→ attention ──→ mind
   ├─→ action ──→ tool（叶子，被拦截链调用）
   ├─→ sleep ──→ world, proposals
   └─→ replay, economy, selfops（元系统，读侧为主）
```

关键的**接缝接口**让上下层解耦（详见末节）：
- `agent.CognitiveKernel`——agent 不知道 episode 的存在，只持一个接口；episode 实现它。包依赖方向是 `episode → agent`（episode 引用 agent 的接口/EventBus），而非反过来。
- `heart.Source` / `attention.Router`——心脏不知道具体源/路由实现。
- `action` 拦截器是 `tool.ToolInterceptor`——行动层挂在工具拦截链上，工具包不反向依赖 action。
- `mind.Provider`——episode/attention 只见接口，不见 Claude/OpenAI 具体类型。

这保证：**改内层不破坏外层，换模型只动 `mind` 和配置。**

## 组合根：gateway 装配模式

`gateway.New()`（`internal/gateway/gateway.go:75`）是唯一的装配点，所有子系统在此显式接线、依赖用 `DepsBuilder` 后期回填。装配序（节选自源码，顺序即依赖顺序）：

1. `eventBus`（贯穿全局的事件总线）
2. `InitConfig` → 配置 + 热重载钩子
3. `InitTelemetry` → 订阅 eventBus，写 `~/.daimon/replays/*.jsonl`
4. `InitFeatures` → 特性开关注册表
5. `InitDatabase` → 打开 SQLite、跑迁移、`session.Manager`
6. `taskLedger`、空 `channels` map
7. `InitTools` → **工具子系统**：注册表 + 拦截链 + `world.Store` + `values.Store` + `action.Store`
8. `DepsBuilder`：Core（tools/sessions/db）+ Security（拦截链/hook/permission）
9. `InitAgentRuntime` → `mind.Provider`
10. `episode.NewRunner(provider, worldStore, identity, eventBus)` + `SetValues` + `SetCostRecorder` → **认知内核**
11. `sleep.NewRunner(...)` → 注册全部整固作业（digest/drift/rollup/reconcile/distill/promote/proposals/distillScreen/throttleEval）
12. `InitMemorySystem` → 检索/cortex + 注册 memory 工具
13. `InitSkills`、`InitMultiAgent`（`SubAgentMgr.SetEpisodeKernel`）、注册 workflow 工具
14. `InitMCP`
15. `agent.NewAgent(deps, LinearLoop{}, eventBus)` + `SetApprovalFunc` + **`SetKernel(EpisodeRunner, EpisodeEnabled)`**
16. `wireProposals`、`InitHealth`、`InitCommands`、`InitScheduler`
17. **若 `agent.heart_enabled`**：`InitHeart` + 事件分发器 + followup 源 + timer 源（heartbeat/daily_brief/health/sleep）+ fs 源 + mail 源 + synthesize-rules 作业
18. `config.OnReload(...)` 注册热重载回调
19. 组装 `subsystems` 列表供生命周期统一 Start/Stop

每个子系统是 `internal/gateway/subsystem_*.go` 中的一个 `*Subsystem` 类型，实现 `Name()/Start()/Stop()`。`init_*.go` 风格的 `InitXxx()` 构造函数负责实际接线。详见 [14-gateway.md](14-gateway.md)。

**硬前置**：`New()` 顶部强制 `heart_enabled ⇒ episode_enabled`（`gateway.go:86`）——心脏把事件路由进自治情节，情节需要认知内核；缺内核则每个路由事件都失败，故在分配任何资源前直接拒绝该组合。

## 两条执行路径

Daimon 有两条进入认知的路径，由 `agent.heart_enabled` 决定是否启用第二条。

### 路径 A：聊天（同步，人在等）

```
TUI/Telegram inbound ─▶ gateway.handleInbound（gateway.go:432）
  ├─ commands.Dispatch          斜杠命令短路（/sleep /trust /holds ...）
  ├─ heart.RecordChatEvent      统一事件流去重（heart 启用时；重发的同 id 消息跳过）
  └─ agent.HandleMessage        ── 核心 ──
       ├─ per-session 互斥锁     串行化同会话
       ├─ session.Get/Create + AddMessage(user)
       ├─ if kernelEnabled: runKernel（走 episode 内核）
       │     └─ 组装 CognitiveRequest → EpisodeRunner.Execute → 回复经 channel
       └─ else: 回退 legacy LinearLoop（episode 关，或显式 kernel_fallback_enabled）
```

聊天事件默认应当被 `Cognize`（人在等回复），所以聊天走 `HandleMessage` 直连内核，不经 attention 路由（attention 是为非交互事件做成本过滤的）。

### 路径 B：自治事件（异步，代理自己醒来）

```
heart.Source（mail/fs/timer/followup）─emit─▶ heart.process
  ├─ Persist（先落库，INSERT OR IGNORE 去重）
  ├─ deliver ─▶ attention.Chain.Route        硬白名单 → rules → 小模型 → Cognize 兜底
  │     ├─ Ignore     丢弃
  │     ├─ Reflex     调显式配置的 deterministic tool-workflow
  │     ├─ WakeUser   推 primary channel 通知用户
  │     └─ Cognize    agent.RunInternalEpisode(eventID, goal, trigger, kind)
  │           └─ EpisodeRunner.Execute（同内核，但 channel=nil → 需审批的工具被拒）
  └─ MarkRouted（回填 verdict；崩溃恢复扫 routed_at IS NULL 重投）
```

`internal.` 前缀的内部事件**绕过认知路径**，走确定性分支（`heart_dispatch.go`）：
- `internal.daily_brief` → `deliverDailyBrief()`（确定性早报装配）
- `internal.health` → 健康看门狗巡检
- `internal.sleep` → 触发整固作业 cycle
- `internal.heartbeat` → 仅更新 `lastEventAt`（idle 检测用）
- `internal.followup` → 用 payload 作 goal 续跑情节

这些分支零 LLM 调用，离认知路径，故不计入成本、不阻塞事件处理。

## 端到端：一封邮件的一生

把两条路径串起来，看一个完整自治情节：

```
1. heart.MailSource 轮询 IMAP，新邮件 → emit Event{
     Source:"mail", Kind:"mail.received",
     Payload:{from,subject,message_id}, DedupKey:message_id }
2. heart.process: Persist（message_id 已见则 INSERT OR IGNORE 丢弃）→ 新邮件则 deliver
3. attention.Chain.Route:
     - 是高风险 kind（payment./security./...）？否
     - rules.yaml 匹配？无规则
     - 小模型分诊？→ Verdict{Cognize}（或兜底默认 Cognize）
4. heart_dispatch cognize 分支 → throttle 检查 → agent.RunInternalEpisode(
     idempotencyKey=eventID, goal="处理邮件事件", trigger=payload, class="mail.received")
5. episode.Runner.Execute:
     - 幂等检查：该 eventID 已有 outcome？跳过（崩溃重投安全）
     - Composer.composeSystem：宪法 + 人格 + identity digest + 高置信 values
                              + 相关 commitments + world.Retrieve(goal) 相关记忆 + goal
     - 裸 ReAct 环（≤20 轮）：provider.Stream → 解析 tool_calls
         · 调 episode_close？→ 解析 Outcome → close
         · 其它工具？→ req.Invoke（经拦截链：权限/hook/verify/action/审计）→ 结果回灌
     - 未交账则 salvage 兜底（启发式提取 Outcome，标 Salvaged=true）
6. episode.close:
     - 展开 FollowUps：watch/check → commitment.create；timer → planter.Plant（heart followup 队列）
     - world.ApplyOutcome（事务性：WorldWrites + journal outcome 条目，按 episodeID 幂等）
     - 发布 TurnClosed 事件 → telemetry 录制进 replays/
7. heart.MarkRouted(eventID, "cognize")
8. 经济侧：episode 累计的 Usage → costs 台账（best-effort，离返回路径）
```

每一跳都落到世界模型，使代理的决策与行动持久、可检索、可回放、可审计。

## 跨包接缝（改代码前必懂）

### CognitiveKernel（在 `agent` 包定义，`episode` 实现）

```go
// internal/agent/cognitive.go
type CognitiveKernel interface {
    Execute(ctx context.Context, req CognitiveRequest) (CognitiveOutcome, error)
}
type ToolInvokeFunc func(ctx, iteration int, call mind.ToolUseBlock) (output string, isError bool)
```

`CognitiveRequest` 携带内核所需一切：SessionID / EpisodeID / ParentEpisodeID / Goal / Trigger / Persona / Rules / Memories / Model / Provider / ActivityClass / Transcript / ToolDefs / **Invoke**。**所有 runtime 资产（人格/规则/记忆/转录本/工具/拦截链/审批/录制）由 agent 组装并注入 request，内核自身零子系统依赖。** 这是「换脑无感」的结构基础——内核只见接口与数据。

### heart.Source（长驻事件发射器）

```go
type Source interface {
    Name() string
    Run(ctx context.Context, emit func(Event) error) error  // 长驻，断线自重连
}
```

telegram/tui 入站、timer、mail(IMAP)、fs(fsnotify)、followup 各实现一个 Source。

### attention.Router（任意可替换）

```go
type Router interface { Route(ctx, ev heart.Event) (Verdict, error) }
```

`Chain` 是默认组合实现（硬白名单 → rules → model → Cognize 兜底），可整体替换。

### action 拦截器（挂在工具拦截链上）

行动层不是独立服务，而是工具拦截链中的一段 `tool.ToolInterceptor`。完整链序（`subsystem_tool.go:185`）：

```
permission → hook → user_hooks → read_before_edit → [verify] → [audit] → action → activity
```

`action` 段在 permission 放行之后、activity 之前，只看到被许可的调用与原始执行结果。详见 [08-action.md](08-action.md) 与 [15-tools.md](15-tools.md)。

下一篇：[02-heart.md](02-heart.md) — 事件心脏。
