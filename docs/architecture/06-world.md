# 06 · world — 世界模型

> 包路径 `internal/world` · 蓝图 §4.4 · **唯一事实源**

## 职责

代理的传记、当下与记忆的唯一权威来源。宪法第 1 条「状态在外」的物理载体——一切持久信息住在这里，上下文是从它重组出来的缓存。验收：杀掉一切运行时内存，仅凭 `~/.daimon` 重启，代理对"我是谁/在帮你做什么/上周发生了什么"三问的回答与重启前一致。

## 三层结构

```
~/.daimon/world/
  identity/                 身份层（月级变化）—— 代理可自编辑的 markdown，git 仓库
    digest.md               sleep 维护的压缩摘要，Composer 直接用
  values/                   价值层（见 07-values，物理同住）
                            ─────────────────────────────
SQLite (data/daimon.db):
  commitments               事项层（天级变化）：项目/承诺/截止/观察/例程
  journal                   日志层（append-only）：情节 outcome + 决策 + 事实
```

### 身份层（文件）

```go
type Identity struct { Dir string }
func (i Identity) Digest() string  // 读 <dir>/digest.md
```

身份层是 git 仓库化的 markdown（`daimon world history/revert` 可回滚）。`digest.md` 由 sleep 的 `DigestJob` 重算，Composer 注入。

### 事项层（commitments 表）

```go
type Commitment struct {
    ID, Kind, Title, Body, State, Horizon, SourceEpisode, DueAt string
    CreatedAt, UpdatedAt string
}
```

- `Kind`：`project | promise | deadline | watch | routine`
- `State`：`active | waiting | done | dropped`
- 检索默认排序：有 due 的在前、due 升序、updated 降序、title 升序。

### 日志层（journal 表，append-only）

```go
type JournalEntry struct {
    ID, EpisodeID, Kind, Summary, Detail, OccurredAt, RollupID string
}
```

`Kind` 取值与用途：

| kind | 来源 | 说明 |
|---|---|---|
| `outcome` | 情节交账 | id 为 `journal_outcome_<episodeID>`（确定性，幂等 claim）|
| `decision` | 决策记录 / distill 候选 | distill 候选 summary 前缀 `"distill candidate: "` |
| `correction` | 用户纠正 | 关联回归集（replay） |
| `fact` | `fact.upsert` | 可检索事实，FTS5 索引；reconcile 处理矛盾/去重 |
| `rollup` | sleep rollup | 折叠旧条目的摘要 |

## 核心接口

### Mutation — Outcome.WorldWrites 的元素

```go
type Mutation struct {
    Op     string          // commitment.create | commitment.update | journal.append | fact.upsert | fact.delete
    Target string          // 更新/删除的目标 id
    Body   json.RawMessage // 操作载荷
}
```

`applyMutations`（`world.go:540`）按 Op 分派。未知 Op 报错（整事务回滚）。

### Apply / ApplyOutcome — 事务性交账

```go
func (s *Store) Apply(ctx, episodeID string, muts []Mutation) error          // 纯 mutation
func (s *Store) ApplyOutcome(ctx, episodeID string, muts, summary, meta) error // 情节交账
```

`ApplyOutcome` 是情节退出契约的落点（[04-episode.md](04-episode.md)）：

1. **先 claim outcome marker**：`claimOutcomeJournal` 用 `INSERT OR IGNORE` 写确定性 id `journal_outcome_<episodeID>`。返回 `claimed=false` 意味着该情节 outcome 已记录过（崩溃重投 / 并发双发），**mutation 不再应用**——把幂等保证落到事实层，而非仅内核层。这关键，因为部分 mutation（如 `commitment.create`）非幂等。
2. claim 成功才 `applyMutations`。
3. 一个事务，全成或全回滚。

`OutcomeMeta` 写入 outcome 条目的 detail（按优先级单槽：salvaged > tool_failures > unverified_actions）+ 专列 `parent_episode_id`（迁移 035）+ `value_created_usd`（迁移 038）：

```go
type OutcomeMeta struct {
    Salvaged          bool    // 框架兜底（模型没调 episode_close）
    ToolFailures      int     // 工具调用出错数（干净执行信号）
    UnverifiedActions int     // 未客观验证的受治理行动数（§4.8 distill 候选）
    ParentEpisodeID   string  // 子情节的父链
    ValueCreatedUSD   float64 // 自报价值
}
```

### Retrieve — 跨层混合检索

```go
type Query struct { Text string; Limit int; Kinds []string }
type Hit struct { Source, ID, Kind, Title, Text, OccurredAt string; Score float64 }
func (s *Store) Retrieve(ctx, q Query) ([]Hit, error)
```

`retrieve.go` 实现 **FTS5（BM25）+ 向量 RRF 融合**（k=60），跨 journal + commitments 检索。FTS5 不可用时降级 LIKE，malformed query 再降级。这是从 legacy `memory` 包整体征用的检索引擎，索引范围扩到三层。Composer 用它供给"相关记忆"段（[04-episode.md](04-episode.md)）。

### 事实安全：upsert / delete 的 kind 守卫

`upsertFact` / `deleteFact` 都带 `kind='fact'` 守卫：模型在 `episode_close` 的 `fact.upsert` WorldWrite 里能放任意 id，若指向非 fact 行（outcome/decision/correction 是 append-only 审计），delete 匹配不到、insert 主键碰撞、整事务失败——**fail-closed，审计行永不被破坏**（不变量 1/4）。

### Rollup — 非破坏性折叠

```go
func (s *Store) Rollup(ctx, summary string, foldedIDs []string) (rollupID, error)
```

sleep 的 `RollupJob` 把旧 journal 条目折叠成一条 `rollup` 摘要，给每个被折叠条目盖 `rollup_id`（**标记而非删除，detail 可追溯**）。UPDATE 里重新断言资格（只折 `rollup_id=''` 且非 fact/rollup 的行），资格在选择后变了则整体回滚——摘要绝不声称覆盖了没盖到的条目。facts（如自摘要单例）与既有 rollup 永不折叠。

### 窗口无关查询

`ListOutcomes` / `ListDistillCandidatesWithoutDraft` / `ListFacts` 在 SQL 里按 kind 过滤，**不受 journal 增长影响**——distill 检测要跨多情节看重复模式，固定的全 kind 切片会让累积的 decision/fact 把 outcome 挤出可见窗口。`ListDistillCandidatesWithoutDraft` 还 `NOT EXISTS` 排除已有 `distill_draft_<id>` 标记的候选，最老优先，消除饥饿。这些前缀（`"distill candidate: "` / `"distill_draft_"`）是与 `internal/sleep` 的磁盘契约。

## 自编辑工具

模型通过三个工具直接读写世界（Letta/MemGPT 路线，`subsystem_tool.go:67-69` 注册）：

- `world_read`：读 identity digest / commitments / journal。
- `commitment`：create/update/list 事项（受治理，Reversible）。
- `world_edit`：写 identity markdown（受治理，git 仓库化即天然 undo，`daimon world revert`）。

## 数据

`commitments` / `journal`（迁移 027）；FTS5 `journal_fts` / `commitments_fts`（迁移 032）；journal 扩列 `parent_episode_id`（035）、`value_created_usd`（038）。详见 [19-data-layer.md](19-data-layer.md)。

## 跨包接缝

- **← episode**：`ApplyOutcome` 是情节交账落点；`Retrieve`/`CommitmentsDigest`/`Identity.Digest` 供 Composer 组装。
- **← sleep**：reconcile（事实和解）、rollup（折叠）、digest（重算 identity）、distill（扫候选）全读写 world。
- **← tool**：`world_read`/`world_edit`/`commitment` 工具。
- **→ vcs**：identity 文件改动经 `vcs.Commit` 落 git。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 三层 + 唯一事实源 | 宪法 1「状态在外」 | 丢上下文不丢事实；连续性测试 |
| claim-first 幂等 | heart at-least-once | 崩溃重投不双应用非幂等 mutation |
| fact 守卫 + append-only 审计 | 不变量 1/4 | 模型给的 id 不可信，审计行 fail-closed 保护 |
| rollup 标记而非删除 | 可追溯 | 折叠后 detail 仍可恢复 |
| 事实经交账 WorldWrites 进入 | 宪法 7 | 退役 per-message LLM 抽取，由情节交账自报 + sleep 兜底 |

下一篇：[07-values.md](07-values.md) — 价值模型。
