# DAIMON — 实施文档

> 性质: `DAIMON_BLUEPRINT.md` 的落地驱动文件。基于符合性差距分析（2026-06-13），把"缺陷完善 + 未实施部分"拆成可执行增量。
> 基线: `refound/daimon` @ 67d6d76 · P0/P1 内核 + P2 数据地基 + 自治环骨架(gated) 已在线。
> 纪律: 绞杀者模式，每增量独立 commit（`refound(pX-Y): ...`），结束二进制可用、`make build-bin && make vet && make test-short` 绿。

---

## 0. 现状基线（差距分析结论）

| 阶段 | 状态 | 关键缺口 |
|---|---|---|
| Phase 0 改名+录制 | ✅ 符合 | — |
| Phase 1 episode+world | ✅ 主体完成 | world 检索门面(P2-F)✅；FollowUps 种入(P0-A)✅；salvaged 计(P0-A)✅；Receipt(P0-B1 undo)✅；余 Budget 未接 |
| Phase 2 heart+attention+action | 🟡 器官成型 | value_gate ask-once 强制(P2-G/CF5)✅；AST+seatbelt(P1-C)✅；feedback 回流(P1-E)✅；chat 经 heart(P2-H1)✅；余 holds 执行环(P1-D)死表、mail/cal/fs 源未接 |
| Phase 3 sleep+proposals+shadow | 🟡 sleep 近完 (J1..J12) | digest/drift/synthesize/rollup/reconcile/proposals引擎/distill检测半+干净执行双代理(J11 tool-failure+J12 action-verified)+**distill-promote v1(候选→惰性SKILL.md草稿)** + replay 读侧/--against/回归集+金丝雀门 + **shadow 影子周报(§4.7)** + **distill 草稿→active operator-gated 转正(§4.8)** 已落；余 distill 自治转正(Canary 基底缺口)、proposals 投递 UX |
| Phase 4 economy+selfops+sensors | 🟡 economy 成本+ROI+throttle-advisor 完 (C1..C2d+J11+J12+C2e-1+C2e-2) | token 归因+记录基底(迁移034)+月报CLI+配置定价+activity-class+by-class$+ROI-by-class+throttle advisor(observe-only 推荐) 已落；余 C2e-3(throttle enforcement 控制流 gated)、selfops、sensors 未开工 |
| §4.5 values | ✅ 完成 (P2-G + P3-J2) | ask-once 门控+条目+digest+漂移检测(sleep DriftJob)全落 |
| §4.7 mind | 🟢 拆出+影子周报 | `internal/mind` 独立包(provider/cognition 契约); 影子周报每千token质量分(`replay --against`); 余 LIVE 采样影子(可选)+thinking 跨 provider(暂缓) |

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
| **P3-I** 🟢 | mind 拆出 + 影子脑 | 6-10d | mind-split done(provider/cognition 契约拆入 `internal/mind`，3 阶段别名桥，agent→mind 单向零环，33 pkg 绿，commits 19a0055→9faeccf); `Caps.CacheBreakpoints` done(provider 自声明缓存边界, 修 OpenAI marker 泄漏); Shadow 增量1 done(action dry-run record-only, 2456f06); 增量3 done(影子周报每千token质量分=`replay --against`+`quality_per_1k_tok`, 25a93d0)→**§4.7 验收 2/3 满足**; 余: LIVE 采样影子(增量2,可选,非验收必需)、thinking 跨 provider(暂缓) |
| **P3-J** 🟡 | sleep + proposals + replay harness | 15-25d | J1 done(sleep Runner+digest+/sleep); J2 done(drift→值漂移, 完成 P2-G deferred); J3 done(synthesize-rules→修正学成 attention 规则, 闭 P1-E loop); J4 done(rollup→旧 journal 折叠为紧凑摘要, 保留近期窗口); J5 done(replay 读侧→`daimon replay` 离线读回放日志重建 session+健康指标); J6 done(proposals 引擎核心→sleep 扫 commitments(72h) 生成提案队列+`daimon proposals` 只读列出); J7 done(replay --against 离线重打分 harness→重跑录制上下文+haiku 裁判对比新旧+质量/成本/regression 报告; harness make test 全验, 实跑 operator 验证); J8 reconcile/J9 回归集+金丝雀/J10-12 distill检测+双代理 done; **distill-promote v1 done(候选→惰性 SKILL.md 草稿入 staging, 窗口无关 SQL, 3 轮 Codex 审, commit 3f44f7f)**; **distill-promote-active done(operator-gated 草稿→活跃技能, 人工签名 `daimon skill promote/demote`, commit 31ebab2)**; 余: distill **自治**转正(replay 技能-delta+忠实行动重打分基底缺口→Canary→first-exec-hold→反射表) + proposals 投递 UX |
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

### 状态
- **mind-split（provider 契约拆出，done，commits 19a0055→9faeccf 共 4 阶段；Codex 跨厂审 stage1+stage2b 各清）** — 新包 `internal/mind` 承载 LLM provider/cognition 契约：`provider.go`/`claude_provider.go`/`openai.go`/`retry.go`/`circuit_breaker.go`/`cache_metrics.go`/`model_context.go`（+6 provider 测试，含 `provider_contract_test.go`）经 `git mv` 迁入（保历史），`package agent`→`mind`。共享缓存分割标记 `<!-- DYNAMIC_CONTEXT -->` 下沉为 `mind.DynamicContextMarker`（provider 是其消费方），byte-identical 零语义变更。**类型别名桥 3 阶段**消风险：stage1 建 mind + `mind_alias.go` 全符号 re-export（28 引用文件零改即编译）；stage2a 外部 17 文件 `agent.X`→`mind.X`（仅用 provider 契约者 drop agent import，兼用 cognitive 契约者双导）；stage2b 包内 19 文件 bare ident→`mind.X`（委 Codex，AST 级避开 `Provider string` 字段名 / `StopReason:` 字面键 / `.Provider` 选择器陷阱）；stage3 删桥。`mind` 仅 import config+internal/errors（叶子）→ agent→mind 单向零环，**推翻旧"8 文件 import-cycle"顾虑**（第 397 行）。纯移动零行为变更：make test 33 pkg 绿（mind 新包 +1）+ build-bin + `daimon costs` smoke 通过。
- **Caps.CacheBreakpoints（provider 声明缓存边界，done，commit 6598333；Codex 跨厂审 4 清 1 pre-existing）** — `mind.Caps{CacheBreakpoints int}` + `Provider.Capabilities() Caps` 接口方法：provider **声明**是否承接调用方放置的缓存边界，取代 prompt 组装无条件插入 Anthropic 专属 `<!-- DYNAMIC_CONTEXT -->` 魔法注释。`prompt_frame` 仅当 `Provider.Capabilities().CacheBreakpoints>0` 才放 `static.dynamic_boundary` 层。**修真 leak**：OpenAI adapter（及 Claude 非缓存分支）原样发 `req.System`→魔法注释作字面文本泄漏给这些模型。Claude 仅 `supportsCaching()`（同 cache_control 分支的 `c.model` 条件→composer 与 provider 永远一致）时声明 1；OpenAI 声明 0（自动缓存）；Retry 转发 inner。缓存路径行为保留（注释放置不变），非缓存 provider 严格移除注释。9 个 Provider 测试桩加 `Capabilities()`；新测覆盖 Claude/OpenAI/Retry caps + 无 breakpoint 时注释省略。**Codex 抓 1 pre-existing（非本片引入）**：`/model` 运行时覆盖改 `req.Model` 但不重建 provider→caching 跟 `c.model`（构造模型）非 `req.Model`；composer↔provider 仍一致故无新风险，inline 文档为独立 follow-up（修法=模型变更时重建 provider）。make test 33 pkg 绿+vet+build-bin。
- **Shadow 增量1：action dry-run record-only 模式（done，commit 2456f06；Codex 跨厂审 6 清）** — §4.7 影子脑「行动全部 dry-run」基底。ctx-scoped `tool.WithDryRun`/`IsDryRun`：action 拦截器对**受治理(副作用)**调用短路为 record-only（工具不执行，trust/undo/hold/collector 零变更，返合成 receipt `dry_run=true`+`action_class`）；**只读调用照常执行**（影子需观察世界才能推理）。**fail-closed**：仅显式 opt-in 携标志→production ctx 永远真执行；**无 production WithDryRun 调用方**（纯基底）。对抗测试证 production-default-executes；Codex 6 清（fail-closed/无 prod 调用方/受治理短路在 gate-undo-next-trust-collector 前/只读透传/合成 receipt 被 episode loop 容忍[只探 `Metadata["verify"]` nil-safe]/ctx 传播/测试覆盖）。
- **Shadow 增量3：影子周报「每千 token 质量分」（done，commit 25a93d0；Codex 跨厂审 4 项裁决）** — §4.7 验收#2。**影子周报 = `daimon replay --against <shadow-config>` 跑一周录制情节**（J5/J7 离线 replay-compare 即「影子脑 dry-run + replay 对比」机制本身，已落）。补齐唯一缺口：原报告 token 行是 provider **run-total（候选重跑+judge 共用一 provider→judge 成本污染效率读数）**。新增 `RescoreReport.CandidateUsage` 累计**候选模型自身** token（judge 排除），`QualityPer1kTokens()` = avg 裁判分 ÷ (候选每情节平均 token / 1k) = 候选每千 token 买到的质量分（贵脑若质量不超便宜脑则效率排名更低）。分母含**全部 token 字段(cache 计入，"每千 token" 是字面计数)**；候选 token 在生成时即计费(judge 之前)→judge 失败仍计其花费。报告新增 `cand_tokens_* quality_per_1k_tok` 行，原 run-total 行更名 `run_tokens_*` 消歧。Codex 审：cache 计入分母 / run-total 更名 / judge-fail-计花费测试 采纳；float-avg 建议**拒**（沿用 int AvgScore 使 avg_score 与 quality_per_1k_tok 可对账，截断界 <0.6%）。
- **余 §4.7 Shadow 增量2(可选,LIVE)+ thinking：** 增量2=LIVE 采样影子（mind.Shadow 类型+`heart_dispatch.go:68` Cognize 处复制给影子 runner，dry-run ctx 跑第二情节）。**交账重定向危机已解除**：episode `world.ApplyOutcome` 全部 nil-guard（`close`:293 / `failEpisode`:375 / `OutcomeExists`:152），影子 runner 构造为 `NewRunner(shadowProvider, nil/*world*/, nil/*identity*/, nil/*bus*/)` 无 planter/cost→既有 nil 守卫天然 record-only，**不动不变量#3**。但 LIVE 影子=情节级双推理(采样成本翻倍)，其对比仍最终走离线 replay-compare→**§4.7 验收#2 已由增量3 离线路径满足，LIVE 仅为「自动捕获」便利，非验收必需**，暂缓为可选增强。thinking 通道跨 provider 统一：OpenAI provider 零 reasoning 处理，需选 reasoning_content/effort 约定，当前 Claude 路径为主→暂缓（非 speculative 原则）。

### 验收
换 Cognition 模型不触碰 mind 包外代码 ✅(mind-split)；影子周报给"每千 token 质量分" ✅(`replay --against <shadow-config>` + `quality_per_1k_tok`, commit 25a93d0)；thinking 通道跨 provider 统一 ⏸(Claude 路径为主, 暂缓)。

### 依赖
P2-F（Composer 稳定）、P3-J（replay 评分）。

---

## 11. P3-J — sleep + proposals + replay harness

### 目标
- `internal/sleep`：reconcile（记忆和解，吸收 `memory/lifecycle.go`）/ rollup（journal 周卷叠）/ digest（重算 identity+commitments digest）/ distill（技能蒸馏：重复≥3且全 verified → workflow/SKILL + 金丝雀转正）/ synthesize-rules（从 feedback 合成路由规则）/ drift（价值漂移）。
- `internal/proposals`：✅ 核心已落（J6, 迁移 033）。sleep 扫 commitments(72h) → 提案队列（commitments 部分；日历+watches 待 Phase 4 sensors）。投递 Telegram inline [做/不做/改] + 每日早报为后续增量。
- `internal/replay` harness：✅ 读侧(J5)+离线重打分(J7,`--against`)+回归集/金丝雀门(J9)已落。余：correction→session 解析接线（把 journal correction 自动入回归集）+ 忠实 action 重打分（重跑 tool 往返+对比 Outcome）。

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
- **J5 ✅（读侧）** replay harness 读侧。`internal/replay`：读 `<appdir>/replays/*.jsonl`（telemetry P0-B 录的回放日志）→ 按 SessionID 重建 `Session`（ProviderExchange/ToolRoundTrip/TurnClosed/EpisodeSalvaged，保留录制顺序，跨日按文件名=日期排序）→ `Analyze` 离线健康指标（exchanges/tool_calls/tool_failures/salvaged/abnormal_stops/max_token_stops）。`LoadDir` 缺目录→无 session 不报错；`parseFile` 用 ReadBytes 容超长行（完整 system prompt+messages），畸形/崩溃截断的末行 skip+计数（回放日志是 best-effort 遥测，非权威配置→不 fail-loud）。`daimon replay [--replays <dir>] [--session <id>]` cobra 命令打印报告（只读，绝不重跑/连 provider，不碰 DB→可与运行中 daemon 并行）。纯逻辑+边界 I/O，6 单测（重建/跨日时序/畸形+截断跳过/缺目录/非 jsonl 忽略/指标聚合）。**最低风险切片**（无变更/网络/鉴权）。Codex 审查待补（合并 main 前）。
- **J6 ✅** proposals 预期引擎核心（commit 6d6b9a3）。`internal/sleep/proposals.go` ProposalsJob：扫 commitments(horizon 72h) → summarizer 提案 → `parseProposals`(扫所有平衡 `[...]` span，首个非空数组胜，垃圾降级 nil) → 与**仍存活**的 pending 标题去重 → 每周期硬上限 5 → 写 proposals 队列；无 commitment 则不调模型。纯 + 时钟注入（job 不读墙钟）。`internal/proposals` 时钟自由 SQLite Store（Create/ListPending/PendingTitles/Decide；PendingTitles 用存活谓词→过期未决标题不再永久挡复提；Decide 仅改 pending 行，已决/未知报错）。迁移 033 proposals 表 + (state,expires_at) 索引。gateway 边界适配器 `worldCommitmentSource`(due_at 自由格式→Go 内多布局解析，无时区布局走 `ParseInLocation(time.Local)` 防 UTC 偏移误桶；丢无/不可解析 due，留 overdue) + `proposalsStoreSink`(边界盖 created_at)；job 注入墙钟构造。`daimon proposals` 只读列出存活队列。Codex 只读审查 5 项全采纳：存活感知去重 / 本地时区 due 解析 / 去 sleep 包内 time.Now 兜底（nil 时钟=接线错误，Run 报错）/ 多候选 JSON 解析 / `FindConfigPath` 解析配置。投递(Telegram inline [做/不做/改])+采纳点燃情节+dismiss→attention_feedback 为后续增量。
- **J7 ✅** replay `--against <config>` 离线重打分 harness（commit c520520）。`internal/replay/rescore.go` **Rescore**(sessions, Candidate, Judge, opts, now)：把每条录制 ProviderExchange 的上下文原样喂候选 config 的 provider→重新生成→Judge(haiku 档)对比新旧响应→聚合 质量分/regression/errors/skipped + 逐条延迟。**dry-run**：只生成+裁判文本，绝不执行 tool/写 world。跳过空 baseline（纯 tool-call 无 prose）AND baseline 发起过 tool-call 的回合（文本裁判会曲解 action 回合，忠实 action 重打分留后续增量）。verdict 解析字符串感知 balanced-object scan，不可解析→中性非 regression（裁判抽风绝不捏造 regression）。cap 按**候选调用次数**计（decode 错误不耗预算）。`agent.NewProviderFromConfig`(单一 provider 构造点，gateway 运行时与离线工具共用；InitAgentRuntime 改用之)。**ProviderExchange 录 tools/tool_choice/thinking_budget**（omitempty 向后兼容）→重跑见同一契约。`RetryProvider.GetTokenStats` 转发→报告 tokens_in/out（成本）。`daimon replay --against <config> [--judge-model] [--max-exchanges]`。Codex 只读审查 4 项全采纳：录+replay tools 契约 / 用候选 max_tokens / 报告 token 成本 / cap 按候选调用计。**harness 由 make test 全验**（stub Candidate+Judge，10 单测）；**实跑 --against 耗真 token，operator 验证**。
- **CF4+CF5 ✅** 蓝图符合性审计修正（commit 54d4d38；Codex 只读跨厂审计）。**CF4 (不变量#3 交账强制):** `episode.Execute` provider/stream 错误时不调 ApplyOutcome→情节凭空消失；修为 `failEpisode` 返回前提交幂等 blocked Outcome。**CF5 (不变量#4 不可逆永远人签):** `valueGate.Permit` 可经 value/trust 自主放行 Irreversible→修为 Irreversible 仅在场人类(interactive)可授权，value/trust 仅覆盖 reversible/compensable。加固：proposals cap 改 queue-depth + batch 内去重；rescore 报 Capped。审计确认 OK：attention 白名单优先级 / seatbelt fail-closed(CF1) / ApplyOutcome 幂等(CF2) / rescore dry-run。
- **J8 ✅** reconcile slice（commit a211c52；自审，Codex relay 401 全程）。`internal/sleep/reconcile.go` ReconcileJob：ListFacts → LLM 判直接矛盾/近重复 → supersede 陈旧者。仿 drift（保守 prompt / 字符串感知平衡-JSON 解析 / fail-safe no-op 不中止 cycle）。守卫：canonical 须真实存在；任一 group 选为 canonical 的 fact 永不删；自/重复 supersede 跳过；<2 facts 跳 LLM。**SoftInvalidate（非硬丢）**：被取代 fact 离开活跃检索集，但全文存进 append-only `correction` journal（假阳性不毁知识，守不变量#4）；delete+correction 单事务（fact 绝不无痕删）。`world.ListFacts` + `fact.delete` op（`DELETE WHERE id=? AND kind='fact'` 守卫→杂散 target 永不删 outcome/decision/correction 审计行；触发 fts delete；missing→no-op，blank→err）。接入常驻 sleep cycle（并列 digest/drift/rollup）。**推进不变量#1 + CF3 前置**（吸收 memory/lifecycle 矛盾和解到 world）。7 测试。
- **CF6/CF7 ✅** episode 退出契约硬化（commit 5748cea；自审）。**CF6:** `parseOutcome` 改枚举校验 status∈{done,blocked,handed_off}（越界→reject→模型重试；salvage 走 heuristic 兜底=永远 enum-valid）。**CF7:** `close()` 在 ApplyOutcome 失败时改走 `failEpisode`（幂等 nil-writes 重 claim），summary+错误注记仍落账→单个坏 WorldWrite 不再回滚整事务连带 journal marker 致情节消失。2 回归测试。
- **J9 ✅** replay 回归集 + 金丝雀门（commit 3dd00ec；自审）。`internal/replay/regression.go` 建立免疫系统裁决层（§4.10 m2/m3）：`SelectRegression`（必过回归集：salvaged ∪ CorrectionSessionIDs，保序去重跳空 id；correction→session 解析留给持有 episode↔session 映射的调用方，本包对所给 Session 保持纯函数）+ `Recent`（金丝雀窗口"最近 N 情节"）+ `Canary`（把候选重打分约为一个升级判决，**fail-closed**：仅 `Compared>0 && Regressions==0 && Errors<=MaxErrors && !Capped` 才 Passed——改动须挣得升级，绝不默认放行）。全程 action dry-run。纯新文件、零跨包改动、13 单测。**金丝雀门已就位，是 distill/selfops 自我修改转正前的闸。**
- **conformance 复验 ✅（2026-06-15）** `make test`(fts5,CGO,race,30 pkg) 全绿、0 race、vet clean。
- **CF8 ✅ Codex 跨厂审计修正（commit 6684347；relay 恢复后补跑 J8/J9/CF6/CF7 的独立审查，3 轮迭代）。** Codex **抓到一个自审漏掉的真 bug**——正是跨厂审查闸的意义所在。5 项有效全修，1 项驳回：
  - **CF8a (High, #1/#4):** `world.upsertFact` 按 id 删除时**缺 kind='fact' 守卫**（与已守卫的 `deleteFact` 不对称）→ 模型供给的 `fact.upsert` WorldWrite 若 id 指向 outcome/decision/correction 行会抹掉该 append-only 审计行。改为守卫式删除：删不中 + 插入主键冲突 → 整个 Apply fail-closed，审计行存活。test `TestFactUpsertCannotClobberAuditRow`。
  - **CF8b (High):** replay `Canary` 可在未验证 action 回合时通过。新增 `RescoreReport.SkippedAction`，Canary 默认 fail-closed（`AllowSkippedActions` 给纯文本改动 opt-out）。二轮补：空 prose 的 tool-call 回合先命中 empty-baseline skip→也计入 SkippedAction。
  - **CF8c (High):** replay `Canary` 可在裁判判决不可读时通过。新增 `Verdict.Indeterminate`/`RescoreReport.Indeterminate`，Canary 非零即 fail。二轮补：`parseVerdict` 曾接受缺 `regression` 键的 `{"score":N}`（默认 false）→ 改指针探针要求 score+regression 双在，否则 indeterminate。
  - **CF8d (Med, #3):** panic/nil `req.Invoke` 绕过 `failEpisode`→情节无痕消失。Execute 改命名返回+deferred recover：panic 经 failEpisode 留 journal 并以 error 上报。
  - **CF8e (Med, #3):** `parseOutcome` 接受空 summary（必填 schema 字段）→ 改拒绝令模型重试；salvage 路径不受影响。
  - **驳回:** failEpisode 返回 `Status="failed"` 非枚举违规——`agent.go:274` 用 `Status != "failed"` 作运行时 `SessionEnded.Succeeded` 信号，存储的 journal outcome 不含 status 枚举，两层不同；Codex 复审认同。
  - **核验通过:** reconcile delete+correction 原子+keep-guard、`fact.delete` kind='fact' 守卫、episode 各失败路径经 failEpisode 留痕、status 枚举强制——守不变量 #1/#3/#4。**结论：CF8 后已实施模块符合蓝图。**
- **J10 ✅ distill 挖掘——技能蒸馏检测半（commit 9ad02d8；Codex 跨厂审查 3 轮）。** §4.8 灵魂的检测半（情节→技能→反射）。`internal/sleep/distill.go` **DistillJob**：挖 journal 中**重复成功**的情节模式→每个落一条 append-only "distill candidate" decision。**绝不**生成技能/注册反射/promote——promotion 是独立的金丝雀门控切片（J9 Canary 即转正闸），因为自动转正的技能会自主执行（蓝图最高带病转正风险 §706）。仿 reconcile/drift，接入常驻 sleep cycle。
  - 仅挖 clean outcomes——排除 salvaged + failEpisode 失败（stream error/panic/world-write-failed）+ **tool_failures>0（J11 加）+ unverified_actions>0（J12 加）**。**保守双代理**蓝图"全 verified"（零 tool 错误 ∧ 所有受治理行动已 verified）；判官 prompt 再要求真实成功。检测专用→代理只门控检测不门控执行。
  - 幻觉守卫：候选须引用 ≥3 个真实 clean-outcome 情节 id；欠支撑/捏造的候选丢弃。
  - **窗口无关去重**：确定性 Unicode-aware id（slug + 规范名 fnv64a 哈希）+ `world.JournalExists`→稳定模式只记一次，即便旧候选已滚出有界扫描窗口。
  - fail-safe：判官回复不可解析→no-op（绝不中止 cycle/污染 journal），同 reconcile/drift。
  - 辅助改动：`world.JournalExists(id)`（仿 OutcomeExists 的通用存在性查询）；`episode.close()` 的 world-write-failure 标记改前缀→failEpisode 的 500 字截断绝不丢标记（长 summary 仍可被 distiller 识别为失败 outcome）。
  - **Codex 3 轮抓真 bug:** (1)200 行窗口去重→重复增长；(2)失败标记被截断丢失;(3)非 ASCII(中文)名 id 坍缩抑制后续候选——全修。1 项(判官 error vs no-op)驳回为符合 reconcile/drift 既有契约,Codex 认同。9 单测。
  - **余 distill promotion 半（受阻同 economy 的 per-episode 结构化 status/verified 字段）:** 候选→skill/workflow 草稿生成 → J9 Canary 门 → first-exec-hold → attention 反射表转正。
- **J11 ✅ per-episode 干净执行信号——强化 distill 代理（commit 4583522；Codex 跨厂审 2 轮）。** 在不做 interceptor→episode 重管线的前提下，给 distill 的"全 verified"判据加客观信号。`world.OutcomeMeta{Salvaged, ToolFailures}` 取代 `ApplyOutcome` 的裸 `salvaged bool`；`claimOutcomeJournal` 按 detail 编码：`salvaged=true`(优先级, 值逐字节不变向后兼容) | `tool_failures=N` | ``(干净)。`episode.Execute` 计非-close tool 调用的 isError(原先丢弃)→穿 `close()`/salvage；failEpisode 记空 meta(已由 summary 标记排除)。distill 排除 `tool_failures>0`(解析整数非前缀匹配, `=0`/非数字不误伤)——模式仅当情节零 tool 错误才够格蒸馏。**纯观测不影响控制流, 无迁移, 检测专用不 promote。** Codex 2 轮无 blocker/high/med, 2 LOW 修(解析计数+测试断言行存在)。6 文件, 5 新测, make test 31 pkg 绿。
- **J12 ✅ per-episode 受治理行动 verified 信号——键石（commit 3e7c888；Codex 跨厂审 2 轮）。** 补上 distill"全 verified"判据缺的 action-level 真值。此前 action 拦截器对每个受治理(非只读)调用算出的 `verified := succeeded && class==Reversible` 只按 `(class, contextKey)` 聚合进 trust ledger，episode 拿不到。**context-scoped collector**（`internal/tool/ActionVerification`，mutex 守护 governed/verified 计数，镜像 channel_class.go 的 ctx-key）：拦截器对每个受治理调用 `Record(verified)`；episode 在 Execute 顶部装 collector 入 ctx，关闭时算 `unverified_actions = governed - verified` 经 `OutcomeMeta` 戳进 outcome detail；distill 排除 `unverified_actions>0`，把 J11 的 tool-failure 单代理升级为 **tool-failure + 未-verified-受治理-行动 双代理**。detail 优先级链 `salvaged > tool_failures>0 > unverified_actions>0 > ""`(单信号逐字节兼容，多信号任一即排除不丢决策)。只读工具在拦截器 `!governed` 早返前不计；纯推理/只读情节 detail 干净(向后兼容)。**纯观测不影响控制流, 无迁移, 读侧只门控 distill 检测不门控执行。** Codex 抓真耦合: collector `Record` 原在 `i.store==nil` 守卫后→nil trust store 下受治理行动会被误计 unverified_actions=0 而错看成 distill-clean; 提到守卫前修复(信号关乎行动非 ledger)→store-nil 只跳过 ledger/receipt。9 文件, 8 新测(collector 各路径/nil-store/episode detail/world 优先级表/distill 排除+`=0` 仍可蒸馏), make test 32 pkg 绿(race 覆盖 collector mutex)。
- **distill-promote v1 ✅ 候选→惰性 SKILL.md 草稿（commit 3f44f7f；Codex 跨厂审 3 轮）。** §4.8 灵魂晋升环的**安全第一纵切**。`internal/sleep/distill_promote.go` **PromoteJob**（注册在 DistillJob 之后→同一 sleep cycle 先检测后晋升）：把 J10 检测出的 "distill candidate" 转成具体 SKILL.md **草稿**，写入**不被加载的 staging 目录** `~/.daimon/skills-staging`——**绝不自动执行、绝不自动加载、绝不进反射表**（本切零 §706 带病转正执行风险）。候选经 `world.ListDistillCandidatesWithoutDraft`（**纯 SQL** `NOT EXISTS('distill_draft_'||id 标记)`、**最旧优先**、`LIMIT promoteMaxPerCycle`）取——**窗口无关**，候选不会被 journal 增长挤出近-N 视野而饥饿。LLM 只生成 markdown 正文；frontmatter 由 `yaml.Marshal` 我方构造（name/description/metadata.distilled+source_candidate+source_episodes）→模型输出无法伪造元数据。每候选一条 journal marker（drafted / 空-body skip / 空-name skip）→**只处理一次**、永不重复计费 LLM、永不永占 LIMIT 槽。路径按**唯一候选 id**（slug+hash 尾）键控→slugify 相同的不同名永不碰撞丢草稿。**隔离纵深防御**：InitSkills 即便 `skills.extra_dirs` 误列 staging 也拒载；file sink 写前 EvalSymlinks 防 symlink 逃逸。**晋升到 active（Canary 门→first-exec-hold→attention 反射表）是刻意分出的下一步，不在本切。** Codex 3 轮抓真问题全修：cap 数扫描非工作量(饥饿)、slug 碰撞丢草稿、200-窗口饥饿(→改纯 SQL 窗口无关查询)、extra_dirs/symlink 隔离、空-name LIMIT 饥饿；float 精度风格建议拒(附理据)。7 文件, make test 33 pkg 绿(race)。
- **distill-promote-active ✅ 草稿→活跃技能（operator-gated，commit 31ebab2；Codex 跨厂审 2 轮）。** §4.8 灵魂晋升环再进一档：staging 草稿可转正为**活跃 prompt-reference 技能**——**人工签名**，非自治。**为何 operator-gated 而非自治 Canary**：核查 `replay.Canary` 今天**无法诚实门控技能转正**，两处基底缺口——(1) `Rescore` 逐字节重放录制的 `ex.SystemPrompt`，只变 `Model`(rescore.go:189-197)，新技能改的是**重组后**的 prompt(`BuildPromptSection`)，回放看不见→Canary 会**虚假通过**；(2) `AllowSkippedActions` 对"改变工具行为"的变更必须留 false(regression.go:80-90)，蒸馏技能正是工具型→含行动轮的窗口 `SkippedAction>0`→**fail-closed 永不通过**。补"忠实行动重打分"=在回放里执行工具=正是 §706 执行风险本身，本轮不可安全完成。故**宪法不变量4(可逆优先: 不可逆高风险永远人签)**让人成为门——比缺失的 Canary **更强**的门。扩展现有 `daimon skill` 命令组(install/remove 同 UX)：`skill drafts`(列 staging 草稿+校验态) / `skill promote <slug>`(校验+§706 提示+y/N 确认+移 staging→active) / `skill demote <slug>`(取消转正)。**安全**：转正后技能仅惰性 prompt-reference(metadata+lazy `read_skill`)，**不授予任何执行权**——其触发的行动仍走完整拦截链+trust ledger；校验蒸馏溯源(`metadata.distilled`)、拒绝活跃同名碰撞、拒绝 symlink 草稿与越根路径(对齐 sink 的 `ensureDraftWithinRoot`)、**可逆**(demote=移回 staging 草稿，移动非删除→零数据丢失)。staging 仍不被加载(InitSkills 隔离守卫不变)。`internal/appdir` 加 `SkillsDir/SkillsStagingDir` 单一来源；`SkillMeta` 加 distilled/source_candidate/source_episodes；gateway sink 委托 appdir。Codex 2 轮抓真问题全修：symlink 草稿逃逸(Lstat+EvalSymlinks 容纳校验)、demote 永久丢失(→可逆移回)、padded-name 碰撞绕过(TrimSpace)、参数标签不符。6 文件, make test 33 pkg 绿(race), 手动 e2e 全验(drafts/promote/list/demote+非蒸馏拒+`../escape`拒)。**仍 deferred(命名基底缺口，不造假)**：自治 Canary 门(需 replay 技能-delta 测试 + 忠实行动重打分)、首次执行 hold(需 hold 拦截器基底)、反射表转正(需 ReflexID→技能执行器)。
- **distill 聚类窗口无关化 ✅ 检测半的 recall 修复（commit 174aace；Codex 跨厂审无 findings）。** §4.8 检测半(J10 DistillJob)的 LLM 判官**已**做模式聚类(prompt 要它把"同类任务成功≥3次"分组并引证 episode id)——真缺口在**判官能看到什么**：`Run` 原读 `ListJournal(ctx,"",200)` = **最近 200 条全 kind 混合** journal(outcome/decision/fact/correction + 本 job 自己写的 distill 候选)再筛干净 outcome。随 journal 累积非-outcome 行，干净 outcome 被挤出 200-窗→跨时间复现的模式(如几周内 3 次)永不在同一 cycle 对判官**共可见**→漏检。**dedup 侧本就窗口无关**(`distillCandidateID`+`JournalExists`)，**检测侧不是**——本切补上：喂判官最近 N 条**干净 outcome**(kind 过滤)，非最近 200 混合。**LLM 仍是聚类器**(零 under-grouping 风险、零新依赖)，只换其输入窗。`world.ListOutcomes(ctx,limit)`(`WHERE kind='outcome'`、newest-first `occurred_at DESC,id DESC`、默认 200) 替换全-kind 源；`distill.go` 常量 `distillJournalLimit`→`distillOutcomeLimit`，删 `e.Kind!="outcome"` in-Go 跳过(kind NOT NULL→SQL `=` 与旧 Go 等值逐字节同义)。下游全不变(`ClassifyOutcome` 干净筛 + `validIDs` 幻觉守卫 + 判官 prompt + `parseDistillCandidates` + 确定性 dedup)。**检测专用、零 §706/promotion 路径变更**——仍只追加 "distill candidate" decision 行。**测试有牙**: `sleep.TestDistillWindowIndependentOverOutcomes`(3 复现干净 outcome + 250 条更新 decision→候选仍 surface；已验证旧 `ListJournal(200)` 源下**失败**["not enough successful episodes"]、新源下通过；经 `JournalExists(distillCandidateID(...))` 断言——因 future-dated decision 也会把当前时戳候选挤出 ListJournal(200) 助手窗)；`world.TestListOutcomes`(仅 kind='outcome'、newest-first、limit、忽略 decision/fact)。Codex 跨厂审无 findings(SQL 列序匹配 scanJournalEntry/行为保持/§706 不变量/牙)。4 文件, make test 33 pkg 绿(race)。**仍 deferred**：确定性文本相似/嵌入预聚类(结构化≥3, recall 已修则 precision 留后续)、true 全历史聚类+持久 cluster 态(个人规模不需)。
- **当前状态注记（2026-07-01）**：`agent.subagent_episode_enabled` 已默认开；chat kernel 失败默认不再回退 legacy，只有显式 `agent.kernel_fallback_enabled: true` 才恢复旧可用性策略。下面条目保留为当时实现记录。
- **subagent→episode slice 1 ✅ flag-gated kernel routing（commit 54db5ce；Codex 跨厂审 2 轮）。** §4.3 把情节(episode)做成认知执行单元的**默认关纵切**。今天 chat/heart 走情节内核(`episode.Runner.Execute` via `agent.SetKernel`)，但**子代理不走**：`SubAgentManager.Spawn` 建 `NewAgent(subDeps,&LinearLoop{},NewEventBus())`→跑 legacy LinearLoop，**无 Outcome 交账**、私有 event bus(replay-盲)、无情节行动收集器——正是 §4.3 缺口。本切：默认关 flag `agent.subagent_episode_enabled` 开时，`Spawn` 在 `HandleMessage` 前 `subAgent.SetKernel(kernel,true)`→子代理跑 `Runner.Execute`→产**强制 Outcome 交账**、受情节治理、成本归到自己的 ActivityClass。**关时 legacy 路径逐字节不变**(零生产行为变更)。**为何此 seam 而非蓝图 `Episode{ID,Parent}` 对象模型**：包环约束(`episode` import `agent`→`agent/subagent.go` 不能 import `episode`)，现有 `agent.CognitiveKernel` 接口(Runner 满足)是依赖安全缝；结果经**既有** capture 路径流(`Outcome.Reply||Summary`→`ch.Send`→`buildResult`)，无需重写结果提取；`Runner.Execute` 可重入(只读共享 deps，每调用本地建 episodeID/usage/transcript，无 Runner 字段突变)→一个共享 Runner 安全服务 chat+并发子代理情节。**runKernel** ActivityClass 由 `SubagentContextFromCtx(ctx)` 派生("subagent" vs "chat")→子代理情节成本归本类(§4.11 账本)不误标 chat。**子代理内核运行是终态**：交账后的失败 surfaces 其 summary 而非回退 legacy 环(无双跑、无治理绕过)；chat 路径保留 legacy 回退(slog.Warn)。Codex 抓 High-1(子代理内核失败回退 LinearLoop=双跑+绕过治理)已修，Low(并发路径无 race 覆盖)补 `TestSubAgentManager_SpawnParallel_EpisodeKernelEnabled`(mutex-safe stub kernel)。6 文件, make test 33 pkg 绿(race 演练并发 Spawn)。**slice 1.5 ✅ 真内核集成测试(commit cbb1385；Codex 跨厂审无 findings)**: slice-1 单测只用 stub kernel 验路由; 本测在 `internal/episode/subagent_integration_test.go`(package episode, 复用 episodeTestProvider/captureRecorder, 无环=agent 不 import episode)接**真 `episode.Runner`** 为子代理内核→驱动完整 `SubAgentManager.Spawn`, 证端到端缝: (1)路由经 Runner.Execute (2)交账落 world journal(不变量#3) (3)成本归 ActivityClass "subagent"(§4.11) (4)回复经 capture 路径 surfaces 成 result.Output。**有牙**: legacy LinearLoop 路径既不交账 world journal 也不记 episode 成本→"恰 1 outcome 行"+"subagent 成本类"断言无法在 kernel 未真正接线时通过(Codex 确认)。
- **slice 2 ✅ parent linkage + Outcome.Status 传播（commit 3298e2c；Codex 跨厂审 2 轮，世界层委 Codex workspace-write）。** 把子代理情节从匿名变**一等**。**Parent linkage(§4.3 Episode.Parent)**: `episode.Execute` 在工具循环前把自身 episodeID 装进 ctx(`agent.EpisodeIDToCtx`)→从工具调用 spawn 的子代理继承该 ctx(`Spawn` 从入参 ctx 派生子 ctx)→子代理 `runKernel` 读 `EpisodeIDFromCtx`→`CognitiveRequest.ParentEpisodeID`→`close`/`failEpisode` 经 `world.OutcomeMeta` 传入→`claimOutcomeJournal` 写**新列 `journal.parent_episode_id`**(迁移 035)→`OutcomeParentEpisodeID(episodeID)` 读。顶层 chat/heart 情节读 ""(无父)——零特例。子情节失败也记父链。**记录外科手术式**: 专列+专访问器,不动 `JournalEntry`/`detail`(detail 是单槽优先级编码,父树暂无读者→YAGNI 不进 4 处 scan)。**Outcome.Status 传播**: `runKernel` 把内核 Outcome 暂存 `agent.lastKernelOutcome`(**仅子代理路径写**——共享长寿 chat agent 永不写, 故并发 per-session chat 不竞争;子代理 agent 每 Spawn 新建单 HandleMessage→无竞争)→`Spawn` 读入 `buildResult`→设 `SubAgentResult.EpisodeStatus`=忠实交账状态 + 投影粗 `Status`: **仅 `failed`→StatusError**(触发熔断+从 summary 合成有意义 error);`done`/`blocked`/`handed_off`→StatusSuccess, 经 `formatResultForParent` 的 EpisodeStatus 行 surface 给父模型。blocked/handed_off 作 Success **匹配 slice-2 前行为**(prose 无 status 标签默认 Success)→无熔断/workflow 回归, 但不再隐藏真实状态。nil kernelOutcome⇒legacy text-parse 逐字节不变。**Codex 2 轮抓真问题全修**: High(blocked/handed_off→StatusError 致空 error+误触熔断)、Med(lastKernelOutcome 共享 chat agent 竞态)、Low(e2e 跳过 Execute→Invoke 交接→补 `TestExecuteExposesEpisodeIDToDispatchedTools` 直测该链)。12 文件, make test 33 pkg 绿(race)。**Deferred(命名)**：distinct `StatusBlocked`/`StatusHandedOff` 枚举+per-status 消费者分支(暂无消费者需要)、**读取**父树的消费者(distill 子情节聚类/replay 树重建)、config-reload 重应用 SetEpisodeKernel(启动期 flag)、distill 子代理-outcome 过滤、`Episode{ID,Parent}` 对象模型、`ExecModeBackground` 路由、replay-bus 统一(子代理内部 provider/tool 交换上全局 bus)。
- **余（J10+，多数受阻于数据管线，已标前置）**：
  - **distill（§4.8 灵魂，最高带病转正风险）** — 检测半已落(J10)+干净执行双代理已落(J11 tool_failures + J12 unverified_actions)+**草稿生成 v1 已落(distill-promote, 候选→惰性 SKILL.md 草稿入 staging)**。promotion 半仍需：(1) **情节模式聚类**——journal outcome 仅 summary 文本，"重复≥3"由 J10 检测的 LLM judge prompt 聚类(verified 信号 J11+J12 已就位)；**recall 缺口已修(distill 聚类窗口无关化, commit 174aace)**：判官输入从最近 200 全-kind 混合 journal 改为最近 N 干净 outcome(`world.ListOutcomes`)，跨时间复现的模式不再被非-outcome 行挤出窗而漏检；余 deferred=确定性文本相似/嵌入预聚类(precision，recall 已修后留后续)；(2) **草稿→active 转正**——**operator-gated 半已落(distill-promote-active, 人工签名 `daimon skill promote/demote`)**；**自治半 deferred**：核查发现 `replay.Canary` 今天无法诚实门控技能转正(Rescore 逐字节重放录制 prompt 只变 Model→技能 delta 看不见; AllowSkippedActions 对工具行为变更须留 false→行动窗口 fail-closed)，自治需先补 replay 技能-delta 测试 + 忠实行动重打分(=回放执行工具=§706 风险本身)，再叠 first-exec-hold(需 hold 拦截器基底) + attention 反射表(需 ReflexID→执行器)。是 autonomous-执行写入，**CLAUDE.md 强制 Codex 反谄媚审查**。注: J12 的 verified 是"当前可 verified 的行动是否都 verified"；让 Compensable/Irreversible 也能客观 verified 的机制是后续切片(本切片如实反映现状，不放宽门)。
  - **economy（§4.11，P3-K）** — 需先补：(1) **每情节 token 归因**——`GetTokenStats` 是 provider 累积量，并发情节下无法精确按情节切分；需在 episode loop 对 Stream 前后快照 delta（热路径只读加点，低风险但触及主环）；(2) **routing-kind 归因**——ROI"按 activity class 聚合"需把 attention verdict kind 落到成本行，当前 replay 流与 costs 均无此列。迁移 034 costs。
  - **mind split（§4.7/P3-I）— ✅ DONE**（commits 19a0055→9faeccf，见 §10 状态）。原"先解 import-cycle"顾虑经核实不成立：provider 契约文件只 import config+errors（叶子），agent→mind 天然单向。3 阶段别名桥落地，纯重构守不变量#2，make test 33 pkg 绿。**解锁 Shadow**。
  - **selfops（§4.12/P3-K）** — 依赖 economy 指标 + health 事件；自我修改走 J9 金丝雀 + 单独 git commit。
  - **proposals 投递 UX（§4.9）** — Telegram inline [做/不做/改]+采纳点燃情节+dismiss→attention_feedback；需 live Telegram，非 make-test 可验。
  - **subagent→episode（§4.3）** — **slice 1+1.5+2 已落(1=flag-gated kernel routing 默认关 commit 54db5ce; 1.5=真内核集成测试 cbb1385; 2=parent linkage[journal.parent_episode_id 迁移035]+Outcome.Status 传播[EpisodeStatus+粗投影 failed→error] commit 3298e2c)**。余切：读取父树的消费者(distill 子情节聚类/replay 树)、distinct Blocked/HandedOff 枚举、`Episode{ID,Parent}` 对象模型、ExecModeBackground 路由、replay-bus 统一、"10 并行情节无串扰"§4.3 验收。
  - **P1-D hold 执行环** — 需生活域 Compensable 工具先落（当前 classifier 不产 Compensable）。
  - **CF3 retire legacy memory** — reconcile(J8)+P2-F 连续性测试已备，仅剩 P2-H 生产浸泡解阻。

---

## 12. P3-K — economy + selfops

### 目标
- `internal/economy`：每情节成本写 `costs` 表（迁移）；activity class 月度 ROI 报表；某 class 连续两月 ROI 负且无 WakeUser → 自动降级 watch + 通知。
- `internal/selfops`：timer 发 `internal.health` → 健康情节检查（salvaged 率/漏报/holds 积压/磁盘/错误聚类）→ 提案或 WakeUser；自我修改走金丝雀回放 + 单独 git commit（`~/.daimon` git 化），回滚=revert。

### 验收
月报回答"花了多少值不值"；注入故障能自报；任何自我修改可单独回滚。

### 依赖
P3-J（replay 金丝雀、proposals）。

### 进度（economy 切片）
- **C1（per-episode token 归因，done）** — `agent.Usage`{Input/Output/CacheRead/CacheCreation} 落在 `CompletionResponse` 与 stream 终态 `StreamDelta`；Claude/OpenAI provider 各自归一化（OpenAI 的 cache-inclusive prompt_tokens 拆成 exclusive；无 drain，finish_reason 即终态）。零=未知非免费，纯观测不入控制流。episode loop 对每次 Stream 累加 delta，`recordCost` 经 `CostRecorder` 写出。
- **C2a（成本记录基底，done）** — `internal/economy`：`Entry`/`Totals`/`Store.Record`（按 EpisodeID 幂等 `cost_<id>` + INSERT OR IGNORE，负值钳零）/`TotalSince`；迁移 034 costs（含 idx_costs_occurred、idx_costs_class）。gateway 适配器 fire-and-forget 异步写（recover 包裹，永不阻塞情节返回）。
- **C2b（月报 CLI + 配置定价，done，commit 88b452d）** — `Prices.CostUSD`（精确→最长子串匹配，确定性 tie-break，空模型/空 key 不定价）+ `ByModelSince`；`config.EconomyConfig.Prices`（leaf）；`daimon costs [--since DUR]` 按模型出 token（恒显）+ $（仅定价模型，未定价脚注，TOTAL 只累加已定价）。无硬编码费率。
- **C2c（activity-class 线程化 + by-class 报表，done，commit d83c249）** — 纯加性穿字段：`CognitiveRequest.ActivityClass`（runKernel="chat"；`RunInternalEpisode` 新增 activityClass 参，heart_dispatch cognize 传 `ev.Kind`）→`EpisodeCost.ActivityClass`→`economy.Entry.ActivityClass`（列+idx 早在 034）。`economy.ByClassSince`（GROUP BY activity_class）+ `daimon costs` by-class 表（仅 token，空=`(unclassified)`，脚注指向 by-model 的 $）。Codex 审无 blocker/high/med，1 LOW（by-class tokens-only 提示）已修。
- **C2d（per-class 美元归因，done，commit 8d28c48）** — `economy.ByClassModelSince`（GROUP BY activity_class, model）取代 tokens-only `ByClassSince`；`cmd/daimon foldClassCosts` 按 class 折叠、每 model 子行各自费率定价累加，任一 model 未定价→该 class COST "—"（不完整非低估），output 降序+class 升序确定排序。by-class 表加 COST 列。实跑验证（chat 跨 opus+haiku+未定价 gpt-4o 正确定价并标 "—"）。Codex 审无 blocker/high/med，4 LOW（全测试覆盖）修。
- **C2e-1（ROI-by-class 只读报表，done，commit 6584baa；Codex 跨厂审 2 轮）** — `daimon costs` 加 ROI-by-class 段：value 代理=clean-outcome 率（情节零 tool 失败且所有受治理行动已 verified=J11+J12 信号）。costs 与 journal 同一 SQLite DB→按 episode_id join。`internal/world/outcome_quality.go`：`OutcomeQuality` 枚举(Clean/ToolFailures/UnverifiedActions/Salvaged/Failed)+`ClassifyOutcome(detail,summary)`(world 拥有 detail 编码契约+failEpisode 标记)+`OutcomeQualityForEpisodes(ids)`(按**规范主键** `journal_outcome_<id>` 查,非 kind+episode_id→stray outcome 行不污染)。`economy.EpisodeClassCostSince`(每情节 class+tokens,成本侧)。`cmd foldROI`(纯函数把 clean 数叠加到 per-class 成本脊柱)→ROI 表(CLEAN/CLEAN%/COST/CLEAN-per-$)。**纯加性只读零行为变更**;distill 仍有自己内联排除逻辑(合并到 world.ClassifyOutcome 为独立 tidy,有意不进只读报表切片——distill 是 §706 敏感路径)。**TestFoldROI 抓真零值 bug**(OutcomeClean=iota 0→缺失 quality 的 episode 默认算 clean 虚增 value;改 presence 检查)。**Codex 抓第二个**(按 kind+episode_id 而非规范 PK→stray 同情节 outcome 行可乱序覆盖 map;改规范 id+回归测试)。实跑 binary 验证(chat 3 情节/2 clean/67%;heartbeat 2/0 clean)。make test 32 pkg 绿。
- **C2e-2（cost/ROI throttle advisor，observe-only，done，commit 32e8bab；Codex 跨厂审 2 轮）** — 基于 C2e-1 的 per-class ROI 标记超预算/低价值 class。**纯 advisory 零控制流变更**:只在 `daimon costs` 打印推荐,绝不自动降级/限流;enforcement(down-routing/降频)是后续 gated 切片(task #15)。`config.ThrottleConfig{PerClassBudgetUSD,MinCleanRate,MinEpisodes}`(validate 校验 budget≥0/clean 率 0..1/episodes≥0,越界返错防误导推荐)。`economy.ThrottlePolicy.Evaluate([]ClassValue)[]ThrottleAdvice`(纯):OverBudget 仅 priced 且 USD>budget(成本不完整绝不触发预算行动);LowValue 仅 clean 率<min ∧ episodes≥MinEpisodes(不罚小样本);未设阈值 flag 0。`cmd` ROI 表后映射→Evaluate→打印推荐表(或"all within thresholds"),unpriced class COST 显"—"同 ROI 表。**Codex 抓 2 真**:(Med)unpriced class flag low-value 误显 $cost→Priced 透传 ThrottleAdvice 显"—";(Med)阈值无边界校验→validate 加界+测试。实跑 binary 验证(chat"over budget+low value" $117>$50&67%<80%;heartbeat"low value" 0%<80%)。make test 32 pkg 绿。
- **C2e-3（throttle enforcement，待实施，task #15）** — gated 控制流:flag 的 class 自治事件 down-route(更便宜模型/Cognize→Reflex/Ignore 安全处)或降 timer 频,**绝不 WakeUser**,可逆+可观测,默认 off。attention Verdict 现无 model 字段→需 model-hint 或 heart 侧 cadence 杠杆。Codex 审。

---

## 13. 迁移号分配（修订）

蓝图原规划号与实际已用号冲突，按实际续号：

| 表 | 迁移号 | 增量 |
|---|---|---|
| follow_ups | 031 | P0-A |
| proposals | 033 | P3-J（032 已被 world_fts 占用，proposals 续号 033） |
| costs | 034 | P3-K |
| journal.parent_episode_id（子情节父链） | 035 | §4.3 slice 2 |
| drop task_checkpoints | 036 | P3-J（重组取代 checkpoint 后） |

> 已用：027 world_model、028 action_ledger、029 events、030 attention_feedback、031 follow_ups、032 world_fts、033 proposals。

---

## 14. 工作流约定

- 每增量：写最小 task 说明 → 实现（自包含逻辑委托 Codex workspace-write，surgical 接线 Claude 直改）→ Codex 独立审查（安全敏感增量强制）→ `make build-bin && make vet && make test-short` → commit `refound(pX-Y): ...`。
- 安全敏感增量（P0-B 门控、P1-C 沙箱、P1-D 撤回、P2-H 绞杀）：Codex 反谄媚审查竞态/边界/绕过后再合。
- 每阶段末：删对应 legacy 路径（绞杀纪律），不留长期双轨。
- 北极星指标随 P0-A（指标6）、P1-E（指标7）、P3（1/2/3/4/5）逐步可观测。
