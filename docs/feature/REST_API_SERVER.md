# REST API Server（ironclaw serve）

**日期**: 2026-05-01  
**范围**: `internal/api/server.go` + `cmd/ironclaw/serve.go` + `internal/session/manager.go` + `internal/gateway/gateway.go` + `internal/config/config.go`

## 概述

在本次改动之前，IronClaw 只能通过 TUI、Telegram 或 Web Dashboard 交互，没有标准 HTTP API。这意味着它无法被 CI/CD 管道、IDE 插件或第三方系统编程调用，永远是单机工具而非平台。

本次改动新增 `ironclaw serve` 子命令，启动一个独立的生产可用 HTTP API Server，暴露完整的 session 管理和消息收发接口，支持 SSE 流式响应和 WebSocket 事件订阅。

## API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/v1/sessions` | 创建新 session，返回 `{session_id, created_at}` |
| `GET` | `/v1/sessions/{id}` | 获取 session 状态（消息数、最后活跃时间） |
| `POST` | `/v1/sessions/{id}/messages` | 发送消息，SSE 流式响应 |
| `GET` | `/v1/sessions/{id}/events` | WebSocket，订阅该 session 的实时事件 |
| `DELETE` | `/v1/sessions/{id}` | 重置/删除 session |
| `GET` | `/v1/health` | 健康检查，返回 `{status: "ok", version: "..."}` |

## 架构设计

### 依赖注入（避免循环依赖）

`internal/api` 包不能直接 import `internal/gateway`（会形成循环）。通过接口解耦：

```go
// internal/api/server.go
type Gateway interface {
    GetOrCreateSession(ctx context.Context, channelName, channelID string) (*session.Session, error)
    GetSessionByID(ctx context.Context, sessionID string) (*session.Session, error)
    ResetSessionByID(ctx context.Context, sessionID string) error
    HandleMessage(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) error
}
```

`gateway.Gateway` 实现这个接口，`APIServer` 只依赖接口，不依赖具体类型。

### SSE 流式响应

`POST /v1/sessions/{id}/messages` 返回 `text/event-stream`，每个 token 一个 `data:` 事件，结束时发 `data: [DONE]`：

```
data: 这是
data: 第一
data: 个回复
data: [DONE]
```

`SSEChannel` 实现 `channel.Channel` 接口，把 `OutboundMessage.Text` 写成 SSE 事件流，支持 `SendStreaming` 的增�� delta 模式：

```go
type SSEChannel struct {
    w       http.ResponseWriter
    flusher http.Flusher
    mu      sync.Mutex
}
```

响应头：
```
Content-Type: text/event-stream
Cache-Control: no-cache
X-Accel-Buffering: no
```

### WebSocket 事件订阅

`GET /v1/sessions/{id}/events` 升级为 WebSocket，订阅 dashboard event bus，过滤出属于该 session 的事件实时推送给客户端：

```go
sub := s.bus.Subscribe()
defer s.bus.Unsubscribe(sub)
for ev := range sub {
    if ev.SessionID == sessionID {
        conn.WriteJSON(ev)
    }
}
```

### Token 认证

如果 `config.API.Token` 非空，所有 `/v1/sessions*` 端点要求 `Authorization: Bearer <token>` header，`/v1/health` 不需要认证。

### Gateway 新增方法

为满足 `api.Gateway` 接口，`gateway.Gateway` 新增四个导出方法：

```go
func (gw *Gateway) GetOrCreateSession(ctx, channelName, channelID) (*session.Session, error)
func (gw *Gateway) GetSessionByID(ctx, sessionID) (*session.Session, error)
func (gw *Gateway) ResetSessionByID(ctx, sessionID) error
func (gw *Gateway) HandleMessage(ctx, ch, msg) error
```

`HandleMessage` 内部调用重构后的 `handleMessageWithChannel`，原有的 `handleInbound` 也改为调用同一个函数，消除代码重复。

### Session Manager 新增方法

```go
// GetByID 先查内存缓存，再查 DB，返回 session 对象
func (m *Manager) GetByID(ctx, sessionID) (*Session, error)

// ResetByID 通过 session ID 删除 session
func (m *Manager) ResetByID(ctx, sessionID) error
```

### ironclaw serve 命令

```bash
ironclaw serve [--host 0.0.0.0] [--port 8080] [--config configs/ironclaw.yaml]
```

启动流程：
1. 加载 config
2. 初始化 Gateway（`gateway.New` + `gateway.Start`）
3. 创建独立 dashboard Bus（不依赖 dashboard feature 是否启用）
4. 启动 APIServer
5. 监听 SIGINT/SIGTERM，优雅退出（10s 超时）

API Server 和 Dashboard Server 可以同时运行在不同端口，互不干扰。

## 配置

```yaml
api:
  token: "your-secret-token"  # 留空则不启用认证
```

## 使用示例

```bash
# 启动 API server
ironclaw serve --port 8080

# 创建 session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer your-token"
# {"session_id":"sess_xxx","created_at":"2026-05-01T..."}

# 发送消息（SSE 流式）
curl -X POST http://localhost:8080/v1/sessions/sess_xxx/messages \
  -H "Authorization: Bearer your-token" \
  -H "Content-Type: application/json" \
  -d '{"text": "分析这个项目的架构"}' \
  --no-buffer

# 健康检查
curl http://localhost:8080/v1/health
# {"status":"ok","version":"0.1.0"}
```

## 文件清单

| 文件 | 改动 |
|------|------|
| `internal/api/server.go` | 新建：APIServer、SSEChannel、sseStreamUpdater、路由、认证中间件 |
| `cmd/ironclaw/serve.go` | 新建：serveCmd Cobra 子命令 |
| `internal/gateway/gateway.go` | 新增 4 个导出方法；重构 `handleInbound` → `handleMessageWithChannel` |
| `internal/session/manager.go` | 新增 `GetByID`、`ResetByID` |
| `internal/config/config.go` | 新增 `API.Token` 配置字段 |
| `cmd/ironclaw/main.go` | 注册 `serveCmd` |
