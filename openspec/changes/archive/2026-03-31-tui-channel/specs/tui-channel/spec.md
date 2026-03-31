## Overview

基于 Bubble Tea 框架的终端交互通道，实现 `channel.Channel`、`channel.ApprovalSender` 和 `channel.ReflectionSender` 接口，支持富文本对话、流式输出、工具审批和 Markdown 渲染。

## Requirements

### R1: Channel 接口实现
- 实现 `channel.Channel` 的全部 5 个方法：`Name()`、`Start()`、`Send()`、`SendStreaming()`、`Stop()`
- `Name()` 返回 `"tui"`
- `Start()` 启动 Bubble Tea 主循环，用户输入通过 `InboundHandler` 回调传递给 Gateway
- `Send()` 通过 `program.Send()` 将 agent 响应注入 Bubble Tea 渲染循环
- `SendStreaming()` 返回支持 50ms 节流的 `StreamUpdater` 实现
- `Stop()` 优雅退出 Bubble Tea 程序

### R2: 交互式审批
- 实现 `ApprovalSender` 接口
- 审批请求以对话框形式展示工具名称和输入参数
- 支持快捷键：`y`（批准）、`n`（拒绝）、`a`（始终批准此工具）
- 审批调用阻塞直到用户响应或超时（默认 120s）
- 超时行为：auto-deny

### R3: 反思决策
- 实现 `ReflectionSender` 接口
- 展示反思原因和置信度百分比
- 支持选择：Continue / Adjust / Abort
- 阻塞直到用户响应或超时

### R4: 流式输出
- Agent 响应逐 token 渲染到聊天区域
- 50ms 节流避免过高频率 UI 更新
- 使用 Glamour 将 Markdown 渲染为 ANSI 终端富文本
- 渲染失败时 fallback 到纯文本

### R5: UI 布局
- 三区域布局：Header 状态栏 + Chat Viewport（可滚动）+ Input Area
- Header 显示：程序名、版本、agent 模式、session 状态
- Chat Viewport：用户消息和 agent 响应，带角色标识和时间戳
- Input Area：多行文本输入，Enter 发送，支持换行
- 窗口大小变化时自适应重新布局

### R6: CLI 命令
- `ironclaw tui` 作为独立子命令
- 启动时重定向 slog 到 `~/.ironclaw/tui.log`
- 支持 `--no-color` 禁用样式
- 支持 `--new-session` 强制新建会话
- 默认复用固定 ChannelID `"tui_local"` 实现会话持久化

### R7: 基础命令
- `/new` — 重置当前会话
- `/quit` 或 Ctrl+C — 优雅退出
