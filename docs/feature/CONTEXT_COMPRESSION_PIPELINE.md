# 上下文压缩管线升级

**日期**: 2026-04-18
**范围**: 统一上下文管理器 + 5 层渐进式压缩 + 反应式 413 重试 + Prompt Cache 分割

## 概述

本次改进引入了 `ContextManager` 统一接口和 `PipelineContextManager` 实现，将原来分散的上下文压缩逻辑（CompactHistory、CompressionPipeline、TokenBudget）整合到一个一致的抽象层下。同时新增反应式压缩（413 错误自动重试）和系统提示词的缓存边界分割，参考 Claude Code 的 4 层压缩架构（snip → microcompact → collapse → autocompact）进行设计。

## 核心架构

### ContextManager 统一接口

```go
type ContextManager interface {
    Compress(ctx context.Context, sess *session.Session, systemPrompt string) (bool, error)
    ReactiveCompress(ctx context.Context, sess *session.Session, systemPrompt string) error
    Utilization(sess *session.Session, systemPrompt string) float64
    SplitSystemPrompt(full string) (static, dynamic string)
}
```

| 方法 | 职责 |
|------|------|
| `Compress` | 主动压缩：在每次迭代前检查利用率，触发渐进式压缩管线 |
| `ReactiveCompress` | 反应式压缩：413/context_length_exceeded 错误时无条件压缩 |
| `Utilization` | 估算当前上下文窗口利用率（字符数 × ratio / 窗口大小） |
| `SplitSystemPrompt` | 在 `<!-- DYNAMIC_CONTEXT -->` 标记处分割系统提示词，用于 Prompt Cache |

### PipelineContextManager 实现

`PipelineContextManager` 封装 `CompressionPipeline` 并提供两套降级路径：

- **有管线（strategy=layered）**: 使用 5 层渐进式压缩
- **无管线（fallback）**: 回退到 `CompactHistory`（基于消息数量阈值 40 条）

在 Gateway 初始化时**无条件创建**，确保即使未配置 layered 策略，413 反应式重试依然可用。

## 5 层渐进式压缩管线

压缩管线按利用率阈值逐层触发，每层在前一层基础上进一步减少上下文：

| 层级 | 名称 | 阈值 | 成本 | 行为 |
|------|------|------|------|------|
| 0 | `tool_output_prune` | tool_eviction_pct | 无 LLM 调用 | 截断旧对话轮次中超过 2000 字符的工具输出，保留最近 4 轮不动 |
| 1 | `tool_eviction` | tool_eviction_pct | 无 LLM 调用 | 将超过 8KB 的工具结果持久化到 ResultStore，内联替换为摘要预览 |
| 2 | `turn_summarization` | summarize_pct | 1 次 LLM 调用 | 将历史对话的前半部分通过 LLM 总结为摘要，支持增量更新 |
| 3 | `old_context_removal` | slim_prompt_pct | 无 LLM 调用 | 移除最旧的 1/3 消息，插入裁剪提示 |
| 4 | `emergency_truncation` | emergency_pct | 无 LLM 调用 | 仅保留最近 10 个对话轮次，丢弃所有其他内容 |

### 执行流程

```
Run(ctx, sess, systemPrompt)
│
├── 对每层: 重新估算利用率
│   ├── 利用率 < 阈值 → 停止（break）
│   └── 利用率 ≥ 阈值 → 执行该层 Compress()
│       └── 层失败 → 日志警告，继续下一层
│
└── ensureToolPairing(sess)  ← 无条件执行
    ├── 移除孤儿 tool_result（对应 tool_use 被压缩删除）
    └── 为缺失 tool_result 的 tool_use 插入桩消息
```

### RunForced（反应式模式）

当 API 返回 413/context_length_exceeded 错误时，跳过所有阈值检查，无条件执行全部压缩层：

```
RunForced(ctx, sess, systemPrompt)
│
├── 逐层执行 Compress()，忽略阈值
│   └── 层失败 → 日志警告，继续
│
└── ensureToolPairing(sess)
```

## 反应式 413 重试

Runtime 的 agent loop 在流式和非流式两条路径上都集成了反应式重试：

```
for iteration := 0; iteration < maxIterations; iteration++ {
    hasAttemptedReactiveCompact = false  ← 每轮重置

    stream/complete → error?
    │
    ├── isContextLengthError(err) && contextManager != nil && !hasAttemptedReactiveCompact
    │   ├── hasAttemptedReactiveCompact = true  ← 断路器：每轮仅允许一次
    │   ├── contextManager.ReactiveCompress(ctx, sess, systemPrompt)
    │   └── continue  ← 重试当前迭代
    │
    └── 其他错误 → 返回
}
```

**关键设计决策**：`hasAttemptedReactiveCompact` 在每轮迭代开始时重置为 `false`，确保不会因持续的 413 错误陷入无限重试循环。同一轮内最多触发一次反应式压缩。

## 系统提示词 Prompt Cache 分割

系统提示词通过 `<!-- DYNAMIC_CONTEXT -->` 标记分为静态部分和动态部分：

```
┌──────────────────────────────────────────┐
│  静态部分（角色定义、工具说明、技能描述）  │ ← CacheControl: ephemeral
├──────────────────────────────────────────┤
│  <!-- DYNAMIC_CONTEXT -->                │
├──────────────────────────────────────────┤
│  动态部分（记忆、知识库、项目上下文）      │ ← 无缓存标记
└──────────────────────────────────────────┘
```

在 `buildParams` 中，如果检测到标记：
- 静态部分作为第一个 `TextBlockParam`，附带 `CacheControlEphemeral`
- 动态部分作为第二个 `TextBlockParam`，无缓存控制

这使得 Anthropic API 可以跨请求复用静态系统提示词的缓存，减少 Token 计费。

## 集成点

### Runtime（简单模式）
- **压缩入口**: `HandleMessage` 中调用 `contextManager.Compress()` 替代原来的 `compressionPipeline.Run()`
- **3 层降级**: `contextManager` → `compressionPipeline` → `CompactHistory`
- **413 重试**: 流式路径（`Stream` 错误）和非流式路径（`Complete` 错误）均支持

### CognitiveAgent（认知模式）
- **压缩入口**: `HandleMessage` 中在 PERCEIVE 阶段前调用 `contextManager.Compress()`
- **传播**: `SetContextManager` 同时设置 `ca.contextManager` 和 `ca.runtime.SetContextManager(cm)`

### Gateway
- **初始化**: `initMultiAgent` 中无条件创建 `PipelineContextManager`，传递给 `runtime` 和 `cognitiveAgent`

## 配置

```yaml
agent:
  compression:
    strategy: "layered"            # "layered" | "legacy"
    token_estimate_ratio: 0.25     # 字符数到 Token 的估算比例
    layers:
      tool_eviction_pct: 50        # 层 0-1 触发阈值
      summarize_pct: 65            # 层 2 触发阈值
      slim_prompt_pct: 80          # 层 3 触发阈值
      emergency_pct: 95            # 层 4 触发阈值
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/context_manager.go` | 新增 | ContextManager 接口 + PipelineContextManager 实现 |
| `internal/agent/context_manager_test.go` | 新增 | 14 个测试用例：压缩触发、利用率估算、Prompt 分割、反应式压缩 |
| `internal/agent/compression.go` | 修改 | 新增 `RunForced()` 方法 + 共享 `countContextChars` 辅助函数 |
| `internal/agent/runtime.go` | 修改 | 集成 ContextManager 压缩 + 413 反应式重试（双路径） |
| `internal/agent/cognitive.go` | 修改 | PERCEIVE 前调用 ContextManager + 传播到内部 Runtime |
| `internal/agent/stream.go` | 修改 | `buildParams` 实现 Prompt Cache 分割逻辑 |
| `internal/gateway/init_multiagent.go` | 修改 | 无条件创建并注入 PipelineContextManager |

## 测试

14 个新增测试用例覆盖所有关键路径：

- `TestPipelineContextManager_Compress_BelowThreshold` — 利用率低于阈值时不触发
- `TestPipelineContextManager_Compress_AboveThreshold` — 利用率超过阈值时触发管线
- `TestPipelineContextManager_Compress_NilPipeline_Legacy` — 无管线时回退到 CompactHistory
- `TestPipelineContextManager_Compress_NilPipeline_BelowThreshold` — 无管线 + 消息不足时不触发
- `TestPipelineContextManager_Utilization_Small/Large/IncludesToolInput` — 利用率估算精度
- `TestPipelineContextManager_SplitSystemPrompt_WithMarker/WithoutMarker/AtStart/AtEnd` — 标记分割边界
- `TestPipelineContextManager_ReactiveCompress_NilPipeline/WithPipeline` — 反应式压缩双路径
- `TestPipelineContextManager_Utilization_DefaultRatio` — 默认 ratio 回退
