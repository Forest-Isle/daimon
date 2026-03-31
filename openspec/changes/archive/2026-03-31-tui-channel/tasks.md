## Phase 1: Channel 抽象层重构

- [x] 1.1 在 `internal/channel/channel.go` 中添加 `ApprovalSender` 接口：`SendApprovalRequest(ctx, target, toolName, input) (bool, error)`
- [x] 1.2 在 `internal/channel/channel.go` 中添加 `ReflectionSender` 接口：`SendReflectionRequest(ctx, target, reason, confidence) (ReplanDecision, error)`
- [x] 1.3 在 `internal/channel/channel.go` 中添加 `ReplanDecision` 类型定义（从 agent 包移动或重新定义，避免循环依赖）
- [x] 1.4 重构 `internal/gateway/gateway.go` 的 `handleApproval()`：将 `ch.(*telegram.Adapter)` 类型断言替换为 `ch.(channel.ApprovalSender)` 接口检查，fallback 仍为 auto-approve
- [x] 1.5 重构 `internal/agent/reflect.go` 的 `RequestReplanApproval()`：将匿名接口断言替换为 `ch.(channel.ReflectionSender)` 接口检查，fallback 仍为 ReplanContinue
- [x] 1.6 让 `internal/channel/telegram/adapter.go` 实现 `ApprovalSender` 接口（将现有 `SendApprovalRequest` + pending approval 等待逻辑封装为阻塞调用）
- [x] 1.7 让 `internal/channel/telegram/adapter.go` 实现 `ReflectionSender` 接口（将现有 `SendReflectionRequest` + pending reflection 等待逻辑封装为阻塞调用）
- [x] 1.8 验证：现有 `make test` 全部通过，Telegram 通道行为无变化

## Phase 2: Gateway 注入式 Channel

- [x] 2.1 在 `Gateway` 上新增 `AddChannel(ch channel.Channel)` 方法
- [x] 2.2 重构 `Gateway.Start()`：移除硬编码的 Telegram adapter 创建，改为遍历已注册的 `gw.channels` 启动
- [x] 2.3 重构 `cmd/ironclaw/main.go` 的 `runStart()`：在调用 `gw.Start()` 之前，手动创建 Telegram adapter 并调用 `gw.AddChannel(tg)`
- [x] 2.4 验证：`ironclaw start` 命令行为完全不变

## Phase 3: TUI Adapter 基础实现

- [x] 3.1 创建 `internal/channel/tui/messages.go`：定义自定义 tea.Msg 类型（agentResponseMsg、streamUpdateMsg、streamFinishMsg、approvalRequestMsg、reflectionRequestMsg、errorMsg）
- [x] 3.2 创建 `internal/channel/tui/styles.go`：使用 Lipgloss 定义样式常量（用户消息、agent 消息、系统消息、审批框、状态栏的颜色和边框）
- [x] 3.3 创建 `internal/channel/tui/formatter.go`：集成 Glamour 进行 Markdown→ANSI 渲染，配置 dark 主题，处理渲染错误时 fallback 到纯文本
- [x] 3.4 创建 `internal/channel/tui/model.go`：实现 Bubble Tea Model
  - State：mode（chat/approval/reflection）、messages 列表、viewport、textarea、当前审批请求
  - Init()：初始化 viewport + textarea 组件
  - Update()：按 mode 路由按键事件，处理自定义 tea.Msg
  - View()：渲染 header + chat viewport + input area，审批模式时覆盖渲染审批对话框
- [x] 3.5 创建 `internal/channel/tui/adapter.go`：实现 `channel.Channel` + `channel.ApprovalSender` + `channel.ReflectionSender`
  - `Name()` → `"tui"`
  - `Start(ctx, handler)`：创建 tea.Program，启动 Bubble Tea 主循环；用户输入在 Model.Update 中捕获，构建 InboundMessage 调用 handler（在新 goroutine 中）
  - `Send(ctx, msg)`：通过 `program.Send(agentResponseMsg{text})` 注入
  - `SendStreaming(ctx, target)`：返回 `tuiStreamUpdater`
  - `SendApprovalRequest(ctx, target, toolName, input)`：通过 `program.Send(approvalRequestMsg{...})` 注入，阻塞等待 `chan bool`
  - `SendReflectionRequest(ctx, target, reason, confidence)`：通过 `program.Send(reflectionRequestMsg{...})` 注入，阻塞等待 `chan ReplanDecision`
  - `Stop(ctx)`：调用 `program.Quit()`
- [x] 3.6 创建 `internal/channel/tui/stream.go`：实现 `tuiStreamUpdater`（注：合并到 adapter.go 中）
  - 持有 `*tea.Program` 引用和消息 ID
  - `Update(text)`：使用 `atomic.Value` 存储最新文本，后台 goroutine 每 50ms 通过 `program.Send()` 推送更新
  - `Finish(text)`：发送最终文本，停止后台 goroutine

## Phase 4: TUI 命令与配置

- [x] 4.1 在 `internal/config/config.go` 中添加 `TUIConfig` 结构体（`AutoApprove bool`、`Theme string`）
- [x] 4.2 在 `Config` 结构体中添加 `TUI TUIConfig \`yaml:"tui"\`` 字段
- [x] 4.3 创建 `cmd/ironclaw/tui.go`：添加 `tui` 子命令
  - 加载配置（复用现有配置加载逻辑）
  - 创建 Gateway
  - 创建 TUI adapter，调用 `gw.AddChannel(tuiAdapter)`
  - 启动 Gateway（不创建 Telegram）
  - TUI adapter 的 Bubble Tea 主循环阻塞直到退出
  - 优雅关闭
- [x] 4.4 TUI 启动时重定向 slog 到文件 `~/.ironclaw/tui.log`（避免 log 输出干扰终端 raw mode）
- [x] 4.5 更新 `configs/ironclaw.example.yaml` 添加 TUI 配置示例
- [x] 4.6 验证：`ironclaw tui` 可启动，输入消息后收到 agent 响应，流式输出正常

## Phase 5: 交互式审批

- [x] 5.1 实现 TUI 审批对话框 View：显示工具名称、输入内容、`[y] Approve [n] Deny [a] Always` 快捷键提示
- [x] 5.2 实现 modeApproval 的按键处理：`y` → 写入 `true` 到 approval channel，`n` → 写入 `false`，`a` → 设置 auto-approve 标记 + 写入 `true`
- [x] 5.3 实现 TUI 反思决策对话框 View：显示原因、置信度、`[1] Continue [2] Adjust [3] Abort` 快捷键提示
- [x] 5.4 实现 modeReflection 的按键处理：写入对应 ReplanDecision 到 reflection channel
- [x] 5.5 验证：配置 `tools.bash.requires_approval: true`，TUI 中执行 bash 工具时弹出审批对话框，按 y 继续、按 n 拒绝

## Phase 6: 体验打磨

- [x] 6.1 实现 `/new` 命令支持：在 TUI 输入 `/new` 时重置会话
- [x] 6.2 实现 `/quit` 或 Ctrl+C 优雅退出
- [x] 6.3 实现 header 状态栏：显示 IronClaw 版本、agent 模式（simple/cognitive）、session ID
- [x] 6.4 实现消息时间戳显示
- [x] 6.5 实现终端窗口大小变化自适应（处理 tea.WindowSizeMsg）
- [x] 6.6 支持 `--no-color` 和 `--plain` 命令行参数，禁用样式和 Markdown 渲染
- [x] 6.7 更新 CLAUDE.md 文档，添加 TUI 相关说明
- [x] 6.8 验证：完整功能测试（对话、流式、审批、退出、窗口调整）
