# 08 · action — 行动层

> 包路径 `internal/action` · 蓝图 §4.6 · **安全的主线**

## 职责

一切副作用的唯一出口。行动层是工具拦截链中的一段 `tool.ToolInterceptor`（不是独立服务），挂在 permission 放行之后，只看到被许可的调用与原始执行结果。它做四件事：**按可逆性分类、查价值/信任决定能否自主放行、Compensable 进 hold 队列、Reversible 记 undo 账并盖 receipt。**

## 可逆性分类

```go
// internal/action/action.go
type Class int
const (
    Reversible   Class = iota // git 跟踪的文件改动 / 世界模型写 → 立即执行 + 记 undo
    Compensable               // 发邮件/发消息/可取消订单 → hold 队列延迟，可撤回
    Irreversible              // 支付/不可恢复删除/法律承诺 → 审批，信任封顶永不全自动
)
```

## 信任账本（autonomy ledger）

```go
type Level int
const (
    AskEvery     Level = iota // 每次审批
    AskFirst                  // 仅首次审批，之后信任
    HoldThenAuto              // 过 recall 窗口后自动执行
    FullAuto                  // 立即自动，无 hold
)
```

### 升降级规则（确定性，不经模型）

```go
func promotionThreshold(level Level) int {  // 离开某级所需 verified 数
    AskEvery → AskFirst:     1
    AskFirst → HoldThenAuto: 3
    HoldThenAuto → FullAuto: 10
}
func classCeiling(c Class) Level {  // Irreversible 封顶 HoldThenAuto，永不 FullAuto
    if c == Irreversible { return HoldThenAuto }
    return FullAuto
}
```

- `RecordAttempt(class, contextKey, verified)`：记一次执行；**战绩干净**（`corrected==0`）且 `verified_ok` 达阈值则升一级（返回 `TrustChange{Promoted, From, To}`），通知用户（可否决）。
- `RecordCorrection(class, contextKey)`：用户纠正 → 降一级（floored at AskEvery）+ **`corrected` 永久冻结升级**——自治只能靠不间断的 verified 战绩赢得。
- Irreversible 封顶 `HoldThenAuto`，保留人签（宪法第 4 条）。

`(action_class, context_key)` 是信任的粒度——如 `("compensable", "send_email")`。

## 执行管线

`Interceptor.Intercept`（`interceptor.go:83`）的完整顺序：

```
1. classify(call) → (class, governed)        // 静态声明 + 参数动态修正
2. governed && dry-run？→ dryRunResult        // 影子脑 record-only 短路（不执行、零状态变更）
3. contextKey = classifier.ContextKey(call)
4. ── values 段（管线头）──
   governed && class != Reversible && gate != nil:
       Permit(class, contextKey)
       !permitted → valueBlockedResult（工具不执行）→ 触发 ask-once
       permitted → valueRef = ref
5. ── hold 段 ──
   governed && Compensable && holdEnabled && !dryRun:
       CreateHold(window 默认 120s)
       ActionCollector.Record(false)         // 排队=未验证行动，其情节不算 distill-clean
       → heldResult（不立即执行，可 daimon holds recall）
6. ── undo 捕获（执行前）──
   governed && Reversible:
       captureFileUndo(call)                 // 快照旧文件状态，best-effort 不阻塞
7. ── 执行 ──
   result, err = next(ctx, call)
8. !governed？→ 直接返回
9. verified = succeeded && class == Reversible // 仅可逆行动凭成功自动 verified
10. ActionCollector.Record(verified)          // 每个 governed 调用都记（独立于 trust store）
11. RecordAttempt(class, contextKey, verified) → 升级 → notifier
12. Reversible: valueRef = "trust:<level>"
13. 盖 receipt: result.Metadata["action_class" / "value_ref" / "receipt_id"]
14. captureUndo && succeeded → RecordUndo（undo_journal 落账）
```

关键设计点：

- **仅 Reversible 凭成功自动 verified**：可逆行动可 undo，成功即足够证据。Compensable/Irreversible 记 attempt 但绝不凭"成功"自动 verified——它们停在 ask-every 直到显式客观验证机制标记，把高风险行动留在人签后。
- **ActionCollector 在 store-nil 守卫前调用**：即使没配 trust store，每个 governed（可能未验证）行动也被记入情节的验证 collector——否则会被静默降到"0 未验证"，错误地让其情节看起来 distill-clean。

## hold 队列（Compensable 执行环）

`Store` 管理 hold 状态机（`action.go:418`）：

```
pending ──CreateHold──▶ [recall 窗口] ──ClaimHold(CAS)──▶ executing ──▶ executed | failed
   │                                                                        
   └──RecallHold──▶ recalled（用户撤回，工具不执行）
```

- `CreateHold`：Compensable 入队，`execute_at = now + holdWindow`。
- `DueHolds(now)`：`state='pending' AND execute_at <= now`。
- `ClaimHold(id)`：原子 `pending → executing`（CAS），返回是否成功——绝不点燃已 recalled/已执行的 hold，绝不双发。
- `MarkHoldState(executed|recalled|failed)`：终态，无重试（失败不再静默标 executed）。
- `RecoverStaleHolds`：启动时把崩溃残留的 `executing` 重置回 `pending`（启动时无 in-flight，executing 必是孤儿）。
- `RecallHold`：撤回仍 pending 的 hold（端到端 <1s）。

执行环在 gateway `Start` 里：先 `RecoverStaleHolds`，再 timer ticker 扫 `DueHolds` → `ClaimHold` → 经 Registry 重放执行 → `MarkHoldState`（[14-gateway.md](14-gateway.md)）。门控 `agent.action.hold_enabled`（默认 false）。

## undo（可逆性装牙）

```go
func (s *Store) RecordUndo(ctx, r UndoRecord) error           // undo_journal 落账
func (s *Store) GetUndo / ListUndoable / ListUndoableByEpisode
func (s *Store) Undo(ctx, root, receiptID) error              // execute-first 再 MarkUndone
func (s *Store) UndoEpisode(ctx, root, episodeID) (reversed int, error)  // 整情节 LIFO，errors.Join
```

`undo.go` 的 `ExecuteUndo` 解码 `fileUndoSnapshot` → 恢复 Prev 内容或删除新建文件（幂等容忍 NotExist），带 symlink 逃逸检测（`fencedRealPath` 重校验父目录在 root 内）。`Undo` 编排 execute-first（先反转文件再标 done，避免标记 done 但文件未反转）。

CLI：`daimon undo [receipt-id|list]` 或 `daimon undo --episode <id>`（[21-cli-reference.md](21-cli-reference.md)）。

## 分类器与 AST

```go
type Classifier interface {
    Classify(call *tool.ToolCall) (Class, governed bool)
    ContextKey(call *tool.ToolCall) string
}
```

- `defaultClassifier`：工具静态声明分类。
- `holdAwareClassifier`（`NewClassifierWithCompensableHTTP`，hold_enabled 时启用）：mutating HTTP（POST/PUT/PATCH/DELETE）→ Compensable。
- **bash AST 分类**（`ast.go`）：用 `mvdan.cc/sh` 解析 bash 为 AST，按命令名 + 参数分类（替代子串黑名单），抵御变形命令。检测 `rm/dd/shred/mkfs`、嵌套解释器（`eval`/`bash -c`/`python -c`，递归至深度 8）、设备重定向、fork-bomb。**fail-closed**：解析错误 / 未知展开 / 可疑模式 → Irreversible。

## 沙箱（代码域）

代码域工具叠加 OS 沙箱档（`subsystem_tool.go:85`）：

```go
shellBackend := tool.NewChannelRoutingBackend(
    tool.NewHostShellBackend(), tool.NewSeatbeltShellBackend(),
    cfg.Tools.Exec.Backend == "seatbelt")
```

`tools.exec.backend = host | seatbelt`。**远程触发（telegram/timer/internal）的 bash 强制走 seatbelt**（macOS `sandbox-exec` + 动态 SBPL profile），本地按配置默认。非 darwin 回退 host + 警告。这是宪法第 4 条「可逆性分域」的代码域一半——生活域用可逆性+trust+hold，代码域用 OS 沙箱。

## 数据

`trust_ledger` / `undo_journal` / `holds`（迁移 028）；undo_journal 扩列 `episode_id`（迁移 040）。详见 [19-data-layer.md](19-data-layer.md)。

## 跨包接缝

- **挂在 `tool.InterceptorChain`**：链序 `permission → hook → user_hooks → read_before_edit → verify → audit → action → activity`（`subsystem_tool.go:185`）。
- **← values**：`ValueGate.Permit` 头段查价值许可。
- **→ tool**：`captureFileUndo` 读工具输入快照；hold 执行经 Registry 重放。
- **→ gateway**：`TrustNotifier` 由 `gatewayTrustNotifier` 实现，升级时通知用户 + 写 journal `trust_promotion` 审计条目。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 可逆性分类 | 宪法 4「可逆优先」 | 默认最保守类别，按风险分流 |
| Irreversible 封顶 HoldThenAuto | 宪法 4 | 不可逆高风险永远人签 |
| 单次 corrected 冻结升级 | 北极星指标 2 护栏 | 自治靠不间断 verified 战绩 |
| 仅 Reversible 自动 verified | 安全 | 高风险不凭"没报错"就升级 |
| AST 分类 + fail-closed | 安全 | 变形命令/路径逃逸零放行 |
| 远程触发强制 seatbelt | 宪法 4 分域 | 远程 bash 是真实命令执行风险面 |

蓝图验收：对抗用例集（变形命令、路径逃逸、伪装域名邮件）零放行；hold 撤回端到端 <1s；trust 升级全程有通知与审计；断电重启后 holds 队列正确恢复。

下一篇：[09-sleep.md](09-sleep.md) — 睡眠整固。
