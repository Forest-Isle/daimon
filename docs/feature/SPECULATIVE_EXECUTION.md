# 推测性执行

**日期**: 2026-04-18
**范围**: 流式响应期间预启动只读工具 + 早期 tool_use 块检测 + 结果收集

## 概述

推测性执行（Speculative Execution）是一项受 Claude Code 并发工具执行机制启发的优化。在 LLM 流式生成响应的过程中，当一个 `tool_use` 块的流式输出完成（但模型仍在生成后续内容或其他工具调用）时，立即在后台启动该工具的执行。当模型最终完成响应、Runtime 准备执行工具时，只读工具的结果可能已经准备好了，从而消除等待时间。

**核心约束**: 仅对只读工具（`IsReadOnly() == true`）启用推测性执行，确保不会产生副作用。写操作工具仍等待模型完整响应后按常规流程执行。

## 架构

### 三阶段生命周期

```
  LLM 流式响应
  ─────────────────────────────────────────────────>
  │
  │  [1] 检测: ContentBlockStopEvent
  │  ├── tool_use 块流式完成
  │  ├── 提取 (ID, Name, Input)
  │  └── 生成 PendingToolBlock
  │
  │  [2] 启动: SpeculativeExecutor.TryLaunch()
  │  ├── 检查: 工具存在? 只读? 未达上限? 无重复?
  │  ├── 通过 → 后台 goroutine 执行
  │  └── 拒绝 → 返回 false，走常规路径
  │
  ──────────── 模型响应完成 ────────────
  │
  │  [3] 收集: SpeculativeExecutor.Collect()
  │  ├── 已完成 → 返回 (result, error)
  │  ├── 仍在运行 → 返回 (nil, nil)
  │  └── 未知 ID → 返回 (nil, nil)
  │  （后两种情况走常规执行路径）
```

### SpeculativeExecutor

```go
type SpeculativeExecutor struct {
    registry    *tool.Registry
    maxInFlight int                          // 最大并发推测执行数
    results     map[string]*speculativeResult // toolUseID → 结果
    inFlight    int                          // 当前并发数
    mu          sync.Mutex
}
```

**API**:

| 方法 | 签名 | 说明 |
|------|------|------|
| `TryLaunch` | `(ctx, toolUseID, toolName, input string) bool` | 尝试启动推测执行，返回是否成功 |
| `Collect` | `(toolUseID string) (*tool.Result, error)` | 收集已完成的结果，未完成返回 nil |
| `CancelAll` | `()` | 取消所有进行中的执行 |
| `Reset` | `()` | 取消并清空（每轮迭代开始时调用） |

### 准入控制

`TryLaunch` 在启动前执行 4 项检查，任一失败则拒绝推测执行：

| 检查 | 条件 | 拒绝原因 |
|------|------|---------|
| 工具存在 | `registry.Get(toolName)` 失败 | 未知工具 |
| 只读属性 | `tool.IsToolReadOnly(t) == false` | 可能产生副作用 |
| 并发上限 | `inFlight >= maxInFlight` | 资源保护 |
| 去重 | `results[toolUseID]` 已存在 | 防止重复执行 |

### 早期 tool_use 块检测

在 `claudeStreamIterator` 中，当接收到 `ContentBlockStopEvent` 时，检查对应的累积块是否为 `ToolUseBlock`：

```go
case anthropic.ContentBlockStopEvent:
    if int(e.Index) < len(it.accum.Content) {
        block := it.accum.Content[e.Index]
        if v, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
            inputBytes, _ := json.Marshal(v.Input)
            it.pendingToolBlocks = append(it.pendingToolBlocks, PendingToolBlock{
                ToolUseID: v.ID,
                ToolName:  v.Name,
                Input:     string(inputBytes),
            })
        }
    }
```

通过 `PendingToolBlockSource` 接口暴露给 Runtime：

```go
type PendingToolBlockSource interface {
    PendingToolBlocks() []PendingToolBlock
}
```

## Runtime 集成

### 流式循环中的推测执行

在 Runtime 的流式 agent loop 中，每次 `stream.Next()` 返回后检查是否有新的完成 tool_use 块：

```go
for {
    delta, err := stream.Next()
    // ... 处理文本和工具调用 ...

    if r.speculativeExecutor != nil {
        if ptbSrc, ok := stream.(PendingToolBlockSource); ok {
            for _, ptb := range ptbSrc.PendingToolBlocks() {
                if launched := r.speculativeExecutor.TryLaunch(ctx, ptb.ToolUseID, ptb.ToolName, ptb.Input); launched {
                    slog.Debug("speculative launch", "tool", ptb.ToolName, "id", ptb.ToolUseID)
                }
            }
        }
    }

    if delta.Done { break }
}
```

### 工具执行中的结果收集

在 `executeToolCall` 中，实际执行工具前先尝试收集推测结果：

```go
func (r *Runtime) executeToolCall(ctx, ch, sess, target, tc) toolResult {
    t, err := r.tools.Get(tc.Name)
    // ...

    if r.speculativeExecutor != nil {
        if specResult, specErr := r.speculativeExecutor.Collect(tc.ID); specResult != nil {
            // 直接使用推测结果，跳过权限检查和实际执行
            return toolResult{...}
        }
    }

    // 常规路径: 权限检查 → 执行 → 返回结果
}
```

### 迭代重置

每轮迭代开始时调用 `Reset()` 清空上一轮的推测结果：

```go
for iteration := 0; iteration < r.cfg.MaxIterations; iteration++ {
    if r.speculativeExecutor != nil {
        r.speculativeExecutor.Reset()
    }
    // ...
}
```

## 并发安全

`SpeculativeExecutor` 使用 `sync.Mutex` 保护所有状态操作：

- `TryLaunch`: 锁内检查并发上限和去重，解锁后启动 goroutine
- goroutine 完成时: 锁内递减 `inFlight`，写入结果
- `Collect`: 锁内读取结果指针，非阻塞 `select` 检查完成状态
- `CancelAll`: 锁内遍历并取消所有 goroutine
- `Reset`: 调用 `CancelAll` 后锁内重建 map

取消语义通过 `context.WithCancel` 和 `cancelled` 标志位实现：即使 goroutine 在 `cancel()` 调用后才完成，也会因 `cancelled == true` 丢弃结果。

## 配置

```yaml
agent:
  speculative_execution:
    enabled: true       # 是否启用推测性执行
    max_in_flight: 3    # 最大并发推测执行数（默认 3）
```

## 适用工具

推测性执行仅对实现 `IsReadOnly() == true` 的工具生效：

| 工具 | 只读 | 可推测 |
|------|------|--------|
| `file_read` | 是 | 是 |
| `file_list` | 是 | 是 |
| `http` (GET) | 是 | 是 |
| `browser_search` | 是 | 是 |
| `browser_extract` | 是 | 是 |
| `bash` | 否 | 否 |
| `file_write` | 否 | 否 |
| `file_edit` | 否 | 否 |

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/speculative.go` | 新增 | SpeculativeExecutor 核心实现 |
| `internal/agent/speculative_test.go` | 新增 | 9 个测试用例 |
| `internal/agent/stream.go` | 修改 | PendingToolBlock 类型 + ContentBlockStopEvent 检测 + Prompt Cache 分割 |
| `internal/agent/provider.go` | 修改 | PendingToolBlockSource 接口定义 |
| `internal/agent/runtime.go` | 修改 | 流式循环中 TryLaunch + 迭代开始时 Reset |
| `internal/agent/concurrent.go` | 修改 | executeToolCall 中 Collect 结果收集 |
| `internal/config/config.go` | 修改 | SpeculativeExecutionConfig 结构 |
| `internal/gateway/init_multiagent.go` | 修改 | 根据配置创建并注入 SpeculativeExecutor |

## 测试

9 个新增测试用例：

- `TestSpeculativeExecutor_TryLaunch_ReadOnly` — 只读工具成功启动并收集结果
- `TestSpeculativeExecutor_TryLaunch_NonReadOnly_Rejected` — 非只读工具被拒绝
- `TestSpeculativeExecutor_TryLaunch_MaxInFlight` — 超过并发上限被拒绝
- `TestSpeculativeExecutor_CancelAll` — 取消后结果为 nil
- `TestSpeculativeExecutor_Collect_UnknownID` — 未知 ID 返回 nil
- `TestSpeculativeExecutor_Reset` — 重置后状态清空
- `TestSpeculativeExecutor_TryLaunch_DuplicateID` — 重复 ID 被拒绝
- `TestSpeculativeExecutor_TryLaunch_UnknownTool` — 不存在的工具被拒绝
- `TestSpeculativeExecutor_DefaultMaxInFlight` — maxInFlight ≤ 0 时默认为 3

## 性能影响

推测性执行的收益取决于工具执行时间与模型生成时间的重叠程度：

- **最佳场景**: 模型生成 3 个工具调用，其中 2 个只读。前 2 个在模型生成第 3 个时已并行完成。
- **典型场景**: 文件读取等快速工具（<100ms）在模型完成响应前已返回结果。
- **最差场景**: 所有工具都是写操作，推测执行不生效，无额外开销。
