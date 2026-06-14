# DAIMON — 实施文档

> 性质: `DAIMON_BLUEPRINT.md` 的落地驱动文件。基于符合性差距分析（2026-06-13），把"缺陷完善 + 未实施部分"拆成可执行增量。
> 基线: `refound/daimon` @ 67d6d76 · P0/P1 内核 + P2 数据地基 + 自治环骨架(gated) 已在线。
> 纪律: 绞杀者模式，每增量独立 commit（`refound(pX-Y): ...`），结束二进制可用、`make build-bin && make vet && make test-short` 绿。

---

## 0. 现状基线（差距分析结论）

| 阶段 | 状态 | 关键缺口 |
|---|---|---|
| Phase 0 改名+录制 | ✅ 符合 | — |
| Phase 1 episode+world | 🟡 主体完成 | world 无检索门面；FollowUps 不种入；salvaged 不计；Budget/Receipt 未接 |
| Phase 2 heart+attention+action | 🟠 地基+骨架 | action 只观测不强制；feedback/holds 死表；仅 timer 源；无 AST/seatbelt |
| Phase 3 sleep+proposals+shadow | 🟡 sleep 推进 (P3-J1..J4) | sleep 包+digest+drift+synthesize-rules+rollup 已落；distill/reconcile/proposals/replay/shadow 未开工 |
| Phase 4 economy+selfops+sensors | ❌ 未开工 | 包不存在 |
| §4.5 values | ✅ 完成 (P2-G + P3-J2) | ask-once 门控+条目+digest+漂移检测(sleep DriftJob)全落 |
| §4.7 mind | ❌ 未拆出 | 仍在 agent/ |

**三处"标称完成实为骨架"的承重墙隐患（最高优先级）**：
1. action 拦截器只 `RecordAttempt` + 标 metadata，不门控（`action/interceptor.go:14-16` 自述）。
2. episode `Outcome.FollowUps` 入 schema 但 `close()` 丢弃（`episode/episode.go:134`）→ 长任务续跑断链。
3. attention `FeedbackStore` / action `holds` 队列无写入路径，是死表。

---

## 1. 实施顺序与里程碑

```
P0 ─▶ P1 ─▶ P2 ─▶ P3
承重墙   护栏    器官    生命/极限
```

| 优先级 | 增量 | 工作量 | 里程碑判据 |
|---|---|---|---|
| **P0-A** | episode FollowUps 种入 + salvaged 指标 | 2-3d | 长任务自动续跑；指标6可统计 |
| **P0-B** | action 强制管线（trust 门控 + undo 落账 + verify） | 5-8d | 行动有 Receipt；Reversible 自动 undo；hold 闸就绪 |
| **P1-C** | AST 分类 + seatbelt 沙箱档 | 4-6d | 对抗用例零放行；远程触发 bash 强制沙箱 |
| **P1-D** | hold 执行环 + Telegram 撤回 UX | 3-5d | hold 到期自动执行；撤回 <1s |
| **P1-E** | attention feedback 回流 + 高风险硬白名单 | 2-3d | WakeUser 零漏报；纠正入库 |
| **P2-F** | world 检索门面（吸收 memory/retriever） | 5-8d | 连续性测试过；记忆走 world.Retrieve |
| ~~**P2-G**~~ ✅ | values 价值模型（ask-once；漂移→P3-J） | 4-6d | 价值权衡30天零重复问 |
| **P2-H** 🟡 | mail/calendar/fs 源 + chat 经 heart | 8-12d | H1 done(chat ingress 经 heart: dedup+统一事件流, gated); 余: async dispatch+删 legacy(需生产浸泡), mail/cal/fs 源→Phase 4 |
| **P3-I** | mind 拆出 + 影子脑 | 6-10d | 换脑回归零；影子周报 |
| **P3-J** 🟡 | sleep + proposals + replay harness | 15-25d | J1 done(sleep Runner+digest+/sleep); J2 done(drift→值漂移, 完成 P2-G deferred); J3 done(synthesize-rules→修正学成 attention 规则, 闭 P1-E loop); J4 done(rollup→旧 journal 折叠为紧凑摘要, 保留近期窗口); 余: distill/reconcile + proposals + replay |
| **P3-K** | economy + selfops | 10-15d | 月报；故障自报；金丝雀回滚 |

> 说明：P0/P1 在已"完成"阶段内补承重墙与护栏，是放开自治前的硬前置。P2 完成绞杀（剥离 memory/agent 残留）。P3 为复利与极限器官，蓝图明示不设死线。

---

## 2. P0-A — episode FollowUps 种入 + salvaged 指标

### 现状
- `Outcome.FollowUps []FollowUp{Kind,Detail,Goal}` 在工具 schema 与解析中存在，但 `Runner.close()` 仅 `ApplyOutcome`（WorldWrites + journal），FollowUps 被静默丢弃。
- salvage 路径产出 Outcome 但不打标；北极星指标 6（salvaged 率）无数据源。

### 目标
1. 情节关闭时按 FollowUp.Kind 种入续跑机制：
   - `timer`：安排一次性内部事件，到点触发 `RunInternalEpisode(Goal)`。
   - `watch` / `check`：写 world commitment（kind=watch/routine，body=Goal）。
2. salvage 产出的 Outcome 标 `Salvaged=true`，落 journal（kind=outcome，detail 含标记）并发 `EpisodeSalvaged` 事件，供 telemetry 录制与指标统计。

### 设计
**新增 `follow_ups` 表（迁移 031）** —— 与 heart 同构的一次性事件源，避免耦合 cron 调度器：
```sql
CREATE TABLE IF NOT EXISTS follow_ups (
    id TEXT PRIMARY KEY,
    source_episode TEXT,
    kind TEXT NOT NULL,          -- timer
    goal TEXT NOT NULL,
    trigger TEXT,
    fire_at INTEGER NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending', -- pending | fired | cancelled
    created_at INTEGER DEFAULT (strftime('%s','now'))
);
```

**episode 侧**（`internal/episode`）：
- 新增接口 `FollowUpPlanter interface { Plant(ctx, episodeID string, f FollowUp) error }`，`Runner` 持有可选字段（nil 跳过），`NewRunner` 增参或 setter。
- `close()` 在 `ApplyOutcome` 成功后遍历 `out.FollowUps`，`watch/check` 直接转 `world.Mutation{Op:"commitment.create"}` 合并进 WorldWrites；`timer` 调 `planter.Plant`。
- `Outcome` 增 `Salvaged bool`（框架置位，非模型字段）；`salvage()` 返回前置 `true`。
- `close()` 接收 salvaged 标志：写 journal detail `salvaged=true`；`bus.Publish(agent.EpisodeSalvaged{SessionID, EpisodeID})`（新事件类型，agent 包定义）。

**gateway 侧**：
- 新建 `internal/heart/followup.go`：`FollowUpStore`（CreateFollowUp / DueFollowUps / MarkFired）+ `FollowUpSource`（heart.Source：轮询 due → emit `Event{Kind:"internal.followup", Payload:goal}`）。
- `goalForEvent` 增 `internal.followup` 分支：直接用 payload 作 goal。
- gateway 在 heart 启用时注册 `FollowUpSource`，并把 `FollowUpStore` 适配为 episode 的 `FollowUpPlanter`（timer kind → CreateFollowUp，fire_at 由 Detail 解析的 interval 决定，缺省 +1h）。
- 非 heart 模式：planter 为 nil（FollowUps 仅 watch/check 经 commitment 落地，timer 类记 journal 警告）。

### 涉及文件
- 新增：`internal/heart/followup.go`、`internal/store/migrations/031_follow_ups.sql`
- 改：`internal/episode/episode.go`（close/salvage/Runner）、`internal/agent/events.go`（EpisodeSalvaged）、`internal/gateway/gateway.go` + `subsystem_heart.go`（注册源+planter）、`heart_dispatch.go`（goalForEvent）

### 验收
- 单测：`handed_off` + timer FollowUp → follow_ups 表有 pending 行；FollowUpSource 到点 emit 事件；watch FollowUp → commitments 有新行。
- 单测：salvage 触发 → Outcome.Salvaged=true，journal detail 含标记，EpisodeSalvaged 事件发出。
- `make test-short` 绿。

### 依赖
无（自包含）。

### 已知限制（Codex 审查确认，P1 跟进）
- **timer follow-up 非事务**：planter 写 heart follow_ups 队列（独立 store），无法并入 world 事务 → best-effort + Error 日志。进度本身已随 outcome/commitment 持久化，丢失的仅是续跑便利。P1 引入事务性 outbox。
- **内部情节非幂等**：heart at-least-once（§4.1），崩溃在 deliver 与 MarkRouted 之间可能重跑情节（新 episodeID/world 写）。dedup_key 挡住常见重发；完整幂等（按 event id 去重 + Receipt 查重）属 P2。
- **failed outcome 不重试**：heart 总是 MarkRouted，cognize 失败不回流重试 —— heart 重试语义属 P1-D 范畴。

---

## 3. P0-B — action 强制管线

### 现状
`action.Interceptor.Intercept` 先 `next()` 执行，再 `RecordAttempt` + 标 `action_class`。纯观测，不门控、不入 hold、不落 undo、不跑 verify。链位置：`permission → hook → user-hook → read-before-edit → verify → audit → action → activity`。

### 目标
把 action 拦截器升级为强制管线段，与现有 permission/verify 协调，不破坏既有权限测试：
```
（permission 已做 approve）→ classify → trust → hold/execute → undo-journal → (verify 已有) → audit/record
```

### 设计（增量切分）
**B1 · undo 落账 + Receipt（先行，低风险）**
- Reversible 且执行成功 → 派生 `UndoSpec` 落 `undo_journal`：
  - `file_write`/`file_edit`/`file_patch`：undo_spec = JSON{path, prev_content_ref}（执行前读旧内容，git 仓库内则记 `git:<sha>` 引用）。
  - `world_edit`：git revert ref。
  - 其余 Reversible：记 best-effort 标记（无 undo）。
- 每次 governed 执行生成 `Receipt`（ID/EpisodeID/Tool/Class/ValueRef/Undo/Verified），写入（复用 undo_journal 或新 receipts 视图）。
- episode `Outcome.Receipts` 由 `[]string` → 收集本情节 Receipt ID（通过 Invoke 回传 metadata）。

**B2 · trust 门控 + hold 闸（核心）**
- 拦截器在 `next()` 前查 `TrustLevel(class, contextKey)`：
  - Reversible：直接执行（git 兜底）。
  - Compensable：`level ≥ HoldThenAuto` → 入 hold 队列（不调 next，返回 "held: 将于 Ns 后执行" 结果）；否则维持现状交 permission 审批。
  - Irreversible：始终需审批（permission 已处理；action 记 attempt，封顶 level=HoldThenAuto，宪法第4条）。
- `RecordAttempt` 的 verified 语义保持：仅 Reversible 成功自动记 verified；Compensable/Irreversible 留待 verify 判据或人工确认。
- 与 permission 分工：**permission 决定"要不要人签"，action 决定"分类 + 是否延迟(hold) + undo/verify 账"**。两者不重复门控；Compensable 的 hold 是 permission 放行后的延迟闸。

**B3 · verify 判据接入**
- 复用现有 `tool.NewVerifyInterceptor`，把结果写进 Receipt.Verified（当前 verify 在 action 之前，需把 verify 结果通过 ctx/metadata 透传给 action 记账，或调整链序为 action 包住 verify）。

### 涉及文件
- 改：`internal/action/interceptor.go`（门控+hold+undo）、`internal/action/action.go`（Receipt 类型、派生 undo）、`internal/action/classifier.go`（ContextKey 细化：mail.send→to:domain 等，为未来生活域准备）
- 改：`internal/gateway/subsystem_tool.go`（链序/注入 episode id 提供器）
- 可能改：`internal/agent`（Invoke 回传 Receipt ID 到 Outcome）

### 验收
- 单测：Reversible file_write 成功 → undo_journal 有可逆 spec；MarkUndone 能回滚（git 内验证）。
- 单测：Compensable 模拟工具 + level=HoldThenAuto → 入 holds 而非立即执行。
- 既有 permission/interceptor 测试全过。
- `make test-short` 绿。

### 依赖
hold 真正执行属 **P1-D**（本增量只入队，不建执行环）。

### ⚠️ 风险（安全敏感）
门控改动触碰审批语义，属 CLAUDE.md "双跑/Codex 审"范畴：实现后必须 Codex 独立审查竞态/边界/绕过。

---

## 4. P1-C — AST 命令分类 + seatbelt 沙箱档

### 现状
`bashLooksDestructive` 子串黑名单（`classifier.go:56`）；无 OS 沙箱。

### 目标
- `mvdan.cc/sh/v3/syntax` 解析 bash 为 AST，按命令名 + 参数分类（替子串），抵御变形命令/路径逃逸。
- 代码域工具叠加沙箱档：`tools.exec.backend = host | seatbelt`；远程触发（telegram/timer/internal）情节强制 `seatbelt`，经 `sandbox-exec` + 动态生成的 SBPL profile 执行。

### 设计
- 新增 `internal/action/ast.go`：`classifyCommand(cmd string) Class`，遍历 AST 的 `CallExpr`，命中破坏性命令（rm/dd/mkfs/...）或重定向到设备 → Irreversible；写文件类 → 按路径 git 内外定 Reversible/Irreversible。
- 新增 `internal/tool/sandbox_darwin.go`：`sandbox-exec -p <profile>` 包裹命令；profile 限制写路径白名单 + 网络。非 darwin 构建标签回退 host + 警告。
- config：`tools.exec.backend`；channel class 经 ctx 传入（已有 `tool.WithChannelClass`），internal/telegram/timer 类强制 seatbelt。

### 涉及文件
- 新增：`internal/action/ast.go`、`internal/tool/sandbox_darwin.go`、`internal/tool/sandbox_other.go`
- 改：`classifier.go`、`internal/tool/bash.go`、`internal/config`（exec.backend）
- go.mod：`mvdan.cc/sh/v3`

### 验收
- 单测：变形命令集（`r''m -rf`、`$(echo rm)`、`>/dev/sda`）均归 Irreversible。
- 单测：远程触发情节 bash 走 sandbox-exec（mock 验证 backend 选择）。
- 对抗用例集零放行。

### 依赖
P0-B（分类器为门控服务）。

---

## 5. P1-D — hold 执行环 + Telegram 撤回 UX

### 现状
`action.Store` 有 `DueHolds/RecallHold/MarkHoldState/CreateHold`，但无执行环、无撤回 UX。

### 目标
- gateway hold runner：timer 驱动扫 `DueHolds(now)` → 重放被 hold 的工具调用 → `MarkHoldState(executed)`；失败留 pending 重试。
- Telegram inline：Compensable 入 hold 时推送 `[撤回]` 按钮；回调 → `RecallHold`（<1s 生效）。审批同样 inline 化（替内存 always-approve）。

### 设计
- `holds.payload` 存被延迟调用的 `{tool, input, session, target}` JSON；runner 反序列化经 InterceptorChain 重放（绕过 action 二次入 hold —— ctx 标记 `hold_replay=true`）。
- runner 复用 heart timer 或独立 goroutine（默认 10s tick）。
- telegram adapter 增 callback 路由：`recall:<holdID>` / `approve:<reqID>` / `deny:<reqID>`。

### 涉及文件
- 新增：`internal/gateway/hold_runner.go`
- 改：`internal/channel/telegram/adapter.go`（callback + inline keyboard）、`internal/action/interceptor.go`（payload 写入、replay 旁路）、`internal/gateway/tool_approver.go`

### 验收
- 单测：hold 到期 → 工具被重放执行一次，state=executed。
- 单测：撤回 → state=recalled，工具不执行。
- e2e（mock telegram）：撤回端到端 <1s。
- 断电重启后 holds 队列恢复（recover 扫 pending）。

### 依赖
P0-B（hold 入队）。

---

## 6. P1-E — attention feedback 回流 + 高风险硬白名单

### 现状
`FeedbackStore.Record` 仅测试调用；无用户纠正 UX；WakeUser 完全可被模型路由决定。

### 目标
- 用户纠正（"这个不用管"/"怎么没告诉我"）→ `attention_feedback` 入库（命令或 NLU 触发）。
- 高风险 kind 硬规则白名单（如 `payment.*`、`security.*`）永远 WakeUser，永不下放模型路由。
- 标注测试集验证 WakeUser 召回率 100%。

### 设计
- `RulesRouter` 前置不可覆盖的 `hardWhitelist []Rule`（synthesized 规则不能改），在 rules 之前匹配。
- TUI `/attention feedback <event> <expected>` + telegram 纠正回调写 FeedbackStore。
- testdata：`attention/testdata/labeled_events.json`（≥50 条），测试断言 WakeUser 漏报=0、Ignore 准确率>80%。

### 涉及文件
- 改：`internal/attention/attention.go`（Chain 加 hardWhitelist）、`feedback.go`（接线）、`internal/gateway`（纠正入口）、`internal/channel/tui/commands.go`
- 新增：`internal/attention/testdata/labeled_events.json`

### 验收
- 单测：高风险 kind 即使有 ignore 规则仍 WakeUser。
- 标注集测试通过（漏报零容忍）。

### 依赖
无（独立）。

---

## 7. P2-F — world 检索门面（吸收 memory/retriever）

### 现状
`world` 无 `Retrieve`；记忆走 legacy `memory` 包（2363 LOC）经 `req.Memories` 注入；`Mutation.Op` 仅 commitment/journal，无 `fact.upsert`/`identity.edit`。

### 目标
- `world.Model.Retrieve(ctx, Query) ([]Hit, error)` 跨 identity/commitments/journal 三层混合检索（FTS5 + 向量 RRF）。
- 吸收 `memory/file_store.go`/`retriever.go`/`cache.go` 为 world 检索引擎；索引范围扩三层。
- `Mutation` 支持 `fact.upsert`（写 journal kind=fact + 索引）、`identity.edit`（改 markdown + git）。
- Composer `req.Memories` 改由 `world.Retrieve` 供给。

### 设计
- 渐进吸收：先在 world 内包装现有 retriever（适配器），保持 memory 包暂存；Composer 切到 world.Retrieve；回放对比组装质量后再删 memory 残留（绞杀）。
- `temporal_facts`（023）时效语义并入 journal 事实。

### 涉及文件
- 改：`internal/world/`（新增 retrieve.go）、`internal/episode/composer.go`、`internal/gateway/subsystem_memory.go`、逐步删 `internal/memory` 残留

### 验收
- 连续性测试（蓝图4.4）：仅凭 `~/.daimon` 重启，三问答案一致，进回归集。
- 组装上下文逐字节可复现（回放前提）。

### 依赖
P3 replay harness 用于质量对比（可后置）。

---

## 8. P2-G — values 价值模型

### 现状
包不存在；action 管线无 values 检查（管线首段缺失）。

### 目标
- `internal/values`：条目 markdown（`~/.daimon/world/values/<domain>/<slug>.md`），confidence/provenance/state。
- ask-once：情节遇无条目覆盖的价值权衡 → action 拒绝自主放行 → `Status=blocked`+`OpenQuestion` → 经渠道问 → 回答成新条目 → 续跑。
- 漂移检测归 sleep（P3-J）。
- Composer 注入价值观 digest（高置信条目）。

### 设计
- `values.Store`（加载/写入 markdown + 索引）；`action` 管线首段查 values：非低风险且无许可 → 触发 ask-once（经 Outcome.OpenQuestion + FollowUp 续跑，复用 P0-A）。
- Receipt.ValueRef 引用许可的 value id 或 trust 等级。

### 涉及文件
- 新增：`internal/values/`、`internal/tool/values.go`（可选读写工具）
- 改：`internal/action/interceptor.go`（values 段）、`internal/episode/composer.go`（digest）

### 验收
- 同一权衡建立条目后 30 天零重复问。
- 每个自主行动 Receipt 可引用许可来源。

### 依赖
P0-A（OpenQuestion 续跑）、P0-B（action 管线）。

---

## 9. P2-H — mail/calendar/fs 感官源 + chat 经 heart

### 现状
heart 仅注册 TimerSource；聊天走 legacy `HandleMessage` 直连（kernel flag 后），不经 heart。

### 目标
- heart 新增源：`mail`（IMAP IDLE）、`calendar`（CalDAV 轮询）、`fs`（fsnotify 监视配置目录）。
- 聊天事件经 heart→attention→episode（P2-6）：`HandleMessage` 改为 emit heart 事件，剥离直连路径，完成 session 降格绞杀。

### 设计
- 各源实现 `heart.Source`，断线自重连；dedup_key（邮件 Message-ID、telegram update_id）。
- 聊天经 heart：telegram/tui inbound → `Event{Source:"telegram",Kind:"message"}`；attention 对 message 默认 Cognize（人在等）；episode 回复经 outbound 接口。需保证延迟可接受（rules 直通 message→Cognize）。

### 涉及文件
- 新增：`internal/heart/source_mail.go`、`source_calendar.go`、`source_fs.go`、`source_chat.go`
- 改：`internal/channel/*`（inbound 转 Source 适配）、`internal/gateway`（接线，删 legacy 直连）

### 验收
- 三源并发一周无丢失（对账 events 表）。
- 聊天经 heart 后回复延迟与直连相当；legacy 路径删除后测试绿。

### 依赖
P1-E（attention 成熟）；绞杀 session 需谨慎，独立 commit 可回退。

---

## 10. P3-I — mind 模型层 + 影子脑

### 目标
- 从 `agent/` 拆出 `internal/mind`：`provider.go`/`claude_provider.go`/`openai.go`/`circuit_breaker.go`/`cache_metrics.go` 迁入。
- `Caps.CacheBreakpoints` 由 Provider 声明，Composer 按声明放缓存边界 → 消灭硬编码 `<!-- CACHE_BOUNDARY -->`。
- `Shadow`：订阅 Cognize 事件副本，同 Composer 组装、推理、**行动 dry-run**，结果进 replay 对比。

### 验收
换 Cognition 模型不触碰 mind 包外代码；影子周报给"每千 token 质量分"；thinking 通道跨 provider 统一。

### 依赖
P2-F（Composer 稳定）、P3-J（replay 评分）。

---

## 11. P3-J — sleep + proposals + replay harness

### 目标
- `internal/sleep`：reconcile（记忆和解，吸收 `memory/lifecycle.go`）/ rollup（journal 周卷叠）/ digest（重算 identity+commitments digest）/ distill（技能蒸馏：重复≥3且全 verified → workflow/SKILL + 金丝雀转正）/ synthesize-rules（从 feedback 合成路由规则）/ drift（价值漂移）。
- `internal/proposals`：模拟情节扫 commitments(72h)+日历+watches → 提案队列（迁移 031→改号，注意与 follow_ups 031 冲突，proposals 用 032）；Telegram inline [做/不做/改]；每日早报。
- `internal/replay` harness：离线重打分 + 回归集（correction 关联情节自动入集）+ 金丝雀（最近50情节回放）。

### 数据
迁移：proposals、costs 见 P3-K、episodes/outcomes 扩列。

### 验收
连续4周蒸馏≥1转正技能且其后该模式零认知调用；提案采纳率>30%起步；`daimon replay --against <config>` 产出对比报告。

### 依赖
P2 全部；replay 依赖 telemetry 录制（已在）。

### 进度
- **J1 ✅** `internal/sleep` 基座 + digest job + `/sleep` 命令（commit 2144085）。
  Runner 串行执行 jobs（per-job error+panic 隔离；mutex TryLock 防并发周期互相覆盖陈旧快照；取消即停）。
  DigestJob 从 commitments+近期 journal 经 LLM 重算自我 digest，幂等 upsert 为稳定 world fact（`fact_sleep_digest`），空 world 跳过 LLM，绝不自喂上轮 digest。
  `/sleep` 在 2 分钟 bounded ctx 下按需跑并汇报。completerAdapter 增可配 maxTokens（默认 512 不变；digest 用 1024）+ 截断告警。
  Codex 审查采纳：panic 隔离 / 周期串行 / 命令超时 / 取消 break / 截断可见。
- **J2 ✅** drift slice（commit 2abcb16）。`internal/sleep/drift.go` DriftJob：LLM 判近期 journal 是否抵触某 active value → 标 active→drifting；drifting 值不再授权自主行动（Lookup 仅 active）→ 下次自主行动重跑 ask-once 用户复核。fail-safe（误报仅多问一次）。无 active 值/无活动则跳过 LLM；校验 flagged id（忽略幻觉）；每标记 1 值落一条 kind="drift" journal 审计。`values.Store.MarkDrifting` 单锁 read-modify-write（防并发复核被陈旧快照覆盖）+ id→path 索引（改名不再生成幽灵文件）。**完成 §4.5 flow-2（P2-G deferred）。** Codex 审查采纳：幽灵文件/锁内丢更新/journal Detail 纳入(漏检即不安全方向)/字符串感知 JSON 解析降级 no-drift。
- **J3 ✅** synthesize-rules slice（commit 1e3be47）。`internal/sleep/synthesize.go` SynthesizeRulesJob：挖用户路由修正(attention feedback)→生成确定性 attention 规则，重复修正不再耗 model/cognize 调用。仅当某 (source,kind) 修正**一致**且来自 **≥2 不同事件**才合成；跳过已被现有规则(通配/有效 action 语义)覆盖者。安全：合成 action=用户自身 expected；高风险白名单在 Chain 中先于 rules→合成的 ignore 永不丢高风险事件。`heart.Store.KindsByID` 批量解析 event source/kind(feedback 仅存 event_id)。gateway `feedbackCorrectionSource`(feedback⋈events join 在边界,job 纯逻辑) + `rulesFileSink`(读/merge/原子 temp+rename 重写 rules.yaml,mtime 守卫防覆盖手改)。合成规则次次重启生效。**闭 P1-E feedback loop。** Codex 审查采纳：跳过 reflex(无 ReflexID)/通配+有效 action 覆盖检查/原子写+mtime 守卫/distinct-event 计数。
- **J4 ✅** rollup slice（commit 39dbad8）。`internal/sleep/rollup.go` RollupJob：把早于近期窗口（keepRecent=50）的旧 journal 条目折叠成单条 LLM 摘要，近期窗口保留完整明细。非破坏：折叠条目打 `rollup_id` 标签而非删除（rollup 是仍在原地明细的有损索引）；fact/rollup 两类永不折叠。`world.UnrolledBeyond`（oldest-first 可折叠条目，排除 fact/rollup，OFFSET keepRecent）+ `world.Rollup`（事务：追加 rollup 条目→守卫式 UPDATE 打标→RowsAffected 断言）。不足 rollupMinFold(3) 条则跳过。Codex 审查采纳：Rollup UPDATE 带与读取同一资格谓词 + 事务内断言 RowsAffected==len(foldedIDs)（全有或全无，资格漂移即整批回滚）；buildRollupInput 仅给真正渲染出内容的条目打标，min-fold 闸按已渲染 id 计数（rollup 绝不声称摘要了没见过的条目）。
- **余 (J5+)**: distill（技能蒸馏转正）/ reconcile（吸收 memory/lifecycle）；proposals 预期引擎；replay harness（含 P2-F deferred 连续性回归测试）。

---

## 12. P3-K — economy + selfops

### 目标
- `internal/economy`：每情节成本写 `costs` 表（迁移）；activity class 月度 ROI 报表；某 class 连续两月 ROI 负且无 WakeUser → 自动降级 watch + 通知。
- `internal/selfops`：timer 发 `internal.health` → 健康情节检查（salvaged 率/漏报/holds 积压/磁盘/错误聚类）→ 提案或 WakeUser；自我修改走金丝雀回放 + 单独 git commit（`~/.daimon` git 化），回滚=revert。

### 验收
月报回答"花了多少值不值"；注入故障能自报；任何自我修改可单独回滚。

### 依赖
P3-J（replay 金丝雀、proposals）。

---

## 13. 迁移号分配（修订）

蓝图原规划号与实际已用号冲突，按实际续号：

| 表 | 迁移号 | 增量 |
|---|---|---|
| follow_ups | 031 | P0-A |
| proposals | 032 | P3-J |
| costs | 033 | P3-K |
| episodes/outcomes 扩列 | 034 | P3-J |
| drop task_checkpoints | 035 | P3-J（重组取代 checkpoint 后） |

> 已用：027 world_model、028 action_ledger、029 events、030 attention_feedback。

---

## 14. 工作流约定

- 每增量：写最小 task 说明 → 实现（自包含逻辑委托 Codex workspace-write，surgical 接线 Claude 直改）→ Codex 独立审查（安全敏感增量强制）→ `make build-bin && make vet && make test-short` → commit `refound(pX-Y): ...`。
- 安全敏感增量（P0-B 门控、P1-C 沙箱、P1-D 撤回、P2-H 绞杀）：Codex 反谄媚审查竞态/边界/绕过后再合。
- 每阶段末：删对应 legacy 路径（绞杀纪律），不留长期双轨。
- 北极星指标随 P0-A（指标6）、P1-E（指标7）、P3（1/2/3/4/5）逐步可观测。
