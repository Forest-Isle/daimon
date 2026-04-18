# 策略硬控制桥接与奖励公式统一

**日期**: 2026-04-18
**范围**: 让 StrategyOptimizer 的数值直接控制认知 Agent 行为 + 统一在线/离线奖励计算

## 概述

此前自进化系统存在两个关键断层：

1. **软控制问题**：`StrategyOptimizer` 输出的 `ReplanThreshold` 和 `ToolPriorities` 仅通过 `BuildPromptSection()` 作为自然语言注入 `{{STRATEGY}}` 模板。Agent 实际使用的 `confidenceThreshold` 来自静态配置 `cogCfg.ConfidenceThreshold`，二者互不影响。
2. **奖励分裂问题**：在线路径（`computeSimpleEpisodeReward`）和离线路径（`computeTrajectoryReward`）使用两套独立的奖励公式，权重和组成不同，导致 RL 训练信号不一致。

本次改动解决了这两个问题。

## Part A: 策略硬控制桥接

### 问题

```
StrategyOptimizer                  CognitiveAgent
─────────────────                  ───────────────
ReplanThreshold: 0.45   ──×──>    confidenceThreshold: 0.6 (from config)
                         │
                 只作为自然语言
                 注入 {{STRATEGY}}
```

优化器辛苦调优的数值，LLM 可能遵守也可能忽略。

### 解决方案

在 `OptimizerConfig` 中新增 `HardControlEnabled bool` 字段（默认 false），启用后 Agent 直接使用优化器的值：

```
StrategyOptimizer                  CognitiveAgent
─────────────────                  ───────────────
ReplanThreshold: 0.45   ──✓──>    confidenceThreshold: 0.45
  (HardControlEnabled: true)       (with clamp bounds)
```

### 新增 API

`StrategyOptimizer` 新增三个公开方法：

```go
// 返回进化后的 replan 阈值，未优化时返回 0
func (so *StrategyOptimizer) GetReplanThreshold() float64

// 返回特定工具的优先级，未设置时返回 defaultToolPriority (0.5)
func (so *StrategyOptimizer) GetToolPriority(toolName string) float64

// 返回硬控制是否启用
func (so *StrategyOptimizer) IsHardControlEnabled() bool
```

### CognitiveAgent 集成

在 `HandleMessage` 中，`confidenceThreshold` 赋值后新增策略覆盖逻辑：

```go
confidenceThreshold := cogCfg.ConfidenceThreshold
if confidenceThreshold <= 0 {
    confidenceThreshold = 0.6
}
// 硬控制：进化引擎直接覆盖阈值
if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
    if so := ca.evoEngine.StrategyOptimizerHook(); so != nil && so.IsHardControlEnabled() {
        if evoThreshold := so.GetReplanThreshold(); evoThreshold > 0 {
            confidenceThreshold = evoThreshold
        }
    }
}
```

### 安全机制

- **默认关闭**：`HardControlEnabled` 默认 false，不影响现有行为
- **Clamp 保护**：`GetReplanThreshold()` 内部将值限制在 `[minReplanThreshold, maxReplanThreshold]`（即 `[0.01, 0.99]`）
- **渐进策略**：当前仅硬化 replan threshold 一个参数；工具优先级仍通过 `{{STRATEGY}}` 软控制

### 配置

```yaml
evolution:
  optimizer:
    enabled: true
    hard_control_enabled: true   # 新增字段
    update_interval: 10
    max_adjustment_percent: 10
```

## Part B: 奖励公式统一

### 问题

| 路径 | 函数 | 位置 | 公式 |
|------|------|------|------|
| 在线（实时） | `computeSimpleEpisodeReward` | `internal/agent/rl_helpers.go` | succeeded ? +1.0 : -1.0, + progress * 0.5 |
| 离线（轨迹） | `computeTrajectoryReward` | `internal/evolution/rl_bridge.go` | succeeded ? +0.5 : 0, + duration < 60s ? +0.2, + noReplan ? +0.1, + feedback * 0.2 |

两个公式的权重、组成完全不同，导致 RL 训练接收到不一致的信号。

### 解决方案

新建 `internal/evolution/reward.go`，提供统一的 `ComputeReward` 函数：

```go
type RewardInput struct {
    Succeeded    bool
    Progress     float64    // 0.0–1.0
    DurationMs   int64
    ReplanCount  int
    UserFeedback float64    // -1 to 1
}

func ComputeReward(in RewardInput) float64
```

奖励分量：

| 分量 | 贡献 | 说明 |
|------|------|------|
| 基础 | +0.5 / -0.5 | 成功/失败 |
| 进度 | 0 ~ +0.25 | `progress * 0.25` |
| 速度 | +0.1 | 完成时间 < 60s |
| 效率 | +0.05 | 无重规划 |
| 反馈 | -0.1 ~ +0.1 | `feedback * 0.1` |

总范围：clamp 到 `[-1.5, 1.0]`。

### 迁移

- `computeSimpleEpisodeReward`（agent 包）→ 改为调用 `evolution.ComputeReward`
- `computeTrajectoryReward`（evolution 包）→ 改为调用 `evolution.ComputeReward`
- 已有的 `TestComputeSimpleEpisodeReward` 测试用例更新期望值以匹配新公式

### 一致性验证

集成测试 `TestEngineIntegration_RewardConsistency` 验证：对于相同的输入参数，在线和离线路径产生完全相同的奖励值。

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/evolution/config.go` | 修改 | `OptimizerConfig` 新增 `HardControlEnabled` 字段 |
| `internal/evolution/optimizer.go` | 修改 | 新增 `GetReplanThreshold`、`GetToolPriority`、`IsHardControlEnabled` |
| `internal/evolution/optimizer_test.go` | 修改 | 新增 5 个测试覆盖新方法 |
| `internal/evolution/reward.go` | 新增 | 统一的 `ComputeReward` 函数和 `RewardInput` 类型 |
| `internal/evolution/reward_test.go` | 新增 | 7 个测试覆盖全部奖励分量和 clamp 边界 |
| `internal/evolution/rl_bridge.go` | 修改 | `computeTrajectoryReward` 改用 `ComputeReward` |
| `internal/agent/rl_helpers.go` | 修改 | `computeSimpleEpisodeReward` 改用 `evolution.ComputeReward` |
| `internal/agent/rl_helpers_test.go` | 修改 | 更新期望值匹配统一公式 |
| `internal/agent/cognitive.go` | 修改 | 新增硬控制覆盖逻辑 |
| `internal/evolution/engine_integration_test.go` | 新增 | 4 个集成测试覆盖完整事件流和奖励一致性 |

## 测试

**Part A 测试**（5 个单元 + 2 个集成）：
- `TestOptimizer_GetReplanThreshold_FreshOptimizer` — 未优化时返回 0
- `TestOptimizer_GetReplanThreshold_AfterOptimization` — 优化后返回有效值
- `TestOptimizer_GetToolPriority_Default` — 未知工具返回默认值
- `TestOptimizer_GetToolPriority_AfterInsights` — 洞察应用后返回调整值
- `TestOptimizer_IsHardControlEnabled` — 配置开关生效
- `TestEngineIntegration_FullEventFlow` — 完整事件分发 → hook 处理 → 策略更新
- `TestEngineIntegration_HardControlPipeline` — 硬控制全链路

**Part B 测试**（7 个单元 + 1 个集成）：
- `TestComputeReward_Success/Failure/SpeedBonus/ReplanPenalty/UserFeedback/Clamped/ProgressContribution`
- `TestEngineIntegration_RewardConsistency` — 在线/离线奖励一致性
