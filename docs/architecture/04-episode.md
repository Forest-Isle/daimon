# 04 · episode — 情节内核

> 包路径 `internal/episode` · 蓝图 §4.3 · **整个重组范式的承重墙**

## 职责

认知的执行单元。**组装上下文 → 裸 ReAct → 强制交账 → 丢弃上下文。** 一个情节是无状态的：每次从世界模型新鲜重组上下文，结束时把学到的东西写回世界模型，然后扔掉上下文（宪法第 1 条「状态在外」）。

`Runner` 实现 `agent.CognitiveKernel` 接口（见 [01-architecture.md](01-architecture.md) 跨包接缝）。工具执行与回放录制委托给 agent runtime（经 `CognitiveRequest.Invoke`），所以情节和 legacy 路径共享同一套治理。

## 核心类型

### Outcome — 退出契约（schema 强制）

```go
// internal/episode/episode.go
type Outcome struct {
    Status          string           `json:"status"`        // done | blocked | handed_off
    Summary         string           `json:"summary"`       // ≤500 字，写入 journal
    WorldWrites     []world.Mutation `json:"world_writes"`  // 学到的事实/事项变更
    Receipts        []string         `json:"receipts"`      // 本情节产生的行动 receipt id
    FollowUps       []FollowUp       `json:"follow_ups"`    // timer | watch | check
    OpenQuestion    *string          `json:"open_question"` // blocked 时：卡在哪个待用户输入
    ValueCreatedUSD float64          `json:"value_created_usd"` // 可选自报价值
    Salvaged        bool             `json:"-"`             // 框架置位（非模型字段）
}

type FollowUp struct { Kind, Detail, Goal string } // kind: timer | watch | check
```

### Runner — 认知内核

```go
type Runner struct {
    provider mind.Provider     // 认知引擎
    world    *world.Store      // 唯一事实源
    identity *world.Identity   // 身份层
    bus      agent.EventBus    // 发布 ProviderExchange/TurnClosed/EpisodeSalvaged
    planter  FollowUpPlanter   // timer 续跑（可选，nil 则丢弃 timer followup）
    values   valueDigester     // 价值观 digest 注入（可选）
    cost     CostRecorder      // 经济台账（可选，observational）
}
func (r *Runner) Execute(ctx, req agent.CognitiveRequest) (agent.CognitiveOutcome, error)
```

四个可选依赖（`SetPlanter`/`SetValues`/`SetCostRecorder` + 构造时的 bus）全部 nil-safe——缺失则该侧关闭，二进制行为不变。这是"渐进装配、零行为变更回退"的纪律。

## Composer：上下文新鲜组装

`composeSystem`（`composer.go:26`）每次认知调用从零构建 system prompt，**顺序即 prompt 布局**：

| 段 | 来源 | 缺失时 |
|---|---|---|
| 人格 + 宪法摘要 | 静态 `constitutionSummary` | 必有 |
| Persona | `req.Persona`（Soul.md） | 跳过 |
| Rules | `req.Rules`（Memory.md） | 跳过 |
| Identity Digest | `world.Identity.Digest()`（sleep 维护的 digest.md） | "Not yet configured." |
| Active Commitments | `world.CommitmentsDigest(ctx, "")` | "None." |
| Values | `valueDigester.Digest()`（高置信条目） | 跳过 |
| Relevant Memories | `world.Retrieve(Query{Text:goal, Limit:6})` | 回退 `req.Memories`（legacy） |
| Goal | `req.Goal` + **必须调 episode_close** 指令 | "Respond to the trigger event." |

`relevantMemories`（`composer.go:72`）是绞杀者开关：`world.Retrieve` 是主源，`req.Memories`（legacy memory 包注入）仅作回退。回放 harness 确认组装质量后，legacy 路径与 `req.Memories` 可删。

消息列表（transcript）由调用方单独提供，不进 system prompt——保持缓存边界在静态段后。

## 关键流程：Execute

```
Execute(req):
  0. provider nil? → failed
  1. 幂等检查：req.EpisodeID 已有 outcome？→ 跳过（崩溃重投安全）
                EpisodeID 空 → 生成新 ep_<time><rand>
  2. defer panic 恢复 → failEpisode（交账强制）
  3. defer recordCost（累计 Usage → 经济台账，任何退出路径都记一行）
  4. ctx 装 ActionVerification collector（统计 governed/verified 行动）
  5. ctx 装 EpisodeIDToCtx（父子情节链）
  6. system = composeSystem(...)
  7. toolDefs = req.ToolDefs + episode_close（保留工具）
  8. ── 裸 ReAct 环（≤20 轮）──
       creq = {Model, System, Messages, Tools}
       fullText, toolCalls, stop, usage, err = streamCompletion(provider, creq)
       used.Add(usage); publishExchange(...)  → 回放录制
       err? → failEpisode（交账强制）
       无 toolCalls？→ 收敛
           已发提醒 or 最后一轮？→ break（去 salvage）
           否则注入"必须调 episode_close"提醒，continue
       for tc in toolCalls:
           tc == episode_close？
               parseOutcome(tc.Input)  → schema 校验
               失败 → 回灌 rejection，模型重试
               成功 → return close(...)   ★ 正常退出
           其它工具 → req.Invoke(ctx, iter, tc)（经拦截链）→ 回灌结果
                       isErr → toolFailures++
  9. 环耗尽未交账 → out = salvage(...)（兜底）
 10. return close(...)
```

### 退出契约流程（宪法第 3 条「交账强制」的承重实现）

`episode_close` 是注册的保留工具（`episodeCloseToolDefinition`），schema 即 `Outcome`。四条路径都保证落 journal：

1. **正常交账**：模型调 `episode_close` → `parseOutcome` schema 校验 → `close`。
2. **校验拒绝**：status 非 `done/blocked/handed_off`、summary 空或 >500 字、value 负 → 回灌 rejection，模型重试。
3. **salvage 兜底**：环耗尽仍未交账 → `salvage`：先让 provider JSON-only 提取 Outcome，失败则 `inferOutcomeFromTranscript` 启发式（扫 transcript 找 blocked/waiting/handed off 关键词），**标 `Salvaged=true`**（北极星指标 6 盯死此比率）。
4. **失败兜底**：provider 流错误 / panic / world 写失败 → `failEpisode` 记一条 blocked outcome（status="failed"），summary 含失败标记。

`parseOutcome`（`episode.go:423`）是 schema 强制的执行点——Outcome 不是"非空即可"，status 必须在枚举内，否则模型重试。这正是宪法第 7 条「不替模型思考」的例外 1：**不替模型思考，但强制模型交账。**

### close：应用 Outcome

`close`（`episode.go:281`）：

1. **展开 FollowUps**：`watch`/`check` → `commitment.create` mutation（与 outcome 事务性同落）；`timer` → 收集后经 `planter.Plant` 写 heart followup 队列（best-effort，独立 store 不能并入 world 事务）。未知 kind 丢弃并告警。
2. **world.ApplyOutcome**：事务性写 WorldWrites + journal outcome 条目，附 `OutcomeMeta{Salvaged, ToolFailures, UnverifiedActions, ParentEpisodeID, ValueCreatedUSD}`，按 episodeID 幂等（`journal_outcome_<episodeID>` claim）。写失败 → `failEpisode`（重应用无写的 outcome，summary 保留失败标记在前以防截断）。
3. **发布事件**：Salvaged 则发 `EpisodeSalvaged`；总是发 `TurnClosed`（reply 优先用 lastReply，空则用 summary）。

### 长任务：handed_off

情节不追求一口气做完。预算到顶 → `Status=handed_off` + WorldWrites 记进度 + FollowUp 种续跑 timer → **下一个情节从世界模型重组继续**。续跑靠重组，不靠 checkpoint 反序列化（`task_checkpoints` 表已退役）。

## 父子情节链（§4.3 parent linkage）

子代理 Spawn 来自工具调用时，读 `agent.EpisodeIDFromCtx(ctx)`（由 `Execute` 第 5 步 `EpisodeIDToCtx` 注入）得到父 episodeID → 填入子情节 `CognitiveRequest.ParentEpisodeID` → `ApplyOutcome` 记入 `OutcomeMeta.ParentEpisodeID` → journal `parent_episode_id` 列（迁移 035）。顶层情节该列为空，零特例。父子情节树由此可从 journal 重建。

## 行动验证信号（§4.8 distill 候选）

`ActionVerification` collector 统计本情节 governed（受治理）与 verified（客观验证通过）的行动数。`unverifiedActionCount = governed - verified` 写入 `OutcomeMeta.UnverifiedActions`。0 表示该情节的每个受治理行动都赢得了客观信任（或没采取行动）。distill 作业据此 + `ToolFailures` + `Salvaged` 排除"不干净"的情节，不让带病模式被廉价复制。

## 数据

写入 `world` 的 `journal`（kind=outcome）与 `commitments` 表（迁移 027/035/038）；cost 写 `costs` 表（迁移 034）。详见 [06-world.md](06-world.md) 与 [19-data-layer.md](19-data-layer.md)。

## 跨包接缝

- **← agent**：实现 `CognitiveKernel`；`req.Invoke` 是 agent 的工具调用装配（经拦截链/审批/hook/录制）。
- **→ world**：`ApplyOutcome` 事务交账；`Retrieve`/`CommitmentsDigest` 组装上下文。
- **→ heart**：`FollowUpPlanter` 种续跑 timer。
- **→ mind**：`streamCompletion` 调 `provider.Stream`；salvage 调 `provider.Complete`。
- **→ telemetry**（经 EventBus）：`ProviderExchange`/`TurnClosed`/`EpisodeSalvaged` 录制进 replays/。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 无状态情节 + 重组续跑 | 宪法 1「状态在外」 | 丢弃上下文不丢失事实；崩溃恢复靠世界模型而非反序列化 |
| episode_close schema 强制 | 宪法 3「交账强制」（例外 1）| 不交账则架构失忆；salvage 兜底但打标，指标盯死 |
| 内核零子系统依赖 | 宪法 2「换脑无感」 | 一切资产由 agent 注入 request，换模型只动 mind |
| recordCost 严格 observational | 宪法 3 | 成本记录失败/panic 绝不影响 outcome |
| 幂等 ApplyOutcome | heart at-least-once | 崩溃重投不双记 |

蓝图验收：交账率 >98%（salvaged <2%）；同一事件 + 同一世界模型快照重组的上下文逐字节可复现（回放前提）；并行 10 情节互不串扰。

下一篇：[05-mind.md](05-mind.md) — 模型层。
