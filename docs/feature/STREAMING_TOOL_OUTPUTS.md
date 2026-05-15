# Streaming Tool Outputs

**日期**: 2026-05-15
**范围**: 工具执行时实时流式传输 stdout 到 channel，消除长命令的"冻结"体验——对标 Claude Code 和 Cursor 的实时工具输出。

## 概述

此前 bash 工具将所有输出缓冲到 `bytes.Buffer`，在命令完全结束后才返回完整结果。对于长时间运行的命令（编译、测试、大文件下载），用户在等待期间看不到任何进度，体验如同 agent "卡住了"。

本次改动添加了基于 context 的流式回调机制：当 channel 支持 `ToolStreamWriter` 时，bash 工具通过 pipe 实时流式传输 stdout chunk，channel 可以立即显示给用户。

## 架构

### Context 驱动的 StreamCallback

流式传输不修改 `Tool` 接口，而是通过 context 传递回调：

```go
// tool/tool.go

type StreamCallback func(chunk string)

func WithStreamCallback(ctx context.Context, cb StreamCallback) context.Context {
    return context.WithValue(ctx, streamCtxKey{}, cb)
}

func StreamCallbackFromContext(ctx context.Context) StreamCallback {
    cb, _ := ctx.Value(streamCtxKey{}).(StreamCallback)
    return cb
}
```

### Channel 接口扩展 (`channel.go`)

```go
// ToolStreamWriter — channel 的可选接口
type ToolStreamWriter interface {
    WriteToolStream(ctx, target, toolName, chunk string) error
    FlushToolStream(ctx, target, toolName string) error
}
```

### Bash 工具改造 (`bash.go`)

**改动前**（全缓冲）：
```go
cmd.Stdout = &stdout       // 所有输出进入 Buffer
cmd.Run()                   // 阻塞直到命令完成
// ...解析 stdout.String()
```

**改动后**（条件流式）：
```go
streamCB := StreamCallbackFromContext(ctx)

if streamCB != nil {
    // 使用 Pipe 在命令运行时读取 stdout
    stdoutPipe, _ := cmd.StdoutPipe()
    cmd.Start()

    buf := make([]byte, 4096)
    for {
        n, err := stdoutPipe.Read(buf)
        if n > 0 {
            stdout.Write(buf[:n])     // tee 到 buffer（用于最终结果）
            streamCB(string(buf[:n])) // 流式传输到 channel
        }
        if err != nil { break }
    }
    cmd.Wait()
} else {
    cmd.Stdout = &stdout
    cmd.Run()
}
```

### Gateway 接线 (`gateway.go`)

```go
func (gw *Gateway) handleInbound(ctx context.Context, msg channel.InboundMessage) {
    // ...
    ch, _ := gw.channels[msg.Channel]

    // 检测 channel 是否支持流式工具输出
    if streamWriter, ok := ch.(channel.ToolStreamWriter); ok {
        ctx = tool.WithStreamCallback(ctx, func(chunk string) {
            streamWriter.WriteToolStream(ctx, target, "bash", chunk)
        })
    }

    // 将带 StreamCallback 的 context 传递给 agent
    switch gw.CurrentMode() {
    case "cognitive":
        gw.cognitiveAgent.HandleMessage(ctx, ch, msg)
    // ...
    }
}
```

### 数据流

```
bash 命令执行
  │
  ├── stdout Pipe ──→ goroutine 读取 chunk
  │                      │
  │                      ├── stdout Buffer (tee, 用于最终结果)
  │                      └── StreamCallback(chunk)
  │                            │
  │                            ▼
  │                      ToolStreamWriter.WriteToolStream()
  │                            │
  │                            ▼
  │                      Channel (Telegram: 编辑消息, TUI: 更新终端)
  │
  └── cmd.Wait() → 正常错误处理 → 返回 Result
```

## Channel 适配

| Channel | 支持 ToolStreamWriter | 行为 |
|---------|---------------------|------|
| TUI | 是 | 实时更新终端输出面板 |
| Telegram | 是 | 编辑流式消息，增量显示 |
| Discord | 待实现 | 可通过 `SendStreaming` + 编辑消息实现 |
| Web Dashboard | 待实现 | 可通过 WebSocket 推送 |

## 配置

无需配置。StreamCallback 在 `handleInbound` 中自动附加，仅当 channel 实现 `ToolStreamWriter` 时生效。channel 不支持时，bash 回退到全缓冲模式。

## 文件

| 文件 | 改动 |
|------|------|
| `internal/channel/channel.go` | +ToolStreamWriter 接口 |
| `internal/tool/tool.go` | +StreamCallback 类型，+WithStreamCallback，+StreamCallbackFromContext |
| `internal/tool/bash.go` | 条件 pipe 流式 + tee 到 buffer |
| `internal/gateway/gateway.go` | handleInbound 中检测 channel 并附加 StreamCallback |
