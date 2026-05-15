# A2A Protocol — Agent-to-Agent Interoperability

**日期**: 2026-05-15
**范围**: 新增 `internal/a2a/` 包，实现 Google A2A 协议，使 IronClaw 可以与其他 A2A agent 互相发现、通信和委派任务。

## 概述

Google 的 Agent-to-Agent (A2A) 协议正在成为 agent 互操作的事实标准。它定义了 agent 如何通过标准 HTTP API（`/.well-known/agent.json` + `/tasks`）发布自身能力、接收任务、返回结果。

本次改动实现完整的 A2A 客户端（调用其他 agent）和服务端（暴露 IronClaw 为 A2A agent），全部使用 `net/http` 标准库，零外部依赖。

## 架构

### 协议类型 (`protocol.go`)

```go
// AgentCard — A2A 发现文档
type AgentCard struct {
    Name         string
    Description  string
    URL          string
    Version      string
    Capabilities Capabilities
    Skills       []AgentSkill
}

type Capabilities struct {
    Streaming          bool
    PushNotifications  bool
}

type AgentSkill struct {
    Name        string
    Description string
    InputSchema map[string]any
}

// Task — A2A 工作单元
type Task struct {
    ID     string
    State  TaskState       // pending → processing → completed/failed
    Input  TaskInput
    Output TaskOutput
}

type TaskState string
const (
    TaskStatePending    TaskState = "pending"
    TaskStateProcessing TaskState = "processing"
    TaskStateCompleted  TaskState = "completed"
    TaskStateFailed     TaskState = "failed"
)
```

### A2A Client (`client.go`)

```go
type Client struct {
    httpClient *http.Client
    baseURL    string
    agentCard  *AgentCard       // 缓存的 AgentCard
    mu         sync.RWMutex
}
```

| 方法 | HTTP | 说明 |
|------|------|------|
| `NewClient(baseURL)` | — | 创建客户端，30s 超时 |
| `Discover(ctx)` | `GET /.well-known/agent.json` | 获取 AgentCard，缓存结果 |
| `SendTask(ctx, input)` | `POST /tasks` | 提交任务，返回带 pending 状态的 Task |
| `GetTask(ctx, id)` | `GET /tasks/{id}` | 查询任务状态 |
| `CancelTask(ctx, id)` | `DELETE /tasks/{id}` | 取消任务 |

**重试策略**：5xx 错误自动重试 2 次，指数退避（1s → 2s）。

### A2A Server (`server.go`)

```go
type Server struct {
    card    AgentCard
    handler TaskHandler
    tasks   map[string]*Task      // taskID → Task
    mu      sync.RWMutex
}

type TaskHandler func(ctx context.Context, task *Task) (*TaskOutput, error)
```

| 路由 | 方法 | 说明 |
|------|------|------|
| `/.well-known/agent.json` | GET | 返回 AgentCard JSON |
| `/tasks` | POST | 创建任务（异步 handler），返回 `processing` 状态 |
| `/tasks/{id}` | GET | 返回任务当前状态和输出 |
| `/tasks/{id}` | DELETE | 标记任务为 canceled |

**异步执行模型**：

```
POST /tasks
  │
  ├── 创建 Task (state=pending)
  ├── go handler(ctx, task)     ← 后台 goroutine
  └── 返回 202 + Task (state=processing)

Handler goroutine:
  ├── task.State = processing
  ├── output, err := handler(ctx, task)
  ├── task.Output = output
  └── task.State = completed | failed
```

关键：handler 使用独立 context（非 HTTP 请求 context），任务不会因 HTTP 连接关闭而被误取消。

### Gateway 集成

A2A 注册为 hot-reloadable feature：

```go
r.Register(feature.Feature{
    Name:          "a2a",
    Default:       false,       // 默认关闭（安全考虑）
    Phase:         feature.PhaseStart,
    HotReloadable: true,
})
```

生命周期 hook：
```go
OnEnable  → gw.startA2AServer()  // 创建 Server，启动 :9191
OnDisable → gw.stopA2AServer()   // 优雅关闭
```

IronClaw 作为 A2A agent 的卡片：
```json
{
  "name": "IronClaw",
  "description": "IronClaw — local-first self-evolving AI agent runtime",
  "url": "http://localhost:9191",
  "version": "1.0",
  "capabilities": {"streaming": true, "push_notifications": false},
  "skills": [{
    "name": "agent_task",
    "description": "Execute a task using the IronClaw agent runtime",
    "input_schema": {"type": "object", "properties": {"message": {"type": "string"}}}
  }]
}
```

### 互操作场景

```
外部 A2A Agent                 IronClaw (A2A Server)
     │                              │
     ├── GET /.well-known/agent.json──→ 返回 AgentCard
     │                              │
     ├── POST /tasks ──────────────→ 创建任务
     │   {"message": "分析 X 项目"}    │
     │                              ├── handler 路由到 agent runtime
     │                              ├── agent 执行任务
     │                              └── 返回 TaskOutput
     │   ← 202 {state: "processing"}│
     │                              │
     ├── GET /tasks/{id} ──────────→ 轮询状态
     │   ← 200 {state: "completed"} │
```

## 文件

| 文件 | 说明 |
|------|------|
| `internal/a2a/protocol.go` | AgentCard / Task / TaskState / Artifact 类型定义 |
| `internal/a2a/client.go` | A2A 客户端：Discover + SendTask + GetTask + CancelTask + 重试 |
| `internal/a2a/server.go` | A2A 服务端：card 暴露 + 异步任务执行 + 状态查询 |
| `internal/a2a/protocol_test.go` | 全覆盖：JSON 往返 / 客户端发现 / 任务流 / 服务端异步 / 错误处理 |
| `internal/gateway/features.go` | A2A feature 注册 + 生命周期 hook |
| `internal/gateway/gateway.go` | +a2aServer 字段，+startA2AServer/+stopA2AServer 方法 |
