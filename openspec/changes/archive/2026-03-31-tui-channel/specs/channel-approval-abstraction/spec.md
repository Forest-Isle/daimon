## Overview

将工具执行审批和认知模式反思决策从 Telegram 硬编码解耦为通用可选接口，使任何 channel 实现都可以提供交互式审批能力。

## Requirements

### R1: ApprovalSender 接口
- 定义在 `internal/channel/channel.go` 中
- 方法签名：`SendApprovalRequest(ctx context.Context, target MessageTarget, toolName string, input string) (bool, error)`
- 阻塞调用：内部管理请求发送和响应等待
- 返回 `true` 表示批准，`false` 表示拒绝

### R2: ReflectionSender 接口
- 定义在 `internal/channel/channel.go` 中
- 方法签名：`SendReflectionRequest(ctx context.Context, target MessageTarget, reason string, confidence float64) (ReplanDecision, error)`
- `ReplanDecision` 类型定义在 channel 包中（避免循环依赖 agent→channel）
- 返回用户选择的决策

### R3: Gateway 集成
- `gateway.go` 的 `handleApproval()` 通过 `ch.(channel.ApprovalSender)` 接口检查替代 `ch.(*telegram.Adapter)` 类型断言
- 不支持审批的 channel fallback 为 auto-approve（保持现有行为）

### R4: Reflector 集成
- `reflect.go` 的 `RequestReplanApproval()` 通过 `ch.(channel.ReflectionSender)` 接口检查替代匿名接口断言
- 不支持反思的 channel fallback 为 ReplanContinue（保持现有行为）

### R5: Telegram 兼容性
- Telegram adapter 实现两个新接口
- 将现有审批逻辑（发送 inline keyboard + 等待 callback + 超时处理）封装到接口方法内部
- pendingApprovals 和 pendingReflections 的 sync.Map 从 Gateway/Reflector 移动到 Telegram adapter 内部管理
- 所有现有测试通过，行为无变化

### R6: Channel 注入
- Gateway 提供 `AddChannel(ch channel.Channel)` 方法
- `Gateway.Start()` 不再硬编码创建 Telegram adapter
- CLI 命令层负责创建适当的 channel 并注入
