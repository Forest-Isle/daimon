# Web Dashboard — Agent 实时监控 设计文档

## 概述

为 IronClaw 添加嵌入式 Web Dashboard，首期聚焦 **Agent 实时监控**：在浏览器中实时查看 agent 当前任务、认知阶段、工具调用流。后续扩展 Session 回放、进化趋势图表、Memory 浏览器、OpenTelemetry 集成。

### 当前状态

| 层面 | 现状 |
|------|------|
| cogmetrics | `Collector` 实现 `evolution.Hook` 但未注册到运行时。CLI `insights health` 离线重放 JSONL |
| HTTP 端点 | stdlib `net/http`，仅 `/health` + `/api/sessions` |
| 认知阶段跟踪 | `task_ledger` 表记录子任务含时间戳；evolution events 记录 DurationMs |
| Session/Tool 存储 | SQLite: `sessions` + `messages` + `tool_log` (含 duration_ms, status) |
| 日志 | 全项目 `log/slog` 结构化日志 |
| 外部依赖 | go.mod 中无 metrics/tracing 库 |

### 设计决策

- **方案**: Go embed + Preact SPA + WebSocket（方案 A）
- **实时性**: 真实时流，WebSocket 推送
- **使用场景**: 本地开发调试 + 远程运维监控
- **范围**: 第一子项目仅做 Agent 实时监控，架构支持后续扩展

---

## 架构

```
┌─────────────────────────────────────────────────────────────┐
│                       Go Binary                              │
│                                                              │
│  ┌──────────────┐    ┌──────────────┐    ┌───────────────┐  │
│  │  Agent Core   │───▶│  Event Bus   │───▶│  WS Hub       │  │
│  │  (Cognitive/  │    │  (in-process │    │  (broadcast   │  │
│  │   Simple)     │    │   pub/sub)   │    │   to clients) │  │
│  └──────────────┘    └──────┬───────┘    └───────┬───────┘  │
│                             │                     │          │
│  ┌──────────────┐    ┌──────▼───────┐    ┌───────▼───────┐  │
│  │  SQLite DB   │◀───│  REST API    │───▶│  WebSocket    │  │
│  │  (sessions,  │    │  /api/...    │    │  /ws          │  │
│  │   tool_log,  │    └──────────────┘    └───────────────┘  │
│  │   ledger)    │                                │          │
│  └──────────────┘                                │          │
│                                                  │          │
│  ┌──────────────────────────────────────────┐    │          │
│  │  Embedded SPA (go:embed web/dist/)       │◀───┘          │
│  │  Preact + Vite                           │               │
│  └──────────────────────────────────────────┘               │
└─────────────────────────────────────────────────────────────┘
```

### 数据流

1. **Agent → Event Bus**: Agent 在关键节点（阶段切换、工具调用开始/完成、plan 生成等）发布事件
2. **Event Bus → WS Hub**: Hub 维护所有 WebSocket 连接，将事件广播给订阅者
3. **Event Bus → AgentStateTracker**: 同一事件被 state tracker 消费，维护实时状态快照
4. **REST API → SQLite**: 历史数据查询走现有 SQLite 存储（sessions、tool_log、task_ledger）
5. **SPA ← WS + REST**: 前端通过 WebSocket 接收实时事件，通过 REST API 查询历史数据

### 关键设计原则

- **Event Bus 是 in-process 的**，不引入 Redis/NATS 等外部依赖，保持 local-first
- **Event Bus 与 evolution hooks 独立**。现有 `evolution.Hook` 链不变，通过 adapter 桥接
- **go:embed** 打包前端静态文件，保持单二进制交付
- **Agent 侧改动最小化**：仅增加可选的 `DashboardEmitter` 接口回调

---

## 后端设计

### Event Bus (`internal/dashboard/eventbus.go`)

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
    Type      EventType              `json:"type"`
    Timestamp time.Time              `json:"timestamp"`
    SessionID string                 `json:"session_id,omitempty"`
    Data      map[string]interface{} `json:"data"`
}

type Bus struct {
    subscribers map[chan Event]struct{}
    mu          sync.RWMutex
    bufSize     int
}
```

- **Fan-out 广播**: `Publish(event)` 非阻塞发送给所有 subscriber，channel 满则跳过（慢消费者不阻塞生产者）
- **Subscribe/Unsubscribe**: 返回 `chan Event`，消费者自行读取
- **Buffer 大小默认 256**

### Agent 侧接口 (`internal/agent/dashboard_emitter.go`)

```go
// DashboardEmitter 定义在 agent 包内，避免依赖 dashboard 包
type DashboardEmitter interface {
    EmitPhaseStart(sessionID, phase string)
    EmitPhaseEnd(sessionID, phase string, durationMs int64)
    EmitToolStart(sessionID, toolName, input string)
    EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
}
```

在 `CognitiveAgent` 和 `Runtime` 中增加可选字段 `dashEmitter DashboardEmitter`。现有的 `registerSubtask` 调用前后各加一行 `EmitPhaseStart`/`EmitPhaseEnd`，工具执行前后加 `EmitToolStart`/`EmitToolEnd`。`dashEmitter == nil` 时跳过。

`EmitToolStart` 的 `input` 参数截断到 500 字符，避免大型 bash 命令或文件内容撑爆事件。截断在 `Emitter` 实现侧处理，接口调用方无需关心。

### Evolution Hook Bridge (`internal/dashboard/evolution_bridge.go`)

实现 `evolution.Hook` 接口，注册到 Engine，将 Reflection/Episode/ToolExec 事件转化为 dashboard Event 发布到 Bus。

### WebSocket Hub (`internal/dashboard/ws_hub.go`)

```go
type Hub struct {
    bus        *Bus
    clients    map[*Client]struct{}
    register   chan *Client
    unregister chan *Client
    mu         sync.RWMutex
}
```

- Hub 订阅 Event Bus，收到事件后 JSON 序列化广播给所有 WebSocket client
- 每个 Client 有独立的 write goroutine + send channel
- 心跳 ping/pong 保持连接活跃
- 客户端连接时发送当前 Agent 状态快照

### Agent State Tracker (`internal/dashboard/state_tracker.go`)

订阅 Event Bus，维护 agent 实时状态的内存视图：

```go
type AgentStateTracker struct {
    bus            *Bus
    mu             sync.RWMutex
    activeSessions map[string]*SessionState
    totalToday     int
    startedAt      time.Time
}

type SessionState struct {
    SessionID    string
    Channel      string
    CurrentPhase string
    CurrentTool  string
    PhaseStart   time.Time
    ToolsExecuted int
    ReplanCount   int
}
```

供 REST API `/api/agent/state` 查询。

### WebSocket 协议

```json
// Server → Client: 事件推送
{
  "type": "phase.start",
  "timestamp": "2026-04-19T15:30:00Z",
  "session_id": "abc123",
  "data": {
    "phase": "PLAN",
    "attempt": 1
  }
}
```

---

## REST API

在现有 `startHTTPServer` 基础上扩展，保持 stdlib `net/http`。

| 方法 | 路径 | 用途 | 数据源 |
|------|------|------|--------|
| `GET` | `/ws` | WebSocket 连接 | Event Bus → Hub |
| `GET` | `/api/agent/state` | Agent 当前状态快照 | AgentStateTracker |
| `GET` | `/api/sessions` | Session 列表（已有，增强） | SQLite `sessions` |
| `GET` | `/api/sessions/{id}/messages` | 单个 Session 的消息历史 | SQLite `messages` |
| `GET` | `/api/sessions/{id}/tools` | 单个 Session 的工具调用记录 | SQLite `tool_log` |
| `GET` | `/api/metrics/health` | 当前健康指标 | cogmetrics Collector |
| `GET` | `/` | SPA 入口 (index.html) | go:embed |
| `GET` | `/assets/*` | SPA 静态资源 | go:embed |

### `/api/agent/state` 响应示例

```json
{
  "status": "busy",
  "active_sessions": [
    {
      "session_id": "abc123",
      "channel": "telegram",
      "current_phase": "ACT",
      "current_tool": "bash",
      "phase_started_at": "2026-04-19T15:30:00Z",
      "tools_executed": 3,
      "replan_count": 0
    }
  ],
  "uptime_seconds": 3600,
  "total_sessions_today": 12
}
```

### HTTP Server 重构

```go
type HTTPServerDeps struct {
    DB           *store.DB
    Hub          *Hub
    StateTracker *AgentStateTracker
    Collector    *cogmetrics.Collector
    StaticFS     fs.FS
}

func startHTTPServer(addr string, deps HTTPServerDeps) { ... }
```

### 安全

- **V1**: Token-based 认证。配置 `dashboard.token`，请求需带 `Authorization: Bearer <token>` 或 `?token=<token>`（WebSocket 用 query param）
- **默认绑定 `127.0.0.1`**: 本地开发无需 token
- **远程使用**: 用户配置 `0.0.0.0` + token，或 SSH tunnel

---

## 前端设计

### 技术选型

| 选择 | 理由 |
|------|------|
| **Preact** (3KB) | React API 兼容，体积极小，适合嵌入 Go 二进制 |
| **Vite** | 快速 HMR，构建产出小 |
| **CSS Modules** | 无运行时开销 |
| **wouter** (~1.5KB) | 极轻量路由，Preact 兼容 |

V1 不引入 ECharts。后续进化趋势图表子项目时再添加。

### 目录结构

```
web/
├── package.json
├── vite.config.ts
├── index.html
└── src/
    ├── main.tsx
    ├── app.tsx
    ├── hooks/
    │   ├── useWebSocket.ts      # WS 连接管理 + 自动重连
    │   └── useAgentState.ts     # Agent 状态聚合
    ├── pages/
    │   ├── Overview.tsx          # Agent 状态总览
    │   ├── SessionDetail.tsx     # Session 详情（V1 预留）
    │   └── NotFound.tsx
    ├── components/
    │   ├── AgentStatus.tsx       # 当前状态卡片
    │   ├── PhaseTimeline.tsx     # 认知阶段时间线
    │   ├── ToolCallFeed.tsx      # 工具调用实时流
    │   ├── SessionList.tsx       # 活跃 session 列表
    │   └── Layout.tsx            # 页面布局
    ├── lib/
    │   ├── api.ts               # REST API 客户端
    │   └── types.ts             # TypeScript 类型
    └── styles/
        └── global.css           # 全局样式 + 深色主题
```

### Overview 页面布局

```
┌─────────────────────────────────────────────────┐
│  IronClaw Dashboard                  ● Connected │
├──────────┬──────────────────────────────────────┤
│          │                                       │
│  Nav     │  ┌─ Agent Status ─────────────────┐  │
│          │  │  Status: BUSY                   │  │
│  Overview│  │  Phase: ACT ▶ tool: bash        │  │
│  Sessions│  │  Session: abc123 (telegram)     │  │
│          │  └────────────────────────────────┘  │
│          │                                       │
│          │  ┌─ Phase Timeline ────────────────┐  │
│          │  │ PERCEIVE → PLAN → [ACT] → ...   │  │
│          │  │   120ms    450ms   running...    │  │
│          │  └────────────────────────────────┘  │
│          │                                       │
│          │  ┌─ Tool Call Feed ────────────────┐  │
│          │  │ 15:30:02 bash ✓ 230ms           │  │
│          │  │ 15:30:05 file_read ✓ 12ms       │  │
│          │  │ 15:30:08 bash ⏳ running...      │  │
│          │  └────────────────────────────────┘  │
│          │                                       │
├──────────┴──────────────────────────────────────┤
│  Sessions: 3 today │ Tools: 47 calls │ Uptime 2h│
└─────────────────────────────────────────────────┘
```

### WebSocket 管理 (`useWebSocket.ts`)

- 自动重连：exponential backoff（1s → 2s → 4s → ... → 30s max）
- 连接状态暴露给 UI（Connected / Reconnecting / Disconnected）
- 连接建立后先调 `GET /api/agent/state` 获取快照，再用 WS 事件增量更新
- Token 通过 URL query param：`ws://host:port/ws?token=xxx`

### 状态管理 (`useAgentState.ts`)

`useReducer` 驱动，不引入外部状态库：

```typescript
type AgentState = {
  status: 'idle' | 'busy'
  activeSessions: SessionState[]
  recentTools: ToolEvent[]     // 最近 100 条
  phaseHistory: PhaseEvent[]   // 当前 session 阶段历史
  connected: boolean
}
```

---

## Gateway 集成

### 初始化顺序

在现有 Gateway `New()` 的 scheduler 之后、`Start()` 中 channel 启动之前：

```
...existing init...
→ scheduler
→ dashboard (Event Bus + WS Hub + AgentStateTracker + HTTP server)  ← 新增
→ Start() → channels
```

### Gateway 结构体扩展

```go
type Gateway struct {
    // ...existing fields...
    dashboardBus    *dashboard.Bus
    dashboardHub    *dashboard.Hub
    stateTracker    *dashboard.AgentStateTracker
}
```

### 初始化逻辑 (`internal/gateway/init_dashboard.go`)

```go
func (gw *Gateway) initDashboard() error {
    if !gw.cfg.Dashboard.Enabled {
        return nil
    }

    gw.dashboardBus = dashboard.NewBus(256)
    gw.stateTracker = dashboard.NewAgentStateTracker(gw.dashboardBus)

    if gw.evoEngine != nil && gw.evoEngine.IsEnabled() {
        gw.evoEngine.RegisterHook(dashboard.NewEvolutionBridge(gw.dashboardBus))
    }

    emitter := dashboard.NewEmitter(gw.dashboardBus)
    gw.runtime.SetDashboardEmitter(emitter)
    if gw.cognitiveAgent != nil {
        gw.cognitiveAgent.SetDashboardEmitter(emitter)
    }

    gw.dashboardHub = dashboard.NewHub(gw.dashboardBus)
    go gw.dashboardHub.Run()

    // Register cogmetrics.Collector as an evolution hook so it receives
    // live data (currently it exists but is not wired at runtime).
    var collector *cogmetrics.Collector
    if gw.evoEngine != nil && gw.evoEngine.IsEnabled() {
        collector = cogmetrics.NewCollector()
        gw.evoEngine.RegisterHook(collector)
    }

    go dashboard.StartServer(gw.cfg.Dashboard, dashboard.HTTPServerDeps{
        DB:           gw.db,
        Hub:          gw.dashboardHub,
        StateTracker: gw.stateTracker,
        Collector:    collector,
        StaticFS:     dashboard.WebDistFS(),
    })

    return nil
}
```

### 包依赖关系

```
agent  ──定义──▶  DashboardEmitter (interface, 在 agent 包内)
                        ▲
dashboard ──实现──┘

dashboard ──依赖──▶  store, cogmetrics, evolution (Hook interface)
gateway   ──依赖──▶  dashboard, agent, evolution, store, ...
```

无循环依赖。`agent` 不知道 `dashboard` 的存在。

---

## 配置

Dashboard 取代现有的 `server` 配置块。现有 `server.enabled` + `server.addr` 改为 `dashboard.enabled` + `dashboard.addr`，原有的 `/health` 和 `/api/sessions` 端点迁移到 dashboard HTTP server 中。如果 dashboard 未启用，这些端点不可用（与现有 `server.enabled: false` 默认行为一致）。

```yaml
# ironclaw.yaml (替代原 server 配置)
dashboard:
  enabled: false
  addr: "127.0.0.1:8080"
  token: ""                # 为空且 addr 为 127.0.0.1 时不强制认证
```

---

## 构建

```makefile
.PHONY: web
web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=1 go build -tags fts5 -o ironclaw ./cmd/ironclaw
```

开发模式：Vite dev server (`:5173`) proxy API 请求到 Go (`:8080`)。

### go:embed

```go
//go:embed all:web/dist
var webDistFS embed.FS
```

---

## 新增 Go 依赖

| 包 | 用途 |
|----|------|
| `github.com/gorilla/websocket` | WebSocket 实现（stdlib 无内置 WS） |

仅此一个新 Go 依赖。

---

## 未来扩展路径

| 子项目 | 接入方式 |
|--------|---------|
| **Session 回放** | REST `GET /api/sessions/{id}/replay`，从 messages + tool_log + task_ledger 组合时间线；前端 `SessionReplay.tsx` |
| **进化趋势图表** | REST `GET /api/evolution/trends?days=30`，读取 trajectory JSONL 聚合；前端 `EvolutionCharts.tsx` + ECharts |
| **Memory 浏览器** | REST `GET/PUT /api/memory/*`，对接 `memory.Store`；前端 `MemoryBrowser.tsx` |
| **OpenTelemetry** | Event Bus 新增 OTel subscriber，将事件转为 spans 导出；`go.opentelemetry.io/otel` 作为可选依赖 |

每个子项目：新增 REST 端点 + 新增前端页面/组件 + 可选的 Event Bus 事件类型扩展。核心架构不变。

---

## 范围边界 (V1)

**包含:**
- Event Bus (in-process pub/sub)
- WebSocket Hub + 实时事件推送
- Agent State Tracker
- REST API: agent state, sessions, session messages, session tools, metrics health
- Preact SPA: Overview 页面（AgentStatus, PhaseTimeline, ToolCallFeed, SessionList）
- Token-based 认证
- go:embed 打包
- Makefile web 构建步骤

**不包含:**
- Session 回放 UI
- 进化趋势图表
- Memory 浏览器
- OpenTelemetry 集成
- HTTPS / TLS（用户自行反向代理或 SSH tunnel）
- 多用户权限管理
