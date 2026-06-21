# 02 · heart — 事件心脏

> 包路径 `internal/heart` · 蓝图 §4.1

## 职责

把世界的一切变化——聊天消息、邮件、文件改动、定时器——统一为**单一持久化事件流**，并把每个事件**恰好一次**投递给处理器。代理的感官。

设计承重句（源码包注释）：**先落库后路由（persist before routing）**。这让事件流可审计、可崩溃恢复——重启时重放"已存但未路由"的事件即可。

## 核心类型

```go
// internal/heart/heart.go
type Event struct {
    ID         string
    Source     string  // "mail" | "telegram" | "timer" | "fs" | "internal" | ...
    Kind       string  // "mail.received" | "message" | "internal.followup" | ...
    Payload    string  // JSON 上下文
    OccurredAt string
    DedupKey   string  // 源内幂等键（邮件 Message-ID、telegram update_id）
}

type Source interface {
    Name() string
    Run(ctx context.Context, emit func(Event) error) error  // 长驻，断线自重连
}

type Handler func(ctx context.Context, ev Event) string  // 返回 verdict 字符串供持久化

type Heart struct { store *Store; handler Handler; sources []Source }
```

`Source.Run` 的 `emit` 返回 error 是个关键设计：emit 失败（事件没能落库）时通知源，让"emit 时修改自身状态"的源（如 followup 标记为已触发）能避免丢失从未进入事件流的工作。

## 关键流程

### 投递主环（`Heart.Run` → `process` → `deliver`）

```
Run:
  recover(ctx)                       崩溃恢复：先重放未路由 backlog
  for each source: go src.Run(ctx, emit)   每源一 goroutine
    emit(ev) = process(ctx, ev)

process(ev):
  Persist(ev)  ──▶ inserted?
                    false → return（重复，已处理过）
                    true  → deliver(ev)

deliver(ev):
  verdict = handler(ev)              attention 返回 action 字符串
  store.MarkRouted(ev.ID, verdict)   回填路由结果
```

三条不变量在此体现：

1. **去重**：`Persist` 用 `INSERT OR IGNORE` on `UNIQUE(source, dedup_key)`，重复事件直接丢弃，`inserted=false`。
2. **崩溃恢复**：`recover` 在启动时 `Unrouted()`（`SELECT WHERE routed_at IS NULL`）重放——崩溃发生在 persist 与 deliver 之间的事件全部补投。
3. **at-least-once**：路由失败不致命（事件仍标 routed，handler 自管重试），但持久事件留存供审计。幂等靠 `dedup_key` + 情节侧的 Receipt/outcome 查重兜底。

### 两种入口：`process` vs `Record`

| 入口 | 用途 | 是否投递 handler | verdict |
|---|---|---|---|
| `process` | 自治事件（mail/fs/timer/followup）| 是，经 attention 路由 | handler 返回值 |
| `Record` | 聊天入口（处理由 channel goroutine 同步拥有）| **否** | 立即标 `"recorded"` |

`Record`（`heart.go:117`）只为聊天消息提供"统一、去重、可崩溃审计的记录"，不负责分发——聊天的处理在 `gateway.handleInbound` 里由 channel goroutine 同步跑完。它用 `PersistRouted` 在一条语句里存为"已路由"，**没有未路由窗口**，崩溃恢复不会把崩在半途的聊天回合再答一次（匹配 legacy 行为）。返回 `inserted=false` 时调用方跳过重发。

## 数据

`events` 表（迁移 029）：

```sql
CREATE TABLE events (
    id TEXT PRIMARY KEY, source TEXT, kind TEXT, payload TEXT,
    occurred_at ..., dedup_key TEXT,
    routed_at ..., verdict TEXT,        -- 路由结果回填
    UNIQUE(source, dedup_key)           -- 去重
);
-- 部分索引 on routed_at IS NULL，加速崩溃恢复扫 backlog
```

## 感官源（Source 实现）

| Source | 文件 | Kind | 说明 |
|---|---|---|---|
| `TimerSource` | `timer.go` | `internal.heartbeat` / `internal.daily_brief` / `internal.health` / `internal.sleep` | 按配置 interval（分钟）发内部事件，驱动早报/健康/整固/idle 检测 |
| `FollowUpSource` | `followup.go` | `internal.followup` | 轮询 `follow_ups` 表，到点把情节种下的 timer 续跑事件 emit 出来 |
| `MailSource` | `mail.go` | `mail.received` | IMAP 轮询收件箱，高水位追踪（`mail_state` 表，迁移 041），dedup_key=Message-ID |
| `FSSource` | `fs.go` | `fs.created` / `fs.modified` / `fs.removed` / `fs.renamed` | fsnotify 监视配置目录，dedup_key=`path\|kind\|unix-second`（坍缩编辑器 save-burst） |
| 聊天（telegram/tui）| 经 `gateway.handleInbound` → `Record` | `message` | 不走 `Source.Run`，而是 inbound 时调 `RecordChatEvent` 落统一流 |

各源在 `gateway.New()` 的 heart 启用分支里按配置注册（`gateway.go:236-282`）：`fs_watch_dirs` 非空注册 FSSource；`mail.enabled && imap_host != ""` 注册 MailSource；各 timer 按对应 interval 分钟数 >0 注册。

### FollowUp 续跑机制

情节交账时若有 `FollowUp{Kind:"timer"}`，经 `FollowUpPlanter` 写入 `follow_ups` 表（迁移 031）。`FollowUpSource` 轮询到期项，emit `internal.followup` 事件，`goalForEvent` 用 payload 作 goal，重新点燃情节。**续跑靠从世界模型重组，不靠 checkpoint 反序列化**（宪法第 1 条「状态在外」）——见 [04-episode.md](04-episode.md) 的长任务处理。

> 已知限制：timer follow-up 写 heart 队列是独立 store，无法并入 world 事务 → best-effort + 错误日志。进度本身已随 outcome/commitment 持久化，丢失的仅是续跑便利。

## 跨包接缝

- **→ attention**：`Heart.handler` 在 gateway 里就是 `newEventDispatcher().handle`，内部调 `attention.Chain.Route`。心脏不解释 verdict 字符串，只持久化。
- **← episode**：`FollowUpPlanter` 由 gateway 用 `follow_ups` store 适配后注入 `EpisodeRunner.SetPlanter`。
- **gateway 生命周期**：`heart.Start` 在 channels 之后启动（其 WakeUser 路径要能触达渠道），run 循环活在 serve ctx 上，shutdown 时随 ctx 取消退出。

## 设计取舍

- **为什么先落库后路由**：崩溃恢复的代价是一次 DB 写，换来的是"杀进程重启不丢事件、不重复处理"。蓝图验收：邮件/定时/聊天三源并发一周无丢失（对账 events 表与源侧计数）。
- **为什么聊天不走 attention**：聊天人在等回复，默认就该 Cognize，过 attention 是浪费且增延迟。聊天经 `Record` 只取其"统一审计流 + 去重"价值。
- **为什么 emit 返回 error**：让源能在"事件没落库"时不误标自身状态（followup 不会标已触发却其实没排队）。

下一篇：[03-attention.md](03-attention.md) — 注意力路由。
