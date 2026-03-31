## Context

IronClaw 的 channel 架构基于干净的 `channel.Channel` 接口（5 个方法），目前仅有 Telegram 实现。但 Gateway 中存在两处 Telegram 硬编码耦合——`handleApproval()` 对 `*telegram.Adapter` 做类型断言，`reflect.go` 对 `SendReflectionRequest` 接口做匿名类型断言。TUI 通道需要解决这些耦合，同时实现 Bubble Tea 框架与 Gateway 异步消息的双向桥接。

**Current State:**
- Channel 接口：`Name()`, `Start()`, `Send()`, `SendStreaming()`, `Stop()`
- 唯一实现：`internal/channel/telegram/adapter.go`（251行）
- 审批：`gateway.go:483` 硬编码 `ch.(*telegram.Adapter)` 类型断言
- 反思：`reflect.go:142` 匿名接口类型断言 `ch.(interface{ SendReflectionRequest(...) })`
- Channel 初始化：`gateway.go:362-367` 硬编码创建 Telegram adapter

**Constraints:**
- Bubble Tea 是 Elm 架构（单线程 Update 循环），外部事件只能通过 `program.Send()` 注入
- Agent runtime 的 handleMessage 在独立 goroutine 中运行，与 Bubble Tea 主循环异步
- 审批流程需要阻塞 agent goroutine 直到用户在 TUI 中按键响应
- 必须保持 Telegram 通道完全不受影响

**Stakeholders:**
- 开发者：需要快速本地调试，无需 Telegram Bot Token
- 用户：想要更丰富的终端交互体验（Markdown、阶段可视化）
- 系统：需要 channel 抽象干净可扩展

## Goals / Non-Goals

**Goals:**
- 实现基于 Bubble Tea 的 TUI channel adapter，满足完整 Channel 接口
- 将审批和反思决策抽象为可选接口（`ApprovalSender`、`ReflectionSender`）
- 重构 Gateway 支持外部注入 channel，解除 Telegram 硬编码
- 支持流式输出、Markdown 渲染、交互式工具审批
- 通过 `ironclaw tui` 独立命令启动

**Non-Goals:**
- 多 channel 同时运行（TUI + Telegram 共存）——后续支持
- Cognitive agent 阶段实时可视化——Phase 3 增强
- 自定义主题/配色系统——后续增强
- 鼠标交互支持——纯键盘操作

## Decisions

### Decision 1: Bubble Tea v1 作为 TUI 框架

**Choice:** 使用 `github.com/charmbracelet/bubbletea`（v1 稳定版）+ Charm 全家桶。

**Rationale:**
- Go 生态 TUI 事实标准，29k+ stars，活跃维护
- Elm 架构（Model-Update-View）适合状态驱动的聊天 UI
- 丰富的组件生态：bubbles（textarea、viewport）、lipgloss（样式）、glamour（Markdown）
- 业界广泛采用：GitHub CLI（gh）、多个知名 Go 项目

**Alternatives Considered:**
- Bubble Tea v2（charm.land/bubbletea/v2）：2026.02 刚发布，仅一个月，API 可能有变动。待稳定后迁移成本低。
- tview：widget 模式更适合表格/表单型 UI，不适合聊天流式交互
- tcell：过于底层，需要大量手写渲染逻辑
- 纯 readline/bufio：无法实现富 UI 布局（分区、样式、Markdown 渲染）

**Trade-offs:**
- (+) 成熟稳定、社区活跃、组件丰富
- (-) Elm 架构对外部异步事件注入需要 `program.Send()` 桥接
- Mitigation: 通过 Go channel + `program.Send()` 实现清晰的双向通信

### Decision 2: 可选接口模式抽象审批

**Choice:** 定义 `ApprovalSender` 和 `ReflectionSender` 作为可选接口（非 Channel 接口的一部分），通过类型断言检查。

```go
// channel.go
type ApprovalSender interface {
    SendApprovalRequest(ctx context.Context, target MessageTarget,
        toolName, input string) (bool, error)
}

type ReflectionSender interface {
    SendReflectionRequest(ctx context.Context, target MessageTarget,
        reason string, confidence float64) (ReplanDecision, error)
}
```

**Rationale:**
- 不修改 Channel 接口签名，保持向后兼容
- 审批是可选能力——不是所有 channel 都需要（如 webhook、scheduler）
- 将「发送请求 + 等待响应」合并为一个阻塞调用，实现端自行管理内部同步
- Go 惯用的可选接口模式（类似 `io.ReaderAt` 之于 `io.Reader`）

**Alternatives Considered:**
- 扩展 Channel 接口加入审批方法：破坏所有现有实现，且不是所有 channel 都需要审批
- 回调注册模式：增加了 Gateway 和 Channel 之间的耦合
- Gateway 统一管理审批 UI：不同 channel 的审批 UI 差异太大（Telegram inline keyboard vs TUI dialog）

**Trade-offs:**
- (+) 最小改动、向后兼容、各 channel 可独立实现审批 UI
- (-) 每处使用需要类型断言
- Mitigation: 仅 `handleApproval()` 和 `RequestReplanApproval()` 两处需要检查

### Decision 3: Gateway 注入式 Channel 初始化

**Choice:** 新增 `gateway.StartWithChannel()` 方法（或重构 `Start()` 接受 channel 参数），由调用方（CLI command）决定创建哪个 channel。

```go
// gateway.go
func (gw *Gateway) AddChannel(ch channel.Channel) {
    gw.channels[ch.Name()] = ch
}
```

**Rationale:**
- `ironclaw start` 创建 Telegram channel，`ironclaw tui` 创建 TUI channel
- Gateway 不需要知道具体 channel 类型
- 后续添加新 channel（Discord、Slack、Web）零改动

**Alternatives Considered:**
- 配置驱动（在 config 中指定 channel 列表）：过度设计，当前只有两个 channel
- 在 Gateway.Start() 中条件创建：仍然是硬编码，只是换了形式

**Trade-offs:**
- (+) 解耦彻底、扩展性好
- (-) CLI command 层需要负责 channel 创建
- Mitigation: channel 创建逻辑简单（1-3 行代码）

### Decision 4: Bubble Tea 双向通信架构

**Choice:** TUI Adapter 持有 `*tea.Program` 引用，通过自定义 `tea.Msg` 类型桥接 Gateway 调用。

```
Gateway goroutine                    Bubble Tea main thread
       │                                      │
   adapter.Send(msg)  ──program.Send()──▶  Update(agentResponseMsg)
   adapter.Update()   ──program.Send()──▶  Update(streamUpdateMsg)
   adapter.SendApproval() ──program.Send()──▶ Update(approvalRequestMsg)
       │                                      │
       │◀──────────── chan bool ──────────────│ (用户按 y/n)
       │              (阻塞等待)               │
```

**Rationale:**
- `program.Send()` 是 Bubble Tea 的官方外部事件注入方式
- Go channel 用于审批同步：adapter 阻塞在 `<-resultCh`，用户按键后 Model.Update 写入 `resultCh`
- Streaming 通过节流（50ms）避免过高频率的 UI 更新

**Trade-offs:**
- (+) 符合 Elm 架构最佳实践，线程安全
- (-) 需要定义多种自定义 tea.Msg 类型
- Mitigation: 类型数量有限（~5 种），结构清晰

### Decision 5: TUI 模式状态机

**Choice:** Model 维护 `mode` 状态，控制按键路由。

```
modeChat       → 按键发送到 textarea 组件
modeApproval   → y/n/a 直接响应审批
modeReflection → 1/2/3 选择 Continue/Adjust/Abort
```

**Rationale:**
- 审批时需要拦截按键而不是输入到文本框
- 明确的状态机避免模态混乱
- 每种模式的 View 渲染不同的 UI 元素

**Trade-offs:**
- (+) 清晰的模态控制，避免误操作
- (-) 状态切换增加了 Update 函数复杂度
- Mitigation: 各模式处理逻辑独立封装到方法中

### Decision 6: 流式输出节流

**Choice:** TUI StreamUpdater 内部 50ms 节流，通过 `program.Send()` 批量更新。

**Rationale:**
- Agent streaming 每个 token 都调用 `Update()`，频率可能很高（每秒数十次）
- Bubble Tea 有自己的渲染节奏（~60fps），无需每次 token 都触发
- 50ms 节流平衡了实时感和性能

**Implementation:**
```go
type tuiStreamUpdater struct {
    program *tea.Program
    id      string
    latest  atomic.Value  // string
    done    chan struct{}
}
// 后台 goroutine 每 50ms 检查 latest，若有变化则 program.Send()
```

**Trade-offs:**
- (+) 避免 UI 闪烁和 CPU 浪费
- (-) 最多 50ms 延迟（用户不可感知）

## Risks / Trade-offs

### Risk 1: Bubble Tea 与 slog 冲突
**Risk:** slog 输出会干扰 Bubble Tea 的终端控制（raw mode 下 stdout 混乱）。

**Mitigation:**
- TUI 模式启动时将 slog handler 重定向到文件（`~/.ironclaw/tui.log`）
- 或使用 Bubble Tea 的 `tea.WithOutput()` + `tea.LogToFile()`

### Risk 2: 大量文本渲染性能
**Risk:** 长对话历史 + Glamour Markdown 渲染可能导致 viewport 性能问题。

**Mitigation:**
- 使用 viewport 的虚拟滚动（只渲染可见区域）
- 限制渲染的历史消息数量（最近 50 条）
- Glamour 渲染结果缓存（消息 immutable）

### Risk 3: 终端兼容性
**Risk:** 不同终端模拟器对 ANSI escape、Unicode、样式支持不一致。

**Mitigation:**
- Lipgloss 自动检测终端能力（color profile）
- 提供 `--no-color` 和 `--plain` fallback 选项
- 主要测试 iTerm2、Terminal.app、Alacritty、kitty

### Risk 4: 审批超时处理
**Risk:** 用户长时间不响应审批请求，agent goroutine 永久阻塞。

**Mitigation:**
- 审批请求带超时（复用 `CognitiveConfig.ApprovalTimeoutSeconds`，默认 120s）
- 超时后 auto-deny 并在 UI 显示提示
- 用户可通过配置 `tui.auto_approve: true` 跳过审批

## Open Questions

1. **是否需要会话持久化？** TUI 退出后重新启动是否恢复上次对话？
   - 当前 session 已持久化到 SQLite，重启后通过相同 ChannelID 可恢复
   - TUI 的 ChannelID 策略：固定为 `"tui_local"` 或每次生成新 ID？
   - 建议：默认固定 `"tui_local"`，支持 `--new-session` flag

2. **多行输入策略？** Enter 发送还是 Shift+Enter 换行？
   - Bubble Tea textarea 默认支持多行
   - 建议：Enter 发送，Ctrl+J 或 Shift+Enter 换行（类似 Slack/Discord）
