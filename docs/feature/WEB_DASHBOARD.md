# Web Dashboard — Agent 实时监控

**日期**: 2026-04-19
**范围**: 新增 `internal/dashboard/` 包 + Preact SPA 前端 + Gateway 集成，提供 Agent 运行时的实时 Web 可视化

## 概述

此前 IronClaw 只能通过 TUI 或 Telegram 与 Agent 交互，缺乏对运行时状态的全局可视化。用户无法回答"Agent 现在在干什么？哪个阶段？调用了什么工具？耗时多少？"等基本问题。

本次改动新增完整的 Web Dashboard 子系统，通过进程内事件总线（Event Bus）捕获 Agent 生命周期事件，经 WebSocket 实时推送到嵌入式 Preact SPA 前端，实现对认知循环五阶段（PERCEIVE → PLAN → ACT → OBSERVE → REFLECT）和工具调用的零延迟可视化。

**设计原则**:
- **Local-first**: 前端通过 `go:embed` 嵌入二进制，无外部依赖，单文件部署
- **零侵入**: Agent 核心代码仅增加可选的 nil-guarded emitter 调用
- **事件驱动**: 解耦的发布-订阅架构，生产者和消费者互不依赖

## 核心架构

### 数据流

```
┌─────────────────────────────────────────────────────────────┐
│                      Agent Runtime                          │
│                                                             │
│  CognitiveAgent                    Runtime (simple)         │
│  ┌──────────┐                      ┌──────────┐            │
│  │PERCEIVE  │◄─ EmitPhaseStart/End │concurrent│            │
│  │PLAN      │                      │.go       │            │
│  │ACT       │   EmitToolStart/End  │          │            │
│  │OBSERVE   │─────────┐            │EmitTool  │            │
│  │REFLECT   │         │            │Start/End │            │
│  └──────────┘         │            └────┬─────┘            │
│                       │                 │                   │
│                       ▼                 ▼                   │
│               ┌───────────────────────────┐                 │
│               │    DashboardEmitter       │                 │
│               │  (agent.DashboardEmitter) │                 │
│               └───────────┬───────────────┘                 │
└───────────────────────────┼─────────────────────────────────┘
                            ▼
                ┌───────────────────────┐
                │      Event Bus        │   ◄── EvolutionBridge
                │  (non-blocking pub/   │       (evolution.Hook → Event)
                │   sub, buf=256)       │
                └──────┬────────┬───────┘
                       │        │
              ┌────────┘        └────────┐
              ▼                          ▼
   ┌──────────────────┐      ┌──────────────────┐
   │AgentStateTracker │      │   WebSocket Hub   │
   │ (in-memory state │      │  (gorilla/ws)     │
   │  snapshot)       │      │  broadcast to     │
   │                  │      │  all clients      │
   └────────┬─────────┘      └────────┬──────────┘
            │                         │
            ▼                         ▼
   ┌──────────────────┐      ┌──────────────────┐
   │  REST API        │      │  /ws endpoint     │
   │  /api/agent/state│      │  real-time events │
   │  /api/sessions   │      │                   │
   └────────┬─────────┘      └────────┬──────────┘
            │                         │
            └────────────┬────────────┘
                         ▼
              ┌──────────────────────┐
              │   Preact SPA         │
              │  (go:embed → binary) │
              │                      │
              │  ┌─ AgentStatus ──┐  │
              │  ├─ PhaseTimeline ┤  │
              │  ├─ ToolCallFeed  ┤  │
              │  └─ SessionList ──┘  │
              └──────────────────────┘
```

### Event Bus

进程内发布-订阅事件总线，所有 Dashboard 事件的唯一传输层。

```go
type EventType string

const (
    EventPhaseStart    EventType = "phase.start"
    EventPhaseEnd      EventType = "phase.end"
    EventToolStart     EventType = "tool.start"
    EventToolEnd       EventType = "tool.end"
    EventPlanGenerated EventType = "plan.generated"
    EventReplanStart   EventType = "replan.start"
    EventTaskUpdate    EventType = "task.update"
    EventSessionStart  EventType = "session.start"
    EventSessionEnd    EventType = "session.end"
    EventAgentIdle     EventType = "agent.idle"
)

type Event struct {
    Type      EventType      `json:"type"`
    Timestamp time.Time      `json:"timestamp"`
    SessionID string         `json:"session_id,omitempty"`
    Data      map[string]any `json:"data"`
}
```

**关键设计**: `Publish` 使用 `select`/`default` 模式实现非阻塞广播——慢订阅者丢弃事件而不阻塞发布者，确保 Agent 主循环不受 Dashboard 影响。

### DashboardEmitter

定义在 `agent` 包中的接口，避免 `agent` → `dashboard` 的循环依赖：

```go
type DashboardEmitter interface {
    EmitPhaseStart(sessionID, phase string)
    EmitPhaseEnd(sessionID, phase string, durationMs int64)
    EmitToolStart(sessionID, toolName, input string)
    EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
}
```

`dashboard.Emitter` 实现此接口，将方法调用转换为 `Event` 结构并发布到 Bus。工具输入超过 500 字符时自动截断，防止事件膨胀。

所有 emitter 调用点均使用 nil-guard（`if ca.dashEmitter != nil`），Dashboard 未启用时零开销。

### Agent State Tracker

订阅 Event Bus，在内存中维护 Agent 实时状态快照：

```go
type SessionState struct {
    SessionID     string    `json:"session_id"`
    Channel       string    `json:"channel,omitempty"`
    CurrentPhase  string    `json:"current_phase"`
    CurrentTool   string    `json:"current_tool,omitempty"`
    PhaseStart    time.Time `json:"phase_started_at,omitempty"`
    ToolsExecuted int       `json:"tools_executed"`
    ReplanCount   int       `json:"replan_count"`
}

type StateSnapshot struct {
    Status             string         `json:"status"`          // "idle" | "busy"
    ActiveSessions     []SessionState `json:"active_sessions"`
    UptimeSeconds      int64          `json:"uptime_seconds"`
    TotalSessionsToday int            `json:"total_sessions_today"`
}
```

处理的事件类型及状态变更：

| 事件 | 状态变更 |
|------|---------|
| `phase.start` | 更新 `CurrentPhase`、`PhaseStart` |
| `phase.end` | 清空 `CurrentPhase` |
| `tool.start` | 更新 `CurrentTool` |
| `tool.end` | 清空 `CurrentTool`，`ToolsExecuted++` |
| `replan.start` | `ReplanCount++` |
| `session.end` | 移除活跃 session，`totalToday++` |

`Snapshot()` 方法通过 `sync.RWMutex` 提供线程安全的只读快照，供 REST API 使用。

### Evolution Bridge

实现 `evolution.Hook` 接口，将自进化引擎的事件转换为 Dashboard 事件：

| evolution 事件 | Dashboard 事件 |
|---------------|---------------|
| `OnReflectionComplete` | `phase.end` (REFLECT) |
| `OnEpisodeComplete` | `session.end` |
| `OnToolExecuted` | `tool.end` |

仅在 `evolution.Engine` 启用时注册，通过编译时接口检查确保类型安全：

```go
var _ evolution.Hook = (*EvolutionBridge)(nil)
```

### WebSocket Hub

管理 WebSocket 连接，将 Event Bus 事件广播到所有已连接的前端客户端：

- **连接管理**: `register`/`unregister` channel 处理客户端生命周期
- **广播**: 从 Bus 订阅事件，JSON 序列化后发送到所有客户端（慢客户端 drop）
- **心跳**: `writePump` 每 30s 发送 Ping，`readPump` 处理 Pong 并刷新 60s 读超时
- **优雅关闭**: `Stop()` 取消 Bus 订阅，关闭所有客户端连接

## HTTP Server & REST API

### 端点清单

| 端点 | 方法 | 认证 | 数据源 | 说明 |
|------|------|------|--------|------|
| `/health` | GET | 无 | — | 健康检查 |
| `/api/agent/state` | GET | 是 | StateTracker | 实时状态快照 |
| `/api/sessions` | GET | 是 | SQLite | 最近 50 个 session |
| `/api/sessions/{id}/messages` | GET | 是 | SQLite | session 消息列表 |
| `/api/sessions/{id}/tools` | GET | 是 | SQLite | session 工具调用日志 |
| `/api/metrics/health` | GET | 是 | cogmetrics | 认知健康指标（可选） |
| `/ws` | GET | 是 | Hub | WebSocket 实时事件流 |
| `/*` | GET | 无 | embed.FS | SPA 静态文件 + 前端路由 fallback |

### Token 认证

支持两种方式传递 token：

1. **HTTP Header**: `Authorization: Bearer <token>`
2. **Query Parameter**: `?token=<token>`

当 `dashboard.token` 为空时认证自动禁用（开发模式）。`/health` 端点始终免认证。

### SPA Fallback

`spaHandler` 实现客户端路由支持：请求的路径在 `embed.FS` 中不存在时，回退到 `index.html`，由前端 `wouter` 路由器处理。

## 前端架构

### 技术栈

| 库 | 版本 | 用途 |
|---|------|------|
| Preact | 10.x | React-compatible UI（~3KB gzip） |
| wouter | 3.x | 轻量路由（~1.5KB） |
| Vite | 8.x | 构建工具，输出到 `internal/dashboard/dist/` |
| TypeScript | 5.x | 类型安全 |

### 状态管理

`useAgentState` hook 使用 `useReducer` 管理全局状态，融合两种数据源：

1. **REST 快照**: 页面加载时 `fetchAgentState()` 获取初始状态
2. **WebSocket 事件**: 实时增量更新 phase history、tool feed、session status

```
┌─ fetchAgentState() ──► dispatch('snapshot') ──► 初始化状态
│
└─ useWebSocket(onEvent) ──► dispatch('event') ──► 增量更新
                                  │
                                  ├── phase.start → phaseHistory 追加
                                  ├── phase.end   → phaseHistory 标记完成
                                  ├── tool.start  → recentTools 追加（上限 100）
                                  ├── tool.end    → recentTools 标记完成
                                  └── session.end → 重置 phaseHistory
```

### 认证集成

`auth.ts` 模块统一管理 token：

- 从 URL `?token=` 参数读取并缓存
- `authHeaders()` 为所有 fetch 请求注入 `Authorization: Bearer` header
- `wsTokenQuery()` 为 WebSocket URL 追加 `?token=` query parameter

### 组件结构

| 组件 | 功能 |
|------|------|
| `Layout` | 侧边栏导航 + WebSocket 连接状态指示灯（绿/红） |
| `AgentStatus` | 当前状态（BUSY/IDLE）、当前阶段、当前工具、session ID |
| `PhaseTimeline` | 五阶段横向时间线：PERCEIVE → PLAN → ACT → OBSERVE → REFLECT，高亮当前阶段，显示耗时 |
| `ToolCallFeed` | 工具调用实时滚动日志（时间、工具名、状态、耗时），上限 100 条 |
| `SessionList` | 活跃 session 列表 + 今日 session 总数 |

### WebSocket 重连

`useWebSocket` hook 实现指数退避自动重连：

- 初始延迟 1s，每次翻倍，上限 30s
- 连接成功后重置重试计数
- 三种状态：`connected` / `reconnecting` / `disconnected`

### 样式

CSS 自定义属性实现暗色主题：

```css
:root {
    --bg-primary: #0d1117;
    --bg-secondary: #161b22;
    --text-primary: #e6edf3;
    --accent: #58a6ff;
    --success: #3fb950;
    --error: #f85149;
    --warning: #d29922;
}
```

## Agent 集成

### Cognitive Agent（5-phase）

在认知循环的每个阶段前后注入 emitter 调用：

```go
// PERCEIVE
if ca.dashEmitter != nil { ca.dashEmitter.EmitPhaseStart(sess.ID, "PERCEIVE") }
// ... PERCEIVE 逻辑 ...
if ca.dashEmitter != nil { ca.dashEmitter.EmitPhaseEnd(sess.ID, "PERCEIVE", duration) }

// PLAN → ACT → OBSERVE → REFLECT 同理
```

`SetDashboardEmitter` 同时传播到 `Executor`，确保 ACT 阶段的工具调用也被追踪。

### Simple-mode Runtime

在 `concurrent.go` 的 `executeToolCall` 中注入工具事件：

```go
if r.dashEmitter != nil {
    r.dashEmitter.EmitToolStart(sess.ID, tc.Name, tc.Input)
}
result, err := t.Execute(ctx, []byte(tc.Input))
duration := time.Since(start).Milliseconds()
// ... 结果处理 ...
if r.dashEmitter != nil {
    r.dashEmitter.EmitToolEnd(sess.ID, tc.Name, status == "success", duration)
}
```

### Gateway 初始化

`initDashboard()` 在 Gateway 构建时按依赖顺序创建所有 Dashboard 组件：

```
Bus(256) → StateTracker → EvolutionBridge* → cogCollector* → Emitter
    → runtime.SetDashboardEmitter
    → cognitiveAgent.SetDashboardEmitter*
    → Hub → StartServer (goroutine)

* 仅在对应组件存在时创建
```

Dashboard 启用时，接管原有的 `internal/gateway/http.go` 服务（通过 `!Dashboard.Enabled` 守卫）。`Stop()` 方法确保 Hub 和 StateTracker 优雅关闭。

## 构建

### 前端构建

Vite 将 Preact SPA 编译为静态文件，输出到 `internal/dashboard/dist/`：

```
web/src/ ──(vite build)──► internal/dashboard/dist/
                              ├── index.html     (0.38 KB)
                              ├── assets/
                              │   ├── index-*.css (0.51 KB)
                              │   └── index-*.js  (38.7 KB / 14.7 KB gzip)
```

### Go 嵌入

`embed.go` 使用 `//go:embed all:dist` 将构建产物嵌入二进制。`dist/` 目录通过 `git add -f` 强制纳入版本控制以确保 `go:embed` 正常工作。

### Makefile

```makefile
web:
    @if [ -d web/node_modules ]; then \
        cd web && npm run build; \
    else \
        cd web && npm ci --prefer-offline && npm run build; \
    fi

build: web
    CGO_ENABLED=1 go build -tags "$(TAGS)" ...
```

`build` 目标依赖 `web`，确保每次构建前先编译前端。`web` 目标在 `node_modules` 已存在时跳过 `npm ci`，加速增量构建。

## 配置

```yaml
dashboard:
  enabled: false            # 是否启用 Web Dashboard
  addr: "127.0.0.1:8080"   # 监听地址
  token: ""                 # 访问 token（空=免认证）
```

启用后访问 `http://127.0.0.1:8080`，如配置了 token 则访问 `http://127.0.0.1:8080/?token=<your-token>`。

## 涉及文件

### 新增文件

| 文件 | 说明 |
|------|------|
| `internal/dashboard/eventbus.go` | 进程内事件总线，10 种事件类型，非阻塞发布 |
| `internal/dashboard/eventbus_test.go` | 4 个测试：发布/订阅、多订阅者、取消订阅、慢订阅者不阻塞 |
| `internal/dashboard/emitter.go` | `agent.DashboardEmitter` 实现，500 字符截断 |
| `internal/dashboard/emitter_test.go` | 3 个测试：阶段事件、工具耗时、输入截断 |
| `internal/dashboard/state_tracker.go` | Agent 状态追踪器，`SessionState`/`StateSnapshot` 快照 |
| `internal/dashboard/state_tracker_test.go` | 3 个测试：阶段转换、工具执行、session 结束 |
| `internal/dashboard/evolution_bridge.go` | `evolution.Hook` → Event Bus 桥接 |
| `internal/dashboard/evolution_bridge_test.go` | 2 个测试：工具事件转换、名称标识 |
| `internal/dashboard/ws_hub.go` | WebSocket 连接管理与事件广播 |
| `internal/dashboard/ws_hub_test.go` | 2 个测试：事件广播、客户端断开 |
| `internal/dashboard/server.go` | HTTP Server，7 个 REST 端点 + SPA fallback + token 认证 |
| `internal/dashboard/server_test.go` | 4 个测试：状态端点、健康检查、SPA fallback、token 认证 |
| `internal/dashboard/embed.go` | `go:embed all:dist`，前端静态文件嵌入 |
| `internal/agent/dashboard_emitter.go` | `DashboardEmitter` 接口定义 |
| `internal/gateway/init_dashboard.go` | Gateway 中 Dashboard 子系统初始化 |
| `web/` | Preact + Vite 前端项目（14 个源文件） |

### 修改文件

| 文件 | 变更 |
|------|------|
| `internal/agent/cognitive.go` | 5 个阶段（PERCEIVE/PLAN/ACT/OBSERVE/REFLECT）添加 `EmitPhaseStart/EmitPhaseEnd`，`SetDashboardEmitter` 传播到 Executor |
| `internal/agent/act.go` | `Executor` 添加 `dashEmitter` 字段，工具执行前后发送事件 |
| `internal/agent/concurrent.go` | Simple-mode `executeToolCall` 添加 `EmitToolStart/EmitToolEnd` |
| `internal/agent/runtime.go` | `Runtime` 添加 `dashEmitter` 字段和 `SetDashboardEmitter` 方法 |
| `internal/config/config.go` | 新增 `DashboardConfig` 结构体 |
| `internal/gateway/gateway.go` | 新增 Dashboard 字段，`initDashboard()` 调用，legacy HTTP 守卫，`Stop()` 清理 |
| `configs/ironclaw.example.yaml` | 新增 `dashboard:` 配置段 |
| `Makefile` | 新增 `web` 构建目标，`build` 依赖 `web` |
| `go.mod` | 新增 `github.com/gorilla/websocket v1.5.3` 直接依赖 |
| `.gitignore` | 新增 `web/node_modules/` |

## 测试

18 个测试用例，覆盖全部后端组件：

| 测试 | 验证内容 |
|------|---------|
| `TestBusPublishToSubscriber` | 基础发布-订阅 |
| `TestBusMultipleSubscribers` | 多订阅者同时收到事件 |
| `TestBusUnsubscribe` | 取消订阅后不再收到事件 |
| `TestBusSlowSubscriberDoesNotBlock` | 慢订阅者不阻塞发布者 |
| `TestEmitterPhaseStart` | 阶段开始事件格式正确 |
| `TestEmitterToolEndWithDuration` | 工具结束事件包含耗时 |
| `TestEmitterTruncatesLongInput` | 超长输入被截断为 500 字符 |
| `TestStateTrackerPhaseTransition` | 阶段转换正确更新快照 |
| `TestStateTrackerToolExecution` | 工具执行更新当前工具和计数 |
| `TestStateTrackerSessionEnd` | session 结束移除活跃状态 |
| `TestEvolutionBridgeToolExec` | evolution 工具事件正确转换 |
| `TestEvolutionBridgeName` | Bridge 名称标识正确 |
| `TestAgentStateEndpoint` | `/api/agent/state` 返回 JSON 快照 |
| `TestHealthEndpoint` | `/health` 返回 200 |
| `TestSPAFallback` | 未知路径回退到 `index.html` |
| `TestTokenAuth` | Bearer header 和 query param 认证 |
| `TestHubBroadcastsEvents` | WebSocket 客户端收到广播事件 |
| `TestHubClientDisconnect` | 客户端断开后正确清理 |

## 后续扩展方向

本次实现为 V1（Agent 实时监控），架构已为后续功能预留扩展点：

| 功能 | 扩展方式 |
|------|---------|
| Session 回放 | 新增 `/api/sessions/{id}/replay` 端点 + 前端时间轴组件 |
| 进化趋势图表 | 接入 `cogmetrics.Collector.Snapshot()` + ECharts |
| Memory 浏览器 | 新增 `/api/memory/` 端点 + 前端搜索组件 |
| OpenTelemetry | 在 Emitter 中同时导出 OTLP spans |
| 多 Agent 监控 | `StateTracker` 已按 `SessionID` 隔离，天然支持多 session 并发 |
