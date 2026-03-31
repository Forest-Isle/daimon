## Why

IronClaw 当前只支持 Telegram Bot 作为交互通道，本地开发和调试必须依赖 Telegram API，增加了使用门槛和调试复杂度。添加 TUI（Terminal UI）通道可以让用户直接在终端启动交互，实现零依赖的本地体验，同时充分发挥 cognitive agent 多阶段流程的可视化潜力（这在 Telegram 的消息形态中很难表达）。

## What Changes

- 新增 `internal/channel/tui/` 包，基于 Bubble Tea 框架实现 `channel.Channel` 接口的 TUI 适配器
- 新增 `ironclaw tui` CLI 子命令，独立启动 TUI 通道（不启动 Telegram）
- 新增 `channel.ApprovalSender` 和 `channel.ReflectionSender` 可选接口，抽象工具审批和反思决策能力
- **重构** `gateway.go` 的 channel 初始化逻辑，从硬编码 Telegram 改为外部注入式
- **重构** `gateway.go` 的 `handleApproval()` 和 `reflect.go` 的 `RequestReplanApproval()`，将 Telegram 类型断言替换为接口检查
- 新增 `TUIConfig` 配置项
- 新增 Glamour Markdown 渲染支持（终端内富文本展示）

## Capabilities

### New Capabilities
- `tui-channel`: 基于 Bubble Tea 的终端交互通道，支持对话、流式输出、工具审批和 Markdown 渲染
- `channel-approval-abstraction`: 将工具审批和反思决策从 Telegram 硬编码抽象为通用可选接口

### Modified Capabilities
- `memory-search`: 无需修改（通过 Channel 接口透明工作）

## Impact

**代码变更：**
- 新增包：`internal/channel/tui/`（约 6-8 个文件，~900 行）
- 新增文件：`cmd/ironclaw/tui.go`
- 重构文件：`internal/channel/channel.go`（+2 个可选接口）、`internal/gateway/gateway.go`（channel 注入 + 审批接口化）、`internal/agent/reflect.go`（使用 ReflectionSender 接口）、`internal/config/config.go`（+TUIConfig）

**依赖变更：**
- 新增：`github.com/charmbracelet/bubbletea`（TUI 框架）
- 新增：`github.com/charmbracelet/bubbles`（textarea, viewport 组件）
- 新增：`github.com/charmbracelet/lipgloss`（终端样式）
- 新增：`github.com/charmbracelet/glamour`（Markdown 渲染）

**向后兼容性：**
- Telegram 通道功能完全不受影响
- `ironclaw start` 命令行为不变
- 非 Telegram channel 的 auto-approve 行为保持兼容（接口检查 fallback）
