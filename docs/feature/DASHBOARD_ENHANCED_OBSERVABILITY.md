# Web Dashboard V2 — 深度 Agent 观测增强

**日期**: 2026-04-20
**范围**: 扩展 `internal/dashboard/` + `internal/agent/` + `web/` 前端，从 4 个维度增强 Agent 运行时观测能力
**前置**: [WEB_DASHBOARD.md](WEB_DASHBOARD.md)（V1 实时监控基础设施）

## 概述

V1 Dashboard 实现了基础的 Agent 实时监控（认知阶段 + 工具调用），但存在**观测盲区多、事件体系不完整**的问题。具体表现为：

1. **事件空壳**：10 种事件类型中 5 种（`session.start`、`replan.start`、`plan.generated`、`task.update`、`agent.idle`）定义了常量但从未有代码发布
2. **事件重复**：EvolutionBridge 与 Emitter 对同一工具调用/阶段产生重复 `tool.end`/`phase.end` 事件，导致 StateTracker 计数膨胀
3. **执行路径静默**：Interceptor Chain 路径（`executeSubTaskViaChain`）和 Speculative Execution 命中路径不发射任何事件
4. **Token 不可见**：`RuntimeMetrics`（iteration/utilization/token/cache）仅推送到 TUI，Web Dashboard 无法观测
5. **认知深度不可见**：无法看到计划内容、replan 决策、observation 断言结果
6. **子代理不可见**：SubAgent 的 spawn/complete 生命周期和 Context 压缩完全不可观测

本次改动通过 4 个并行阶段全面解决上述问题，将 `DashboardEmitter` 接口从 **4 个方法扩展到 13 个**，新增 **5 种事件类型**，添加 **3 个前端组件**，修复 **3 个关键 bug**。

## 核心架构变更

### DashboardEmitter 接口（V1 → V2）

V1 接口仅覆盖阶段和工具事件：

```go
// V1: 4 methods
type DashboardEmitter interface {
    EmitPhaseStart(sessionID, phase string)
    EmitPhaseEnd(sessionID, phase string, durationMs int64)
    EmitToolStart(sessionID, toolName, input string)
    EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)
}
```

V2 扩展至 13 个方法，覆盖完整 Agent 生命周期：

```go
// V2: 13 methods
type DashboardEmitter interface {
    // ── V1 原有 ──
    EmitPhaseStart(sessionID, phase string)
    EmitPhaseEnd(sessionID, phase string, durationMs int64)
    EmitToolStart(sessionID, toolName, input string)
    EmitToolEnd(sessionID, toolName string, succeeded bool, durationMs int64)

    // ── Phase 1: 会话生命周期 ──
    EmitSessionStart(sessionID, channel string)
    EmitSessionEnd(sessionID string, succeeded bool, durationMs int64)

    // ── Phase 2: Token/LLM 指标 ──
    EmitMetricsUpdate(sessionID string, iteration, maxIter int, utilization float64,
        inputTokens, outputTokens, cacheCreate, cacheRead int64, model, provider string)

    // ── Phase 3: 认知深度 ──
    EmitPlanGenerated(sessionID string, taskCount int, complexity string, hasDirectReply bool)
    EmitReplanStart(sessionID string, attempt int, reason string)
    EmitObservationResult(sessionID string, passed, failed, total int, overallProgress float64)

    // ── Phase 4: 子代理 + 压缩 ──
    EmitSubAgentSpawn(sessionID, parentSessionID, agentName, task string)
    EmitSubAgentComplete(sessionID, agentName string, succeeded bool, durationMs int64)
    EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64)
}
```

`multiEmitter` 同步扩展所有 13 个方法的 fan-out 实现。`TUIEmitter` 对新增方法提供空实现（no-op stub）以满足接口约束。

### 事件类型（V1 → V2）

| 事件类型 | V1 状态 | V2 状态 | 数据字段 |
|----------|---------|---------|----------|
| `phase.start` | 正常 | 正常 | `phase` |
| `phase.end` | 正常 | 修复去重 | `phase`, `duration_ms`, `source?` |
| `tool.start` | 正常 | 正常 | `tool_name`, `input` (≤500 chars) |
| `tool.end` | 正常 | 修复去重 | `tool_name`, `succeeded`, `duration_ms`, `source?` |
| `plan.generated` | **空壳** | **已激活** | `task_count`, `complexity`, `has_direct_reply` |
| `replan.start` | **空壳** | **已激活** | `attempt`, `reason` |
| `session.start` | **空壳** | **已激活** | `channel` |
| `session.end` | 仅 Bridge | 正常 | `succeeded`, `duration_ms`, `source?` |
| `metrics.update` | — | **新增** | `iteration`, `max_iterations`, `utilization`, `input_tokens`, `output_tokens`, `cache_create`, `cache_read`, `model`, `provider` |
| `observation.result` | — | **新增** | `passed`, `failed`, `total`, `overall_progress` |
| `subagent.spawn` | — | **新增** | `parent_session_id`, `agent_name`, `task` (≤200 chars) |
| `subagent.complete` | — | **新增** | `agent_name`, `succeeded`, `duration_ms` |
| `context.compress` | — | **新增** | `reason`, `layers_run`, `before_pct`, `after_pct` |
| `task.update` | 空壳 | 未激活（保留） | — |
| `agent.idle` | 空壳 | 未激活（保留） | — |

### 数据流（V2 增量）

```
┌─────────────────────────────────────────────────────────────────┐
│                      Agent Runtime                               │
│                                                                   │
│  CognitiveAgent                    Runtime (simple)               │
│  ┌──────────────┐                  ┌──────────────┐              │
│  │ EmitSession  │                  │ EmitSession  │              │
│  │ Start/End    │                  │ Start/End    │              │
│  │ EmitPlan     │  ★ NEW          │ EmitMetrics  │  ★ NEW      │
│  │ Generated    │                  │ Update       │              │
│  │ EmitReplan   │                  └──────┬───────┘              │
│  │ Start        │                         │                      │
│  │ EmitObserve  │                         │                      │
│  │ Result       │                         │                      │
│  └──────┬───────┘                         │                      │
│         │                                 │                      │
│  SubAgentManager          PipelineContextManager                 │
│  ┌──────────────┐         ┌──────────────┐                      │
│  │EmitSubAgent  │ ★ NEW   │EmitContext   │ ★ NEW               │
│  │Spawn/Complete│         │Compress      │                      │
│  └──────┬───────┘         └──────┬───────┘                      │
│         │                        │                               │
│         ▼                        ▼                               │
│  ┌───────────────────────────────────────┐                      │
│  │         DashboardEmitter (13 methods) │                      │
│  └───────────────┬───────────────────────┘                      │
└──────────────────┼──────────────────────────────────────────────┘
                   ▼
       ┌───────────────────────┐
       │      Event Bus        │
       │  (15 event types)     │
       └──────┬────────┬───────┘
              │        │
     ┌────────┘        └────────┐
     ▼                          ▼
┌──────────────────┐   ┌──────────────────┐
│AgentStateTracker │   │  WebSocket Hub   │
│ ★ 扩展字段:       │   │  → 前端 3 新组件  │
│ · token 指标      │   │  · TokenUsage    │
│ · plan/观测       │   │  · Cognitive     │
│ · sub-agent      │   │    Summary       │
│ · 压缩计数        │   │  · SubAgentList  │
└──────────────────┘   └──────────────────┘
```

## Phase 1: Bug 修复

### 1.1 EvolutionBridge 事件去重

**问题**：当 evolution engine 启用时，`EvolutionBridge.OnToolExecuted` 发布 `tool.end` 与 `Emitter.EmitToolEnd` 重复；`OnReflectionComplete` 发布 `phase.end` 与 `EmitPhaseEnd` 重复。StateTracker 对每个 `tool.end` 执行 `ToolsExecuted++`，导致计数膨胀 2x。

**修复**：EvolutionBridge 发布的所有事件添加 `"source": "evolution"` 标记。StateTracker 在处理 `EventToolEnd` 和 `EventPhaseEnd` 时检查该字段，跳过 evolution 源事件的状态变更。WebSocket 客户端仍接收两份事件（用于前端可选的 enrichment 数据如 confidence），但核心状态不重复计算。

```go
// evolution_bridge.go — 所有 Publish 调用添加 source 字段
Data: map[string]any{
    "tool_name":   event.ToolName,
    "succeeded":   event.Succeeded,
    "duration_ms": event.DurationMs,
    "source":      "evolution",  // ★ 新增
}

// state_tracker.go — 跳过 evolution 源
case EventToolEnd:
    if ev.Data["source"] != "evolution" {
        ss := t.getOrCreate(sid)
        ss.CurrentTool = ""
        ss.ToolsExecuted++
    }
```

### 1.2 静默执行路径补全

**问题 A**：`Executor.executeSubTaskViaChain`（cognitive ACT + interceptor chain 路径）从未调用 `EmitToolStart/End`，通过拦截器链执行的工具在 Dashboard 上不可见。

**修复**：在 interceptor chain Execute 调用前后添加 emit 调用，覆盖成功和错误路径。

**问题 B**：`Runtime.executeToolCall` 中 speculative execution 命中时直接 return，跳过所有 Emit 调用。

**修复**：在 speculative 结果使用前后添加 `EmitToolStart` 和 `EmitToolEnd`。

### 1.3 SessionState.Channel 填充

**问题**：`SessionState.Channel` 字段始终为空——没有代码设置它。

**修复**：
1. `DashboardEmitter` 新增 `EmitSessionStart(sessionID, channel string)` 和 `EmitSessionEnd`
2. `CognitiveAgent.HandleMessage` 入口和出口发射 session 事件
3. `Runtime.HandleMessage` 入口发射 `EmitSessionStart`
4. `StateTracker` 处理 `EventSessionStart` 时设置 `ss.Channel`

## Phase 2: Token/LLM 指标观测

### 2.1 后端

新增 `EventMetricsUpdate` 事件和 `EmitMetricsUpdate` 方法。`Runtime` 在每次 agent 迭代中，已有的 `metricsEmitter.SendMetrics(m)` 之后，同步调用：

```go
if r.dashEmitter != nil {
    r.dashEmitter.EmitMetricsUpdate(sess.ID,
        m.Iteration, m.MaxIter, m.Utilization,
        m.InputTokens, m.OutputTokens, m.CacheCreate, m.CacheRead,
        m.Model, m.Provider)
}
```

StateTracker `SessionState` 新增 9 个字段：

```go
Iteration    int     `json:"iteration,omitempty"`
MaxIter      int     `json:"max_iterations,omitempty"`
Utilization  float64 `json:"utilization,omitempty"`
InputTokens  int64   `json:"input_tokens,omitempty"`
OutputTokens int64   `json:"output_tokens,omitempty"`
CacheCreate  int64   `json:"cache_create,omitempty"`
CacheRead    int64   `json:"cache_read,omitempty"`
Model        string  `json:"model,omitempty"`
Provider     string  `json:"provider,omitempty"`
```

### 2.2 前端 TokenUsage 组件

新增 `web/src/components/TokenUsage.tsx`，展示：

| 区域 | 内容 |
|------|------|
| Model + Provider | 模型名称 + provider badge |
| Iteration | 当前迭代 / 最大迭代 |
| Context Utilization | 进度条，颜色编码：<60% 绿、<80% 黄、≥80% 红 |
| Token Counts | Input / Output / Total（自动 k/M 格式化） |
| Cache Stats | Created / Read / Hit Rate（仅当 cache > 0 时显示） |

## Phase 3: 认知深度观测

### 3.1 后端

新增 3 个 emitter 方法和对应事件，在 `cognitive.go` 的认知循环中注入：

**计划生成**（`plan.generated`）——在 `ca.planner.Run` 成功返回后：

```go
if ca.dashEmitter != nil {
    ca.dashEmitter.EmitPlanGenerated(sess.ID, len(plan.SubTasks),
        string(state.Goal.Complexity), plan.DirectReply != "")
}
```

**重规划启动**（`replan.start`）——在 attempt > 0 的循环入口：

```go
if ca.dashEmitter != nil {
    reason := "low_confidence"
    if reflection != nil && reflection.SuggestedAdjustment != "" {
        reason = "adjustment: " + truncateStr(reflection.SuggestedAdjustment, 100)
    }
    ca.dashEmitter.EmitReplanStart(sess.ID, attempt, reason)
}
```

**观测结果**（`observation.result`）——在 `ca.observer.Run` 返回后：

```go
if ca.dashEmitter != nil {
    ca.dashEmitter.EmitObservationResult(sess.ID,
        obsResult.SuccessCount, obsResult.FailureCount,
        obsResult.SuccessCount+obsResult.FailureCount, obsResult.OverallProgress)
}
```

StateTracker `SessionState` 新增 5 个字段：

```go
PlanTaskCount     int     `json:"plan_task_count,omitempty"`
PlanComplexity    string  `json:"plan_complexity,omitempty"`
ObservationPassed int     `json:"observation_passed,omitempty"`
ObservationFailed int     `json:"observation_failed,omitempty"`
OverallProgress   float64 `json:"overall_progress,omitempty"`
```

### 3.2 前端 CognitiveSummary 组件

新增 `web/src/components/CognitiveSummary.tsx`，展示：

| 区域 | 内容 |
|------|------|
| Plan | subtask 数量 + 复杂度 badge（simple/medium/complex） |
| Observations | passed/failed 计数 + 进度条 + 百分比 |
| Replan badge | replan 次数（仅 > 0 时显示） |

组件仅在有任何数据时渲染（无数据返回 null 避免空白区域）。

## Phase 4: 子代理 + Context 压缩观测

### 4.1 SubAgent 生命周期

`SubAgentManager` 新增 `dashEmitter DashboardEmitter` 字段和 `SetDashboardEmitter` 方法。

在 `Spawn` 方法中注入：

1. **Spawn 前**：发射 `subagent.spawn` 事件（含 parent session ID、agent name、task 预览）
2. **传递 emitter**：`subRuntime.SetDashboardEmitter(m.dashEmitter)` — 子代理的工具调用也可见
3. **Spawn 后**：发射 `subagent.complete` 事件（含成功/失败、耗时）

Task 字段截断为 200 字符，防止事件膨胀。

Gateway 集成（`init_dashboard.go`）：

```go
if gw.subAgentMgr != nil {
    gw.subAgentMgr.SetDashboardEmitter(emitter)
}
```

### 4.2 Context 压缩

`PipelineContextManager` 新增 `dashEmitter DashboardEmitter` 字段和 `SetDashboardEmitter` 方法。

在 `Compress` 方法中，管道运行后发射：

```go
if cm.dashEmitter != nil {
    afterUtil := cm.Utilization(sess, systemPrompt)
    cm.dashEmitter.EmitContextCompress(sess.ID, "proactive",
        len(cm.pipeline.layers), util, afterUtil)
}
```

在 `ReactiveCompress`（413 触发）中，以 `"reactive_413"` 为 reason 发射。

Gateway 集成：

```go
if gw.contextMgr != nil {
    gw.contextMgr.SetDashboardEmitter(emitter)
}
```

### 4.3 StateTracker 扩展

StateSnapshot 新增：

```go
type StateSnapshot struct {
    // ...existing...
    ActiveSubAgents   []SubAgentState `json:"active_subagents,omitempty"`
    CompressionEvents int             `json:"compression_events,omitempty"`
}

type SubAgentState struct {
    SessionID       string    `json:"session_id"`
    ParentSessionID string    `json:"parent_session_id"`
    AgentName       string    `json:"agent_name"`
    Task            string    `json:"task,omitempty"`
    StartedAt       time.Time `json:"started_at"`
}
```

事件处理：
- `subagent.spawn` → 添加到 `activeSubAgents` map
- `subagent.complete` → 从 `activeSubAgents` 移除
- `context.compress` → `compressionCount++`

### 4.4 前端 SubAgentList 组件

新增 `web/src/components/SubAgentList.tsx`，展示：

| 区域 | 内容 |
|------|------|
| Header | "Sub-Agents & Context" + 压缩计数 badge |
| Agent 列表 | 每行：agent name + task 预览 + 状态 badge（running/done/failed + 耗时） |
| 空状态 | "No sub-agent activity" |

组件仅在有子代理活动或压缩事件时渲染。

## 前端状态管理（V2）

`useAgentState` hook 的 `AgentState` 接口从 8 个字段扩展为 13 个：

```typescript
interface AgentState {
  // V1 原有
  status: 'idle' | 'busy'
  activeSessions: SessionState[]
  recentTools: ToolEvent[]
  phaseHistory: PhaseEvent[]
  connected: boolean
  totalSessions: number
  uptimeSeconds: number
  replanCount: number
  error: string | null

  // V2 新增
  metrics: MetricsState | null          // Phase 2
  planInfo: { taskCount; complexity } | null  // Phase 3
  observationResult: { passed; failed; total; progress } | null  // Phase 3
  subAgents: SubAgentEvent[]            // Phase 4
  compressionCount: number              // Phase 4
}
```

Reducer 新增 8 个事件处理分支：`metrics.update`、`plan.generated`、`replan.start`、`observation.result`、`subagent.spawn`、`subagent.complete`、`context.compress`、`session.start`。

`session.start` 和 `session.end` 事件触发完整的状态重置（phaseHistory、planInfo、observationResult、subAgents、compressionCount、replanCount 全部清零）。

## Overview 页面布局（V2）

```
┌──────────────────────────────────┐
│ AgentStatus                      │  ← V1
├──────────────────────────────────┤
│ TokenUsage                       │  ← ★ Phase 2
├──────────────────────────────────┤
│ PhaseTimeline                    │  ← V1
├──────────────────────────────────┤
│ CognitiveSummary                 │  ← ★ Phase 3
├──────────────────────────────────┤
│ ToolCallFeed                     │  ← V1
├──────────────────────────────────┤
│ SubAgentList                     │  ← ★ Phase 4
├──────────────────────────────────┤
│ SessionList                      │  ← V1
├──────────────────────────────────┤
│ Footer (uptime + WS status)      │  ← V1
└──────────────────────────────────┘
```

## Gateway 初始化顺序（V2）

```
Bus(256) → StateTracker → EvolutionBridge* → cogCollector*
    → Emitter
        → runtime.SetDashboardEmitter
        → cognitiveAgent.SetDashboardEmitter*
        → subAgentMgr.SetDashboardEmitter*    ★ NEW
        → contextMgr.SetDashboardEmitter*      ★ NEW
    → Hub → NewServer → ListenAndServe (goroutine)

* 仅在对应组件存在时执行
```

## 测试

36 个测试用例，覆盖全部后端组件（V1 22 个 → V2 36 个）：

### Phase 1 新增测试

| 测试 | 验证内容 |
|------|---------|
| `TestEvolutionBridgeSourceField` | EvolutionBridge 事件包含 `source: "evolution"` |
| `TestStateTrackerSkipsEvolutionToolEnd` | evolution 源 `tool.end` 不增加 ToolsExecuted |
| `TestStateTrackerSkipsEvolutionPhaseEnd` | evolution 源 `phase.end` 不清除 CurrentPhase |
| `TestStateTrackerSessionStartSetsChannel` | `session.start` 正确设置 Channel 字段 |
| `TestEmitSessionStart` | `EmitSessionStart` 事件格式正确 |
| `TestEmitSessionEnd` | `EmitSessionEnd` 事件包含 succeeded 和 duration_ms |

### Phase 2 新增测试

| 测试 | 验证内容 |
|------|---------|
| `TestEmitterMetricsUpdate` | 所有 9 个指标字段正确发布 |
| `TestStateTrackerMetricsUpdate` | metrics 事件正确更新 SessionState 字段 |

### Phase 3 新增测试

| 测试 | 验证内容 |
|------|---------|
| `TestEmitterPlanGenerated` | task_count + complexity + has_direct_reply 正确 |
| `TestEmitterReplanStart` | attempt + reason 正确 |
| `TestEmitterObservationResult` | passed + failed + total + overall_progress 正确 |
| `TestStateTrackerPlanGenerated` | plan 事件更新 PlanTaskCount + PlanComplexity |
| `TestStateTrackerReplanStart` | replan 事件递增 ReplanCount |
| `TestStateTrackerObservationResult` | observation 事件更新 passed/failed/progress |

### Phase 4 新增测试

| 测试 | 验证内容 |
|------|---------|
| `TestEmitterSubAgentSpawn` | parent_session_id + agent_name + task 正确 |
| `TestEmitterSubAgentSpawnTruncatesTask` | 超长 task 截断为 200 字符 |
| `TestEmitterSubAgentComplete` | agent_name + succeeded + duration_ms 正确 |
| `TestEmitterContextCompress` | reason + layers_run + before_pct + after_pct 正确 |
| `TestStateTrackerSubAgentLifecycle` | spawn 添加 / complete 移除 activeSubAgents |
| `TestStateTrackerCompressionCount` | compression 事件递增计数器 |

## 涉及文件

### 新增文件

| 文件 | 说明 |
|------|------|
| `web/src/components/TokenUsage.tsx` | Token 用量和 LLM 指标面板 |
| `web/src/components/CognitiveSummary.tsx` | 计划/观测/Replan 认知摘要面板 |
| `web/src/components/SubAgentList.tsx` | 子代理活动和上下文压缩面板 |

### 修改文件

| 文件 | 变更 |
|------|------|
| `internal/agent/dashboard_emitter.go` | `DashboardEmitter` 接口 4→13 方法；`multiEmitter` 同步扩展 |
| `internal/agent/cognitive.go` | 添加 EmitSessionStart/End、EmitPlanGenerated、EmitReplanStart、EmitObservationResult |
| `internal/agent/runtime.go` | 添加 EmitSessionStart、EmitMetricsUpdate |
| `internal/agent/act.go` | `executeSubTaskViaChain` 添加 EmitToolStart/End |
| `internal/agent/concurrent.go` | speculative 路径添加 EmitToolStart/End |
| `internal/agent/subagent.go` | SubAgentManager 添加 dashEmitter 字段 + spawn/complete 事件 |
| `internal/agent/context_manager.go` | PipelineContextManager 添加 dashEmitter + Compress/ReactiveCompress 事件 |
| `internal/channel/tui/emitter.go` | TUIEmitter 新增 9 个 no-op 方法满足接口 |
| `internal/dashboard/eventbus.go` | 新增 5 个事件类型常量 |
| `internal/dashboard/emitter.go` | 新增 9 个 Emitter 方法实现 + maxTaskLen 常量 |
| `internal/dashboard/evolution_bridge.go` | 所有事件 Data 添加 `"source": "evolution"` |
| `internal/dashboard/state_tracker.go` | SessionState 新增 14 个字段；SubAgentState 结构体；8 个新事件处理分支 |
| `internal/gateway/init_dashboard.go` | 新增 subAgentMgr + contextMgr 的 emitter 注入 |
| `internal/gateway/gateway.go` | Gateway 结构体新增 contextMgr 字段 |
| `internal/gateway/init_multiagent.go` | SubAgentManager 创建后设置 dashEmitter |
| `web/src/lib/types.ts` | EventType 新增 5 种；SessionState + SubAgentEvent 类型扩展 |
| `web/src/hooks/useAgentState.ts` | AgentState 新增 5 个字段；reducer 新增 8 个事件分支 |
| `web/src/pages/Overview.tsx` | 引入并放置 3 个新组件 |
| `internal/dashboard/emitter_test.go` | 新增 8 个测试 |
| `internal/dashboard/state_tracker_test.go` | 新增 8 个测试 |
| `internal/dashboard/evolution_bridge_test.go` | 新增 source 字段验证测试 |

## 后续扩展方向

| 功能 | 扩展方式 |
|------|---------|
| Session 事件回放 | 持久化 bus 事件到 SQLite + 新增 `/api/sessions/{id}/events` 端点 |
| Token 用量历史图表 | 累计 metrics 事件到时序数组 + ECharts 折线图 |
| Agent 树形视图 | 利用 `parent_session_id` 构建 parent → child 层级关系 + 前端树组件 |
| Context 压缩曲线 | 存储 `before_pct`/`after_pct` 序列 + 面积图展示压缩效果 |
| 工具执行详情 | EmitToolEnd 增加 output 预览 + error message 字段 |
| Memory 操作观测 | 新增 `memory.add`/`memory.update`/`memory.delete` 事件类型 |
| OpenTelemetry 导出 | 在 Emitter 中并行导出 OTLP spans，接入 Jaeger/Grafana |
