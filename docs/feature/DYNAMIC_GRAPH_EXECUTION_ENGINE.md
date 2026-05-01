# 动态图执行引擎（Dynamic Graph Execution Engine）

**日期**: 2026-05-01  
**范围**: `internal/agent/graph_types.go` + `graph_engine.go` + `graph_nodes.go` + `graph_node_adapters.go` + `event_store.go` + `heartbeat.go` + `synthesize.go` + `internal/gateway/init_graph.go` + `internal/store/migrations/020_execution_events.sql`

## 概述

IronClaw 原有的认知循环是一个**固定五阶段线性序列**：PERCEIVE → PLAN → ACT → OBSERVE → REFLECT。这个设计在大多数任务上工作良好，但存在几个结构性限制：

| 问题 | 原因 |
|------|------|
| 所有任务走同一路径，无法按复杂度分流 | 固定序列，无条件路由 |
| 阶段间状态通过 `CognitiveState` 大对象传递，耦合重 | 没有独立的状态快照机制 |
| 执行历史不可 replay，故障后无法重建状态 | 内存状态，无持久化事件日志 |
| 无法在不重启的情况下扩展新阶段 | 阶段硬编码在 `cognitive.go` 的主循环里 |

本次改动引入**动态图执行引擎**，将执行路径从固定序列改为有向图，节点之间通过条件边路由，状态通过 append-only 事件日志持久化。

## 架构设计

### 核心概念

```
ExecutionGraph
  ├── 节点（GraphNode）：每个阶段是一个独立节点，实现 Execute(ctx, GraphState) NodeResult
  ├── 边（EdgeCondition）：func(NodeResult) NodeType，决定下一个节点
  └── 引擎（GraphEngine）：驱动节点执行 → 记录事件 → 路由 → 循环
```

### 执行路径分流

引擎在收到用户消息时，根据输入特征选择三条路径之一：

| 路径 | 触发条件 | 节点序列 |
|------|---------|---------|
| `PathLightweight` | 输入 < 100 字且无换行 | NodeAct → NodeTerminate |
| `PathStandard` | 默认 | NodeAct → NodeObserve → NodeTerminate |
| `PathDeep` | 含 plan/analyze/implement/refactor 等关键词 | NodePerceive → NodePlan → NodeAct → NodeObserve → NodeReflect → NodeTerminate |

### 节点类型

```go
const (
    NodePerceive   NodeType = "perceive"
    NodePlan       NodeType = "plan"
    NodeAct        NodeType = "act"
    NodeObserve    NodeType = "observe"
    NodeReflect    NodeType = "reflect"
    NodeReplan     NodeType = "replan"
    NodeSynthesize NodeType = "synthesize"
    NodeTerminate  NodeType = "terminate"
)
```

### 状态线程（State Threading）

各节点之间通过 `graphNodePayload` JSON 传递状态，序列化进每个 `GraphEvent.OutputSnapshot`：

```go
type graphNodePayload struct {
    CogState     *CognitiveState    `json:"cog_state,omitempty"`
    TaskPlan     *TaskPlan          `json:"task_plan,omitempty"`
    Observations []Observation      `json:"observations,omitempty"`
    ObsResult    *ObservationResult `json:"obs_result,omitempty"`
    Reflection   *Reflection        `json:"reflection,omitempty"`
}
```

每个节点从 `GraphState.Events` 里反向扫描最近一个非空 snapshot，反序列化出所需字段，执行后把更新后的 payload 写入 `NodeResult.Output`。

### 事件溯源（Event Sourcing）

每次节点执行后，引擎向 `ExecutionEventStore` 追加一条 `GraphEvent`：

```sql
CREATE TABLE execution_events (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    node_type       TEXT NOT NULL,
    transitioned_to TEXT,
    execution_path  TEXT NOT NULL,
    input_snapshot  TEXT,
    output_snapshot TEXT,
    metadata        TEXT,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

`GetLatestState` 从事件列表重建 `GraphState`，支持会话恢复和审计。

### 节点适配器（NodeDeps）

`CognitiveAgent` 的私有阶段对象通过 `BuildNodeDeps` 暴露给图引擎：

```go
type NodeDeps struct {
    Perceiver *Perceiver
    Planner   *Planner
    Executor  *Executor
    Observer  *Observer
    Reflector *Reflector
    Sessions  *session.Manager
    Channel   channel.Channel
}
```

`BuildGraphWithDeps(deps, userMsg, userID)` 组装完整的带真实 LLM 逻辑的图，每次请求构建一个新实例（因为 Channel 是 per-request 的）。

### 心跳调度器（HeartbeatScheduler）

```go
type HeartbeatConfig struct {
    Interval time.Duration
    Enabled  bool
}
```

`HeartbeatScheduler` 以固定间隔向 `Ticks()` channel 发送 tick，使 agent 可以在无用户触发的情况下主动执行任务（如定期检查、自我评估）。非阻塞投递，慢消费者自动丢弃。

### 技能合成节点（SynthesizeNode）

`NodeSynthesize` 在图执行结束前，从 `GraphState.Events` 里提取所有 `NodeAct` 事件，生成 SKILL.md 草稿写入 `~/.IronClaw/skills/synthesized/`。需要至少 2 个 Act 步骤才触发合成，之后无论成功与否都路由到 `NodeTerminate`。

## 与现有架构的关系

图引擎作为 Gateway 的第三种 agent 模式（`mode: graph`）接入，与 `simple` 和 `cognitive` 并列：

```go
// internal/gateway/gateway.go
switch gw.currentMode {
case "graph":
    return gw.handleGraphMessage(ctx, ch, msg)
case "cognitive":
    return gw.cognitiveAgent.HandleMessage(ctx, ch, msg)
default:
    return gw.runtime.HandleMessage(ctx, ch, msg)
}
```

`handleGraphMessage` 在 `graphEventStore` 或 `cognitiveAgent` 为 nil 时自动 fallback 到 `runtime`，保持向后兼容。

### 输出提取

`ExtractFinalAnswer(snapshot string) string` 从最后一个事件的 OutputSnapshot 里提取人类可读回复：

1. 优先取 `Reflection.FinalAnswer`
2. 回退到 `CogState.RecentHistory` 最后一条 assistant 消息
3. 两者都空则 fallback 到 runtime

## 配置

在 Feature Registry 注册为 Tier 3（默认关闭，需显式启用）：

```yaml
features:
  graph:
    enabled: true
```

启用后通过 `/mode graph` 切换到图执行模式。

## 文件清单

| 文件 | 职责 |
|------|------|
| `internal/agent/graph_types.go` | 核心类型：NodeType、ExecutionPath、GraphEvent、GraphState、GraphNode 接口、ExecutionGraph |
| `internal/agent/graph_engine.go` | GraphEngine 主循环：执行节点 → 记录事件 → 路由 → 终止判断 |
| `internal/agent/graph_nodes.go` | FuncNode 适配器 + BuildDefaultGraph（stub 节点，用于测试） |
| `internal/agent/graph_node_adapters.go` | NodeDeps、graphNodePayload、6 个真实节点构造函数、BuildGraphWithDeps、ExtractFinalAnswer |
| `internal/agent/event_store.go` | ExecutionEventStore 接口 + SQLiteExecutionEventStore 实现 |
| `internal/agent/heartbeat.go` | HeartbeatScheduler：tick 生成、Start/Stop、TriggerNow |
| `internal/agent/synthesize.go` | SynthesizeNode：从 Act 事件提取步骤 → 生成 SKILL.md 草稿 |
| `internal/agent/cognitive.go` | 新增 BuildNodeDeps 方法，暴露私有阶段对象 |
| `internal/gateway/init_graph.go` | initGraphEngine + handleGraphMessage（含 fallback 逻辑） |
| `internal/gateway/gateway.go` | 新增 graphEventStore/heartbeat 字段，switch 路由到 handleGraphMessage |
| `internal/gateway/features.go` | 注册 "graph" feature（Tier 3，默认 false） |
| `internal/store/migrations/020_execution_events.sql` | execution_events 表 + 两个索引 |
