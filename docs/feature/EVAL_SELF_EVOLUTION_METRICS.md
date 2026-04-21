# 评测框架自进化指标增强

**日期**: 2026-04-21
**范围**: EvalResult 自进化指标（ToolExecStats、EpisodeReward）+ CogMetrics 认知健康集成 + 上下文压缩追踪 + 记忆评测 Live Store 联通

---

## 概述

IronClaw 拥有完整的自进化系统（`internal/evolution/`），包括奖励函数、策略优化、偏好学习。但原有评测框架（`internal/eval/`）在运行 Live 评测时，这些维度的数据既没有被捕获，也没有体现在 `EvalResult` 和 `SuiteResult` 中，导致以下问题：

| 问题 | 影响 |
|------|------|
| 无工具使用统计 | 无法量化 Agent 的工具调用效率和成功率 |
| 无 Episode 奖励 | 无法判断进化引擎对单次任务的奖励评估 |
| 无上下文压缩记录 | 无法感知长任务中 context 压缩对质量的影响 |
| 认知健康指标孤立 | cogmetrics.Collector 仅在 dashboard 开启时才初始化，eval 模式（dashboard 关闭）完全缺失 |
| 记忆评测与 Agent 断链 | fixtures_memory.go 创建的是独立的 FileMemoryStore，Agent 的 PERCEIVE 阶段读取的是 gateway 的 store，两者是不同实例 |

本次升级分三个方向补齐这些缺口，使评测结果能够完整反映 Agent 在自进化、认知健康和记忆检索三个维度的实际表现。

---

## 方向一：EvalResult 自进化指标增强

### 新增类型

**文件**: `internal/eval/harness.go`

```go
// ToolExecStat 聚合单次任务中某个工具的调用统计信息。
type ToolExecStat struct {
    ToolName        string  `json:"tool_name"`
    CallCount       int     `json:"call_count"`
    SuccessCount    int     `json:"success_count"`
    FailCount       int     `json:"fail_count"`
    SuccessRate     float64 `json:"success_rate"`
    AvgDurationMs   float64 `json:"avg_duration_ms"`
    TotalDurationMs int64   `json:"total_duration_ms"`
}

// CompressionEvent 记录一次上下文压缩事件。
type CompressionEvent struct {
    Reason    string  `json:"reason"`
    LayersRun int     `json:"layers_run"`
    BeforePct float64 `json:"before_pct"`
    AfterPct  float64 `json:"after_pct"`
}
```

### EvalResult 新增字段

```go
type EvalResult struct {
    // ... 已有字段 ...

    // 工具调用统计（按工具聚合）
    ToolExecStats []ToolExecStat `json:"tool_exec_stats,omitempty"`

    // 本次任务的 episode 奖励（由进化引擎的奖励函数计算）
    EpisodeReward float64 `json:"episode_reward,omitempty"`

    // 上下文压缩次数及详情
    CompressionCount  int                `json:"compression_count,omitempty"`
    CompressionEvents []CompressionEvent `json:"compression_events,omitempty"`
}
```

### SuiteResult 新增字段

```go
type SuiteResult struct {
    // ... 已有字段 ...

    // 整个 suite 执行结束后的认知健康快照。
    // 仅在进化引擎开启时填充（Live 模式），干跑时为 nil。
    CogHealth *cogmetrics.HealthReport `json:"cog_health,omitempty"`
}
```

### EpisodeReward 计算规则

`EpisodeReward` 由 `populateFromEvolution` 中无条件计算（不依赖进化引擎是否开启）：

```
EpisodeReward = AssertionPassRate * 0.4
              + (1 - ReplanCount/maxReplans) * 0.3
              + SuccessBonus * 0.3
```

- `AssertionPassRate`：OBSERVE 断言通过率
- `ReplanCount / maxReplans`：重规划率惩罚（越少越好）
- `SuccessBonus`：任务最终成功为 1.0，失败为 0.0

当进化引擎开启时，优先使用进化引擎的实际 `EpisodeEvent` 数据覆盖兜底计算值。

### ToolExecStats 聚合逻辑

`EvalHook` 订阅进化引擎的 `ToolExecEvent`，按 `ToolName` 分组聚合：

```go
for _, ev := range events {
    stat := statsMap[ev.ToolName]
    stat.CallCount++
    stat.TotalDurationMs += ev.DurationMs
    if ev.Success {
        stat.SuccessCount++
    } else {
        stat.FailCount++
    }
}
// 计算 SuccessRate 和 AvgDurationMs
```

---

## 方向二：CogMetrics 认知健康 + 压缩追踪集成

### 问题背景

原有初始化路径：

```
gateway.New
  └─ initDashboard（仅 dashboard.enabled=true 时）
       └─ cogmetrics.NewCollector()
            └─ evoEngine.RegisterHook(collector)
```

Eval 模式强制关闭 dashboard（避免端口冲突），导致 `cogCollector` 永远为 nil，`HealthReport` 无法生成。

### 解决方案：`init_cogmetrics.go`

**新文件**: `internal/gateway/init_cogmetrics.go`

独立出 `initCogMetrics()` 方法，在 gateway 初始化流程中**无条件**（在 `evoEngine` 就绪后）调用，使 CogMetrics 不再依赖 dashboard：

```go
func (gw *Gateway) initCogMetrics() {
    if gw.cogCollector != nil {
        return // 已由 initDashboard 创建，幂等
    }
    if gw.evoEngine == nil || !gw.featureEnabled("evolution") {
        return
    }
    gw.cogCollector = cogmetrics.NewCollector()
    gw.evoEngine.RegisterHook(gw.cogCollector)
}
```

`initDashboard` 改为调用 `gw.initCogMetrics()` 而非直接创建，确保 collector 实例全局唯一。

### CogHealthCaptor 接口

**文件**: `internal/eval/harness.go`

```go
// CogHealthCaptor 由能够返回认知健康报告的 runner 实现（可选）。
type CogHealthCaptor interface {
    CaptureCogHealth() *cogmetrics.HealthReport
}
```

`RunSuite`/`RunSuiteWithOptions` 在收集完所有任务结果后，类型断言：

```go
if chc, ok := runner.(CogHealthCaptor); ok {
    suite.CogHealth = chc.CaptureCogHealth()
}
```

### AssertionPassRate 上报

每次任务执行完成后，`cognitive_runner.go` 主动将断言通过率上报到 cogmetrics collector：

```go
if r.cogCollector != nil {
    r.cogCollector.RecordAssertionRate(result.AssertionPassRate)
}
```

确保 `HealthReport.AssertionPassRate` 在 eval 结束时有实际数据。

### 上下文压缩追踪

**文件**: `internal/eval/eval_hook.go`

`EvalHook` 新增压缩事件存储及接口：

```go
type EvalHook struct {
    // ...
    compressions map[string][]CompressionEvent
}

func (h *EvalHook) RecordCompression(sessionID, reason string,
    layersRun int, beforePct, afterPct float64)

func (h *EvalHook) GetCompressions(sessionID string) []CompressionEvent
```

**文件**: `internal/eval/cognitive_runner.go`

`compressionAdapter` 实现 `agent.DashboardEmitter`，将压缩事件路由到 `EvalHook`：

```go
type compressionAdapter struct {
    hook      *EvalHook
    sessionID func() string
}

func (a *compressionAdapter) EmitContextCompress(
    sessionID, reason string, layersRun int, beforePct, afterPct float64) {
    a.hook.RecordCompression(sessionID, reason, layersRun, beforePct, afterPct)
}
// 其他 DashboardEmitter 方法均为 no-op
```

**文件**: `internal/gateway/gateway.go`

`NewEvalRunner` 将压缩适配器接入 ContextManager 的多路 emitter：

```go
gw.contextMgr.SetDashboardEmitter(
    agent.NewMultiEmitter(gw.dashEmitter, r.CompressionEmitter()),
)
```

---

## 方向三：记忆评测 Live Store 联通

### 问题背景

原设计（隔离 store）的断链路径：

```
SetupFunc
  └─ newMemEvalHarness() → 创建临时 FileMemoryStore A（tmpDir1）
       └─ store.Save(entries)  → 写入 A

CognitiveAgent PERCEIVE
  └─ ca.memStore.Search(...)   → 读取 gateway 的 FileMemoryStore B（用户真实目录）

A ≠ B → 测试数据永远不被检索到
```

同时存在另一个问题：即使强制联通，eval 的 `memory_manage` 工具写入也会污染用户真实的 `~/.IronClaw/memory/` 目录。

### 解决方案：三层联通

#### 层 1：eval 内存目录隔离

**文件**: `cmd/ironclaw/eval.go`，`initEvalGateway()`

```go
evalMemDir, err := os.MkdirTemp("", "ironclaw-eval-memory-*")
cfg.Memory.StorageDir = evalMemDir
```

gateway 的 `init_memory.go` 读取 `cfg.Memory.StorageDir`，因此所有 eval 期间的内存操作（包括 `memory_manage` 工具调用）都写入这个临时目录，与用户真实数据完全隔离。`cleanup()` 结束时调用 `os.RemoveAll(evalMemDir)` 清除。

#### 层 2：MemoryAwareRunner 接口 + CognitiveAgentRunner 实现

**文件**: `internal/eval/cognitive_runner.go`

```go
// MemoryAwareRunner 由支持测试数据注入的 runner 实现。
type MemoryAwareRunner interface {
    InjectMemory(ctx context.Context, entries ...memory.Entry) error
    CleanupMemory(ctx context.Context, ids ...string) error
}
```

`CognitiveAgentRunner` 新增 `memStore memory.Store` 字段，由 `gateway.NewEvalRunner` 通过 `r.SetMemoryStore(gw.memStore)` 注入。注入的 store 与 `CognitiveAgent` 内部 `ca.memStore` 是**同一实例**（均来自 `gw.memStore`）：

```go
func (r *CognitiveAgentRunner) InjectMemory(ctx context.Context,
    entries ...memory.Entry) error {
    for _, e := range entries {
        if err := r.memStore.Save(ctx, e); err != nil {
            return fmt.Errorf("inject memory entry %q: %w", e.ID, err)
        }
    }
    return nil
}
```

#### 层 3：TaskCase 的 runner-aware 生命周期钩子

**文件**: `internal/eval/harness.go`

```go
type TaskCase struct {
    // ... 已有字段 ...

    // SetupWithRunner / CleanupWithRunner 优先于 SetupFunc / CleanupFunc 调用。
    // 用于需要访问 runner 能力（如内存注入）的任务。
    SetupWithRunner   func(ctx context.Context, runner AgentRunner) error `json:"-" yaml:"-"`
    CleanupWithRunner func(ctx context.Context, runner AgentRunner) error `json:"-" yaml:"-"`
}
```

Harness 执行优先级：

```
SetupWithRunner（有则调用）
    ↓ 无则降级
SetupFunc（有则调用）
    ↓
runner.RunTask(ctx, task)
    ↓
CleanupWithRunner / CleanupFunc（同上优先级）
```

### 记忆任务改写（fixtures_memory.go）

所有记忆评测任务使用 `SetupWithRunner` 替代旧的 `SetupFunc`，通过 `injectAndTrack()` helper 生成注入/清理闭包：

```go
func injectAndTrack(entries ...memory.Entry) (
    setup   func(context.Context, AgentRunner) error,
    cleanup func(context.Context, AgentRunner) error,
)
```

所有注入 entry 使用 `UserID: "eval_user"`，与 `CognitiveAgentRunner.RunTask` 中会话 UserID 一致，确保 PERCEIVE 阶段的 `Search(UserID: "eval_user")` 能命中：

```go
func evalMemEntry(id, content string) memory.Entry {
    return memory.Entry{
        ID:     id,
        Scope:  memory.ScopeUser,
        UserID: "eval_user",  // 与 runner 会话 UserID 匹配
        // ...
    }
}
```

### 完整数据流

```
initEvalGateway
  └─ cfg.Memory.StorageDir = tmpDir  ← 隔离
     └─ gateway.New → init_memory.go
          └─ gw.memStore = FileMemoryStore(tmpDir)
               └─ gw.cognitiveAgent.SetMemoryStore(gw.memStore)

gateway.NewEvalRunner
  └─ r.SetMemoryStore(gw.memStore)  ← runner 与 agent 共享同一 store 实例

eval harness（每个 memory 任务执行前）
  └─ task.SetupWithRunner(ctx, runner)
       └─ runner.(MemoryAwareRunner).InjectMemory(entries...)
            └─ r.memStore.Save(ctx, entry)  ← 写入 gw.memStore

CognitiveAgent.Run → PERCEIVE
  └─ ca.memStore.Search(UserID: "eval_user")  ← 读取同一 gw.memStore ✓

任务结束
  └─ task.CleanupWithRunner(ctx, runner)
       └─ runner.(MemoryAwareRunner).CleanupMemory(ids...)

eval 全部结束
  └─ cleanup() → os.RemoveAll(tmpDir)  ← 完全清除
```

---

## 涉及文件汇总

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/eval/harness.go` | 修改 | 新增 `ToolExecStat`、`CompressionEvent` 类型；`EvalResult` 新增 3 个字段；`SuiteResult` 新增 `CogHealth`；`TaskCase` 新增 `SetupWithRunner`/`CleanupWithRunner`；`runSetup`/`runCleanup` 辅助函数；`CogHealthCaptor` 接口 |
| `internal/eval/eval_hook.go` | 修改 | 新增 `compressions` 字段；`RecordCompression`/`GetCompressions` 方法；`ClearSession` 清除压缩记录 |
| `internal/eval/cognitive_runner.go` | 修改 | 新增 `memStore` 字段、`MemoryAwareRunner` 接口、`InjectMemory`/`CleanupMemory`/`SetMemoryStore`；`SetCogCollector`/`CaptureCogHealth`；`compressionAdapter` 实现压缩事件路由；`populateFromEvolution` 无条件计算 `EpisodeReward`，聚合 `ToolExecStats` 和压缩事件；任务后上报 `AssertionPassRate` |
| `internal/eval/cognitive_runner_test.go` | 修改 | 新增 `TestMemoryAwareRunner_InjectAndCleanup`、`TestMemoryAwareRunner_NoStore`、`TestRunSuite_SetupWithRunner` |
| `internal/eval/fixtures_memory.go` | 重写 | 移除隔离 store 方案；`evalMemEntry`/`injectAndTrack` helpers；6 个记忆任务全部使用 `SetupWithRunner`/`CleanupWithRunner` |
| `internal/gateway/gateway.go` | 修改 | `New()` 中调用 `initCogMetrics()`；`CogMetricsCollector()` 访问器；`NewEvalRunner()` 中设置 `SetMemoryStore`、`SetCogCollector`、多路压缩 emitter |
| `internal/gateway/init_cogmetrics.go` | 新增 | 独立的 `initCogMetrics()` 实现幂等 collector 初始化 |
| `internal/gateway/init_dashboard.go` | 修改 | 改为调用 `gw.initCogMetrics()` 而非直接创建 collector |
| `internal/agent/cognitive.go` | 修改 | 新增 `MemoryStore() memory.Store` 访问器 |
| `cmd/ironclaw/eval.go` | 修改 | `initEvalGateway` 中创建临时内存目录并在 cleanup 中删除 |

---

## 设计决策

### 为什么 EpisodeReward 无条件计算？

进化引擎在 `evolution.enabled=false`（默认配置）时不运行，但评测结果仍需要一个奖励信号用于任务横向比较。将计算逻辑内置在 harness 中（基于已有的 `AssertionPassRate`、`ReplanCount`、`Success`），使 reward 在任何运行模式下都有值，进化引擎启用时用实际 `EpisodeEvent` 覆盖，向后兼容。

### 为什么不直接用 CognitiveAgent.MemoryStore() 而是在 runner 持有 store？

`CognitiveAgentRunner` 不应对 `CognitiveAgent` 内部结构强依赖。runner 持有独立的 `memStore` 字段，由 gateway 注入，使 `InjectMemory`/`CleanupMemory` 可在单元测试中用 mock store 直接验证，无需初始化完整的 `CognitiveAgent`（后者需要 runtime、provider 等重型依赖）。

### 为什么用临时目录而不是 mock store？

Eval 需要真实的文件系统语义（MEMORY.md 索引、FTS5 搜索、YAML frontmatter 解析），mock store 无法覆盖这些路径。临时目录方案让 eval 使用与生产相同的代码路径，测试完毕后一次性删除，兼顾真实性与隔离性。

### 为什么增加 SetupWithRunner 而不是修改 SetupFunc 签名？

`SetupFunc func() error` 是已有的公开 API，存量代码（测试、已有任务集）均依赖此签名。新增 `SetupWithRunner func(context.Context, AgentRunner) error` 作为可选覆盖字段，完全向后兼容：不需要 runner 的任务继续使用 `SetupFunc`，需要 runner 的任务（如 memory 任务）使用 `SetupWithRunner`。

---

## 使用示例

### 运行记忆评测（Live 模式）

```bash
ironclaw eval run --suite memory --live -o eval_output/memory_results.json
```

评测会自动：
1. 创建临时内存目录（隔离用户数据）
2. 在 agent 的 live store 中注入测试数据
3. 驱动 CognitiveAgent 执行任务（PERCEIVE 阶段检索注入数据）
4. 清理注入数据
5. 删除临时目录

### 诊断结果中的自进化指标

```bash
ironclaw eval diagnose --suite full --live -o eval_output/
cat eval_output/results.json | jq '.results[] | {
  id: .task_id,
  reward: .episode_reward,
  compressions: .compression_count,
  tools: [.tool_exec_stats[]? | {name: .tool_name, success_rate: .success_rate}]
}'
```

### 认知健康报告

```bash
cat eval_output/results.json | jq '.cog_health'
# 输出示例：
# {
#   "avg_reflection_quality": 0.82,
#   "assertion_pass_rate": 0.76,
#   "replan_rate": 0.15,
#   "tool_success_rate": 0.91
# }
```
