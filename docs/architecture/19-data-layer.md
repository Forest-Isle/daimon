# 19 · 数据层 — SQLite、迁移、磁盘布局

> 包路径 `internal/store` · 迁移 `internal/store/migrations/*.sql` · 蓝图 §5

## 存储原则

- **单一 SQLite 文件**：`~/.daimon/daimon.db`（legacy `~/.ironclaw/ironclaw.db`）。
- **WAL 模式 + 单写**：world 是唯一真相（宪法第 1 条），单写者免并发冲突。
- **嵌入迁移**：`//go:embed migrations/*.sql`，DB 打开时**字母序自动应用**（`store` 包）。
- **FTS5 优雅降级**：启动探测 FTS5，不可用回退 LIKE。
- **store 保持 clock-free**：时间戳由调用方传入（如 `costs.OccurredAt`），便于确定性测试。

## 迁移时间线（35 文件，编号至 041）

编号有跳号（002-005/007/011 缺）= 历史 squash/drop。**三个 DROP 迁移是绞杀者改造的拆除现场**——旧 IronClaw 子系统被逐表移除：

| 迁移 | 动作 | 拆除对象 |
|---|---|---|
| `013_cleanup_legacy` | DROP | `memories` `cognitive_cycles` `memory_facts*` `fact_embeddings*` `agent_traces` |
| `022_drop_rl_tables` | DROP | `rl_bandit_arms` `rl_*`（强化学习子系统）|
| `024_drop_knowledge_tables` | DROP | `kb_*`（知识库）`kg_*`（知识图谱）|

新世界模型从 `027` 起建立。下表按子系统归组现役表：

## 表清单（按子系统）

### 会话 / 任务（001/016-021/025）

| 表 | 迁移 | 职责 |
|---|---|---|
| `sessions` | 001 +016(parent)+017(summary) | 会话；父链 `idx_sessions_parent` + 增量摘要 |
| `messages` | 001 | 消息历史 |
| `scheduled_tasks` | 001 / 025 | 定时任务 |
| `tool_log` | 001 | 工具调用日志 |
| `task_checkpoints` | 018 | 任务 checkpoint |
| `task_ledger` | 019 | 任务账本（state/parent/kind 索引）|
| `execution_events` | 020 | 执行事件 |
| `agent_replays` / `agent_replay_events` | 021 | 回放录制（DB 侧）|
| `sidechain_entries` | 015 | 子代理链（agent_id/chain_id）|

### 世界模型（027/032/035 — [06-world.md](06-world.md)）

```sql
-- 027_world_model.sql
commitments(id, kind, title, body, state, due_at, horizon, source_episode, ...)
  -- kind: project|promise|deadline|watch|routine; state: active|waiting|done|dropped
journal(id, episode_id, kind, summary, detail, occurred_at, rollup_id)
  -- kind: outcome|decision|correction|fact
```

- `032_world_fts`：`journal_fts` / `commitments_fts`（FTS5 虚表）+ ai/ad/au 触发器同步。
- `035_journal_parent`：`parent_episode_id` 列 + 索引（父子情节链）。
- `038_value_created`：journal 扩 `value_created_usd` 列（[12-economy.md](12-economy.md)）。
- journal 还含 `fact`（身份事实 upsert/delete kind 守卫）。

### 行动层（028 — [08-action.md](08-action.md)）

```sql
-- 028_action_ledger.sql
trust_ledger(action_class, context_key, attempts, verified_ok, corrected, level, ...)
  -- PK(action_class, context_key); level: 0..3 trust 等级
undo_journal(receipt_id PK, tool_name, undo_spec, created_at, expires_at, undone_at)
holds(id PK, receipt_id, tool_name, payload, execute_at, state, ...)
  -- state: pending|executing|executed|failed|recalled
```

- `040_undo_episode`：undo_journal 扩 `episode_id` 列（episode-linked undo）。

### 注意力 / 心脏（029/030/031）

| 表 | 迁移 | 职责 |
|---|---|---|
| `events` | 029 | 事件心脏；`idx_events_unrouted` 支撑崩溃恢复扫 unrouted（[02-heart.md](02-heart.md)）|
| `attention_feedback` | 030 | 路由反馈回流（[03-attention.md](03-attention.md)）|
| `follow_ups` | 031 | 续跑队列（due 索引）|

### 提案 / 经济（033/034/036/037/039）

| 表 | 迁移 | 职责 |
|---|---|---|
| `proposals` | 033 +036(delivered_at)+039(action_kind/action_ref) | 提案队列（[10-proposals.md](10-proposals.md)）|
| `costs` | 034 | 成本台账（occurred/class 索引，[12-economy.md](12-economy.md)）|
| `regression_corrections` | 037 | 回归纠正（按录制 session id 键控，[11-replay.md](11-replay.md)）|

### 工作流 / 记忆 / 邮件（006-012/023/026/041）

| 表 | 迁移 | 职责 |
|---|---|---|
| `workflow_step_cache` | 026 | workflow replay cache（[17-skills-workflow.md](17-skills-workflow.md)）|
| `memory_index` / `memory_fts` / `memory_embeddings` | 006 +023(temporal) | 绞杀残留检索（[18-supporting.md](18-supporting.md)）|
| `fact_access_log` / `fact_access_stats` | 008 | 事实访问统计 |
| `memory_audit_log` | 012 | 记忆审计 |
| `permission_audit_log` | 014 | 权限审计 |
| `reflection_tracker_state` | 010 | 反思追踪 |
| `mail_state` | 041 | IMAP 邮件源 UID 游标（mail 感官源）|

## ~/.daimon 磁盘布局

```
~/.daimon/                  ← 整目录 git 化（vcs，自我修改可逆）
├── daimon.db               单一 SQLite（WAL）
├── identity.md             身份文件（world_edit 写，git 可 revert）
├── values.md               价值条目（[07-values.md]）
├── attention/
│   └── rules.yaml          路由规则（synthesize 追加，git 可 revert）
├── skills/                 active 技能（被加载）
├── skills-staging/         蒸馏草稿（inert，绝不加载）
└── replays/                telemetry JSONL 录制语料
```

`appdir.go` 解析路径（`BaseDir`/`SkillsDir`/`SkillsStagingDir`）。git 化让身份/规则/技能三类自我修改各有 revert（[20-security-governance.md](20-security-governance.md)）。

## 幂等机制

- **journal outcome 幂等**：`ApplyOutcome` 用 `journal_outcome_<episodeID>` 哨兵 id，重投跳过（[06-world.md](06-world.md)）。
- **events 去重**：`UNIQUE(source, dedup_key)` INSERT OR IGNORE（[02-heart.md](02-heart.md)）。
- **hold CAS**：`ClaimHold` pending→executing 原子转移，绝不双发。
- **FTS5 触发器**：journal/commitments 增删改自动同步虚表。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 单 SQLite + WAL 单写 | 宪法 1/6 | world 唯一真相，本地主权，无外部 DB |
| 嵌入迁移字母序自动应用 | 可复现 | 启动即 schema 最新，无手工迁移 |
| DROP 迁移逐表拆旧 | 绞杀者 | 新路径跑通后移除 legacy 表 |
| store clock-free | 可测 | 时间由调用方传，确定性断言 |

下一篇：[20-security-governance.md](20-security-governance.md) — 安全与治理。
