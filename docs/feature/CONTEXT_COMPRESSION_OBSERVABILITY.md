# 上下文压缩可观测性（TUI 通知）

**日期**: 2026-04-21
**范围**: TUI 压缩事件通知 + 统计面板 Compressions 计数器

## 概述

在引入 5 层上下文压缩管线（`PipelineContextManager`）之后，压缩事件对用户来说仍然完全不可见：`TUIEmitter.EmitContextCompress` 是一个空函数体，Runtime 每次触发主动压缩或反应式 413 压缩时，TUI 界面毫无反馈。用户既不知道压缩是否发生，也无法判断上下文被压缩了多少。

本次改进将压缩事件完整接入 Bubble Tea 消息循环，在对话流中插入系统通知，并在统计面板持续显示压缩累计次数和最近一次压缩前后的利用率对比。

## 背景与动机

### 已有基础设施

`agent.DashboardEmitter` 接口已定义 `EmitContextCompress` 方法：

```go
type DashboardEmitter interface {
    // ...其他方法...
    EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64)
}
```

`PipelineContextManager` 在两处调用该方法：

- **主动压缩**（`Compress`）：利用率超过阈值、管线至少执行一层后调用，`reason = "proactive"`
- **反应式压缩**（`ReactiveCompress`）：API 返回 413/context_length_exceeded 错误时调用，`reason = "reactive_413"`

Dashboard 侧的 `Emitter` 已正确实现。TUI 侧的 `TUIEmitter` 方法体为空，无任何用户反馈。

### 两种压缩触发路径

| 触发路径 | reason 字段 | 触发条件 |
|----------|------------|---------|
| `Compress()` | `"proactive"` | 每次迭代前，利用率超过管线配置阈值 |
| `ReactiveCompress()` | `"reactive_413"` | API 返回上下文长度错误，无条件全层压缩 |

## 实现细节

### 1. 新消息类型（`messages.go`）

在 `internal/channel/tui/messages.go` 中新增 Bubble Tea 消息类型，承载压缩事件的全部元数据：

```go
// compressionNotificationMsg is sent when context compression fires.
type compressionNotificationMsg struct {
    sessionID string
    reason    string
    layersRun int
    beforePct float64
    afterPct  float64
}
```

字段说明：

| 字段 | 类型 | 含义 |
|------|------|------|
| `sessionID` | string | 当前会话 ID（用于多会话区分） |
| `reason` | string | 触发原因：`"proactive"` 或 `"reactive_413"` |
| `layersRun` | int | 本次压缩实际执行的层数（0–5） |
| `beforePct` | float64 | 压缩前上下文利用率（0.0–1.0） |
| `afterPct` | float64 | 压缩后上下文利用率（0.0–1.0） |

### 2. TUIEmitter 实现（`emitter.go`）

`TUIEmitter.EmitContextCompress` 从空函数体改为通过 `tea.Program.Send` 向 Bubble Tea 主循环发送消息：

```go
func (e *TUIEmitter) EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64) {
    if e == nil || e.program == nil {
        return
    }
    e.program.Send(compressionNotificationMsg{
        sessionID: sessionID,
        reason:    reason,
        layersRun: layersRun,
        beforePct: beforePct,
        afterPct:  afterPct,
    })
}
```

**并发安全性**：`tea.Program.Send` 内部通过 channel 实现，无需额外锁，可在任意 goroutine 中调用。nil 守卫确保 TUI 未初始化时（如批量 API 模式）不会 panic。

### 3. Model 新增字段（`model.go`）

在 `Model` 结构体中增加四个压缩状态跟踪字段：

```go
compressionCount   int     // 本次会话压缩累计次数
lastCompressFrom   float64 // 最近一次压缩前的利用率（0.0–1.0）
lastCompressTo     float64 // 最近一次压缩后的利用率（0.0–1.0）
lastCompressReason string  // 最近一次压缩原因
```

### 4. Update() 处理器（`model.go`）

在 `Update` 方法的消息分派中新增 `compressionNotificationMsg` case：

```go
case compressionNotificationMsg:
    m.compressionCount++
    m.lastCompressFrom = msg.beforePct
    m.lastCompressTo = msg.afterPct
    m.lastCompressReason = msg.reason
    notification := fmt.Sprintf(
        "🗜️ Context compressed: %d%% → %d%% (%s, %d layers)",
        int(msg.beforePct*100), int(msg.afterPct*100),
        msg.reason, msg.layersRun,
    )
    m.messages = append(m.messages, chatMessage{
        role:      "system",
        content:   notification,
        timestamp: time.Now(),
    })
    m.updateViewportKeepScroll()
    return m, nil
```

通知消息以 `role: "system"` 插入对话历史，与用户消息、Agent 响应的渲染样式区分，格式示例：

```
🗜️ Context compressed: 85% → 42% (proactive, 3 layers)
🗜️ Context compressed: 91% → 38% (reactive_413, 5 layers)
```

`updateViewportKeepScroll()` 确保视口在用户未手动滚动时自动跟随到最新消息。

### 5. 统计面板 Compressions 行（`model.go`）

在 `renderStats()` 函数中，当 `compressionCount > 0` 时追加 Compressions 行：

```go
if m.compressionCount > 0 {
    compVal := fmt.Sprintf("%d", m.compressionCount)
    if m.lastCompressFrom > 0 {
        compVal += fmt.Sprintf(" (last: %d%%→%d%%)", int(m.lastCompressFrom*100), int(m.lastCompressTo*100))
    }
    _, _ = fmt.Fprintf(&b, "  %s %s\n",
        statsLabelStyle.Render("Compressions:"),
        statsValueStyle.Render(compVal))
}
```

统计面板显示示例：

```
  Compressions:  3 (last: 85%→42%)
```

首次压缩前（`compressionCount == 0`）该行完全隐藏，不占用面板空间。

## 数据流

```
PipelineContextManager
│
├── Compress()  ─────────────────────────────────────────┐
│   reason="proactive"                                    │
└── ReactiveCompress()  ───────────────────────────────── │
    reason="reactive_413"                                 │
                                                          ▼
                                              agent.DashboardEmitter.EmitContextCompress(
                                                  sessionID, reason, layersRun,
                                                  beforePct, afterPct
                                              )
                                                          │
                                                          ▼
                                              TUIEmitter.EmitContextCompress()
                                              tea.Program.Send(compressionNotificationMsg{...})
                                                          │
                                              ┌───────────▼────────────┐
                                              │  Bubble Tea 主循环      │
                                              │  Update(msg)            │
                                              │  case compressionMsg:   │
                                              │  ├─ compressionCount++  │
                                              │  ├─ 追加 system 消息    │
                                              │  └─ 刷新统计面板        │
                                              └────────────────────────┘
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/channel/tui/messages.go` | 修改 | 新增 `compressionNotificationMsg` 结构体 |
| `internal/channel/tui/emitter.go` | 修改 | `EmitContextCompress` 从空函数体改为 `tea.Program.Send` |
| `internal/channel/tui/model.go` | 修改 | 新增 4 个 Model 字段、`Update` 处理器 case、统计面板 Compressions 行 |

## 验证

### 手动验证

1. 配置 `agent.compression.strategy: "layered"` 并设置较低阈值（如 `tool_eviction_pct: 10`）
2. 启动 `ironclaw tui`，发送需要多轮工具调用的请求
3. 预期：对话流中出现 `🗜️ Context compressed: X% → Y% (proactive, N layers)` 系统消息
4. 预期：统计面板底部出现 `Compressions: N (last: X%→Y%)` 行

### 反应式压缩验证

当 API 返回上下文长度错误时（需要足够长的对话历史触发）：

- 消息显示 `reactive_413` 作为原因
- `layersRun` 为 5（所有层均强制执行）

### nil 守卫验证

在非 TUI 模式（如 Telegram、Web API）下，`TUIEmitter` 为 nil，`EmitContextCompress` 直接返回，不影响其他 channel。
