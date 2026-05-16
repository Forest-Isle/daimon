# CognitiveAgent HandleMessage 拆分重构

**日期**: 2026-05-17
**范围**: HandleMessage 方法拆分、错误传播修复、goto 消除、ch.Send 静默错误修复

## 概述

`CognitiveAgent.HandleMessage` 原本是一个 539 行的怪兽函数，承担了会话初始化、任务账本注册、上下文压缩、PERCEIVE 阶段及后注入、简单任务委托、辩论触发、MCTS/Tree Planner 搜索、五阶段重试循环（PLAN→ACT→OBSERVE→REFLECT）、反馈收集、演化事件分发、会话持久化和记忆保存等全部职责。

本次重构将其拆分为 6 个私有方法，HandleMessage 从 539 行缩减到 36 行，消除了 `goto` 语句，修复了 PLAN/ACT/REFLECT 阶段的错误吞没问题，同时清理了全项目 16 处 `_ = ch.Send(...)` 静默错误丢弃。

## 核心架构

### 拆分前（539 行单函数）

```
HandleMessage(ctx, ch, msg)
  ├── 会话获取 + 仪表盘 EmitSessionStart
  ├── /resume 检查
  ├── 任务账本注册 + defer cleanup
  ├── 用户消息追加
  ├── 上下文压缩（ContextManager 或 CompactHistory 回退）
  ├── ── PERCEIVE ── + dashboard/metrics + codebase 注入
  ├── 简单任务委托（ComplexitySimple）
  ├── 辩论触发
  ├── MCTS 搜索 / Tree Planner 候选生成
  ├── for attempt := 0; attempt <= maxReplans; attempt++
  │   ├── ── PLAN ──（MCTS 复用 / Tree Planner / 线性 Planner）
  │   ├── RL PPO 策略调整
  │   ├── Direct Reply 跳过 ACT/OBSERVE
  │   ├── ── ACT ──（RunWithContext）
  │   ├── ── OBSERVE ── + 检查点保存
  │   ├── ── REFLECT ── + DQN 重规划调整
  │   └── 重规划决策（Abort / Continue / Adjust）
  ├── goto persist  ← 清理标签
  ├── 反馈收集
  ├── RL Episode 记录
  ├── 演化事件分发 + WaitPending
  ├── 会话持久化
  └── 记忆保存
```

### 拆分后（6 个私有方法 + 36 行协调器）

```go
func (ca *CognitiveAgent) HandleMessage(...) error {
    sess, target, parentTaskID, cleanup, err := ca.setupCognitiveSession(ctx, ch, msg)
    // ...
    state, err := ca.runPerceivePhase(ctx, sess, msg, parentTaskID)
    // ...
    plan, mctsCandidates, mctsActive, treePlanner := ca.runPrePlanSearch(ctx, sess, state)
    _, plan, obsResult, reflection, ppoStrategy, episodeCollector, dqnReplanAction, rlState, loopErr :=
        ca.runCognitiveLoop(ctx, ch, sess, target, state, plan, mctsCandidates, mctsActive, treePlanner, parentTaskID)
    finalizeErr := ca.finalizeCognitiveSession(...)
    if loopErr != nil { return loopErr }
    return finalizeErr
}
```

### 新增私有方法

| 方法 | 职责 | 行数 |
|------|------|------|
| `setupCognitiveSession` | 会话获取、任务账本注册、用户消息追加、上下文压缩 | ~65 |
| `runPerceivePhase` | PERCEIVE 阶段 + codebase/skills/agents/personality/evolution 注入 | ~58 |
| `delegateToRuntime` | 简单任务委托 + evolution episode 分发 | ~25 |
| `runPrePlanSearch` | MCTS 搜索 + Tree Planner 候选生成（回退逻辑） | ~55 |
| `runCognitiveLoop` | 核心重试循环：PLAN→ACT→OBSERVE→REFLECT + RL + 重规划决策 | ~300 |
| `finalizeCognitiveSession` | 检查点清理、反馈收集、RL 记录、演化分发、会话持久化、记忆保存 | ~60 |

## goto 消除

### Before

```go
if shouldAbort {
    goto persist  // 跳出重试循环
}
// ...
switch decision {
case ReplanAbort:
    goto persist
case ReplanContinue:
    goto persist
case ReplanAdjust:
    continue
}

persist:
    _ = ca.checkpointStore.Delete(ctx, sess.ID)
    // 反馈收集...
```

### After

`goto persist` 被移除。重试循环抽取为独立的 `runCognitiveLoop` 方法，正常 return 即可跳出。清理逻辑（检查点删除、反馈收集）移入 `finalizeCognitiveSession`，通过 HandleMessage 的协调层在 `runCognitiveLoop` 返回后**无条件执行**，确保即使 loopErr != nil 也能完成清理。

## 错误传播修复

### Before（错误吞没）

```go
// PLAN 失败
if err != nil {
    slog.Error("cognitive: plan failed", "err", err)
    break  // 跳出循环
}
// ...
// ACT 失败
if actErr != nil {
    slog.Error("cognitive: act failed", "err", actErr)
    break
}
// ...
// REFLECT 失败
if err != nil {
    slog.Error("cognitive: reflect failed", "err", err)
    break
}
// 最终 return nil ← 调用者不知道任务失败！
```

### After（错误传播）

```go
// PLAN 失败
if err != nil {
    return "", plan, obsResult, reflection, nil, nil, 0, nil,
        fmt.Errorf("plan phase failed: %w", err)
}
// ACT 失败
if actErr != nil {
    return "", plan, obsResult, reflection, nil, nil, 0, nil,
        fmt.Errorf("act phase failed: %w", actErr)
}
// REFLECT 失败
if err != nil {
    return "", plan, obsResult, reflection, nil, nil, 0, nil,
        fmt.Errorf("reflect phase failed: %w", err)
}
```

`runCognitiveLoop` 现在返回 `error` 作为最后一个返回值，调用方 `HandleMessage` 检查并传播。

## ch.Send 静默错误修复

全项目 16 处 `_ = ch.Send(context.Background(), ...)` 全部改为显式错误处理：

```go
// Before
_ = ch.Send(ctx, channel.OutboundMessage{...})

// After
if err := ch.Send(ctx, channel.OutboundMessage{...}); err != nil {
    slog.Warn("failed to send message", "err", err)
}
```

涉及文件：
- `internal/gateway/gateway.go` — 12 处
- `internal/agent/runtime.go` — 1 处
- `internal/agent/act.go` — 1 处
- `internal/agent/cognitive.go` — 1 处
- `internal/gateway/command_feature.go` — 1 处

`internal/eval/eval_channel.go` 的 1 处保留不动（测试用假 channel）。

## 检查点操作错误处理

`checkpointStore.Save()` 和 `checkpointStore.Delete()` 的 `_ =` 静默丢弃改为 `slog.Warn` 日志：

```go
// Before
_ = ca.checkpointStore.Save(ctx, cp)
// After
if err := ca.checkpointStore.Save(ctx, cp); err != nil {
    slog.Warn("cognitive: checkpoint save failed", "err", err)
}
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/cognitive.go` | 修改 | HandleMessage 拆分 + 6 个新私有方法 + 错误传播 |
| `internal/agent/runtime.go` | 修改 | ch.Send 错误处理 |
| `internal/agent/act.go` | 修改 | ch.Send 错误处理 |
| `internal/gateway/gateway.go` | 修改 | 12 处 ch.Send 错误处理 |
| `internal/gateway/command_feature.go` | 修改 | ch.Send 错误处理 |

## 验收清单

- [x] `HandleMessage` 从 539 行缩减到 36 行
- [x] 所有 `goto` 语句已移除
- [x] PLAN/ACT/REFLECT 失败时返回真实 error（不再 return nil）
- [x] `_ = ch.Send(...)` 在非测试代码中清零
- [x] `_ = ca.checkpointStore.Save/Delete` 改为 slog.Warn
- [x] `gofmt` 通过
