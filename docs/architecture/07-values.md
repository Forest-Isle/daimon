# 07 · values — 价值模型

> 包路径 `internal/values` · 蓝图 §4.5

## 职责

显式、可溯源、可编辑的用户价值观。**自主行动的许可来源。** 当情节遇到无现有条目覆盖的价值权衡，行动层拒绝自主放行，情节 blocked 问用户一次，答案存成新条目——此后同类权衡不再问（ask-once）。

条目存为 markdown，住在 `<world>/values/<domain>/<slug>.md`，是 world git 仓的一部分、可手编辑。Store 维护内存索引供行动层 value gate（ask-once）与 episode Composer（高置信 digest）使用。

## 核心类型

```go
// internal/values/values.go
type Entry struct {
    ID         string       // v-<domain>-<slug>
    Domain     string       // 域：work | health | security | travel | ...
    Statement  string       // 价值陈述本身
    Confidence float64      // [0..1]，默认 0.8
    Provenance []Provenance // 来源：episode + date + 用户原话
    State      string       // active | drifting | retired
    Body       string       // frontmatter 后的自由 markdown
}

type Provenance struct { Episode, Date, Quote string }
```

### 条目格式（磁盘）

```markdown
---
id: v-travel-no-redeye
domain: travel
statement: 宁可贵 500 以内也不订红眼航班
confidence: 0.9
state: active
provenance:
  - episode: ep-xxxx
    date: "2026-03-14"
    quote: "原话片段"
---
（可选自由正文）
```

## Store 接口

```go
func NewStore(dir string) *Store
func (s *Store) Load(ctx) error                          // 遍历 root，解析 *.md，重建索引
func (s *Store) Add(ctx, e Entry) (Entry, error)         // 幂等 create/update，原地重写
func (s *Store) Lookup(domain string) (Entry, bool)      // 返回域内首个 active 条目
func (s *Store) MarkDrifting(ctx, id, reason) (Entry, bool, error)
func (s *Store) Digest() string                          // 高置信 active 条目（注入 prompt）
```

并发安全：行动 gate 读（Lookup）、Composer 读（Digest）、values 工具写（Add）由 `sync.RWMutex` 保护。

## 两个流程

### 1) ask-once（问一次）

`action.ValueGate` 是行动管线的头段（[08-action.md](08-action.md)）：

```go
// internal/action/value_gate.go
type ValueGate interface {
    Permit(ctx, class Class, contextKey string) (ref string, permitted bool)
}
```

流程（`action/interceptor.go:104`）：

```
governed && class != Reversible && gate != nil:
    ref, permitted = gate.Permit(class, contextKey)
    !permitted → valueBlockedResult（工具不执行）
```

`valueBlockedResult` 返回的是 error result，让模型看到并以 `blocked` + `open_question` 收尾：

> action blocked by value gate: a `<class>` `<tool>` action requires an explicit value decision... Close the episode with status "blocked" and an open_question asking the user to decide this tradeoff; once they answer, record it with the values tool so future actions in this domain are covered.

闭环：情节 blocked → OpenQuestion 经渠道问用户 → 用户答 → `values` 工具 `Add` 新条目 → FollowUp 续跑情节 → 同类权衡此后被 `Lookup` 覆盖，不再问。Reversible（低风险）行动豁免——可 undo，自由执行。

许可来源记在 receipt 的 `value_ref`（`value:<id>` / `trust:<level>` / `interactive`），使每个自主行动可追溯到许可它的决策（北极星指标 2 的审计基础）。

### 2) 漂移检测（sleep DriftJob）

sleep 的 `DriftJob`（[09-sleep.md](09-sleep.md)）扫 journal 中用户纠正与现有 active 条目的矛盾，矛盾则 `MarkDrifting`：

```go
func (s *Store) MarkDrifting(ctx, id, reason) (Entry, bool, error)
```

- `active → drifting`：drifting 条目不再授权自主行动（`Lookup` 只返 active）→ 下次该域自主行动重跑 ask-once，用户重新确认（或放弃）。
- **fail-safe**：误判只代价一次无害重问，绝不导致不安全行动。
- reason 作为 provenance note 追加（`episode: sleep:drift`）做审计。
- 重写 entry 加载时的原文件路径（非按可能被手编辑的 statement 重算），否则改名的 statement 会生成第二个文件、原 active 副本会在 reload 时再次授权。

## 安全：路径围栏

`ensureWithinRoot`（`values.go:428`）：domain/slug 已 sanitize 为单段（`sanitizeSegment` 只留 `[a-z0-9-_]`），但预存 symlink 可能重定向写到 root 外——`EvalSymlinks` 解析 root 与 target，确认 target 在 root 内，否则拒写。

## 数据

纯文件（`<world>/values/<domain>/<slug>.md`），无 SQLite 表。漂移检测的纠正信号来自 journal（world）。

## 跨包接缝

- **← action**：`ValueGate.Permit` 是行动管线头段；gateway `newValueGate(valuesStore, actionStore)` 适配。
- **← episode**：`Digest()` 经 `valueDigester` 注入 Composer 的 Values 段。
- **← sleep**：`DriftJob` 调 `MarkDrifting`。
- **← tool**：`values` 工具调 `Add`（`NewValuesTool`）。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 价值是自主许可源 | 宪法 4「可逆优先」 | 自主行动必须有许可，否则 ask-once |
| markdown + git 仓 | 宪法 6「本地主权」 | 可溯源、可手编辑、可审计 |
| drift fail-safe | 安全 | 误判只代价一次重问 |
| Reversible 豁免 gate | 成本/体验 | 可 undo 的低风险行动不该问 |

蓝图验收：同一价值权衡建立条目后 30 天内零重复提问；每个自主行动 Receipt 都能引用许可它的 value id（或 trust 等级）。

下一篇：[08-action.md](08-action.md) — 行动层。
