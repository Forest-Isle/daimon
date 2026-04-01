# 05 - Channel 通道系统

## 文件结构

```
internal/channel/
├── channel.go           # 核心接口定义
├── message.go           # 消息类型定义
├── telegram/
│   ├── adapter.go       # Telegram Bot 适配器
│   └── formatter.go     # Markdown → Telegram 格式化
└── tui/
    ├── adapter.go       # TUI 适配器
    ├── model.go         # Bubble Tea Model
    ├── styles.go        # Lipgloss 样式
    ├── messages.go      # 自定义消息类型
    └── formatter.go     # Markdown → 终端格式化
```

## 一、核心接口

### Channel 接口

```go
type Channel interface {
    Name() string
    Start(ctx context.Context, handler InboundHandler) error
    Send(ctx context.Context, msg OutboundMessage) error
    SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error)
    Stop(ctx context.Context) error
}
```

### StreamUpdater 接口

```go
type StreamUpdater interface {
    Update(text string) error   // 增量更新（流式文本）
    Finish(text string) error   // 最终完成
}
```

### 可选接口

```go
// 交互式工具审批（如 Telegram 的内联键盘）
type ApprovalSender interface {
    SendApprovalRequest(ctx, target, toolName, input) (bool, error)
}

// 交互式 Replan 决策
type ReflectionSender interface {
    SendReflectionRequest(ctx, target, reason, confidence) (ReplanDecision, error)
}
```

**设计模式**：通道不必实现所有接口。未实现 `ApprovalSender` 的通道自动批准工具执行；未实现 `ReflectionSender` 的通道默认继续（不重新规划）。

### 消息类型

```go
type InboundMessage struct {
    Channel   string
    ChannelID string
    UserID    string
    UserName  string
    Text      string
}

type OutboundMessage struct {
    Channel   string
    ChannelID string
    Text      string
}

type MessageTarget struct {
    Channel   string
    ChannelID string
}
```

## 二、Telegram 适配器

### 核心功能

```
telegram.Adapter
    │
    ├── Name() → "telegram"
    ├── Start() → 启动长轮询 (getUpdates)
    ├── Send() → bot.Send(message)
    ├── SendStreaming() → 通过 editMessage 模拟流式
    ├── Stop() → 停止轮询
    │
    ├── ApprovalSender ✅
    │   └── 发送内联键盘 [✅ Approve] [❌ Deny]
    │       等待回调 → 返回结果
    │
    └── ReflectionSender ✅
        └── 发送内联键盘 [Continue] [Adjust] [Abort]
            等待回调 → 返回决定
```

### 流式输出实现

```
SendStreaming() → telegramStreamUpdater
    │
    ├── 首次 Update() → bot.Send() 创建消息
    ├── 后续 Update() → bot.Edit() 编辑消息
    │   （限流：避免 Telegram API rate limit）
    └── Finish() → 最终 bot.Edit()
```

### 安全特性

- 白名单用户 ID（`allowed_user_ids`）
- 工具执行审批（内联键盘）
- 审批超时（`approval_timeout_seconds`）

## 三、TUI 适配器

### 技术栈

```
Bubble Tea (Elm Architecture)
    │
    ├── Glamour     → Markdown 渲染
    ├── Lipgloss    → 样式/布局
    └── Bubbles     → 输入组件
```

### Model 结构

```go
type Model struct {
    // 输入
    textarea    textarea.Model

    // 显示
    messages    []displayMessage
    viewport    viewport.Model

    // 状态
    streaming   bool
    waitingApproval bool

    // 通道
    inbound     chan InboundMessage
    outbound    chan OutboundMessage
}
```

### 交互流程

```
用户输入 → textarea.Model
    │
    ├── Enter → 发送消息到 inbound channel
    │           Gateway.handleInbound() 接收
    │
    ├── Agent 回复 → outbound channel → viewport 显示
    │
    ├── 流式输出 → StreamUpdater → 实时更新 viewport
    │
    ├── 工具审批 → ApprovalSender
    │   └── 显示 [y/n] 对话框
    │       ├── y → 批准
    │       └── n → 拒绝
    │
    └── Replan 决策 → ReflectionSender
        └── 显示选项对话框
            ├── Continue
            ├── Adjust
            └── Abort
```

### 样式系统（styles.go）

使用 Lipgloss 定义的终端样式：
- 用户消息样式
- 助手消息样式
- 工具调用/结果样式
- 错误样式
- 状态栏样式

### Markdown 渲染（formatter.go）

使用 Glamour 将 Markdown 渲染为终端富文本：
- 代码高亮
- 表格格式化
- 链接着色
- 列表缩进

## 四、通道扩展模式

添加新通道只需：

1. 实现 `Channel` 接口
2. （可选）实现 `ApprovalSender` / `ReflectionSender`
3. 在 `gateway.New()` 或 `main.go` 中注册

```go
// 示例：添加 Discord 通道
type DiscordAdapter struct { ... }

func (d *DiscordAdapter) Name() string { return "discord" }
func (d *DiscordAdapter) Start(ctx, handler) error { ... }
func (d *DiscordAdapter) Send(ctx, msg) error { ... }
func (d *DiscordAdapter) SendStreaming(ctx, target) (StreamUpdater, error) { ... }
func (d *DiscordAdapter) Stop(ctx) error { ... }

// 可选：支持交互式审批
func (d *DiscordAdapter) SendApprovalRequest(ctx, target, tool, input) (bool, error) { ... }
```

## 设计亮点

1. **最小接口**：核心 Channel 接口仅 5 个方法
2. **可选增强**：ApprovalSender/ReflectionSender 通过类型断言渐进增强
3. **流式抽象**：StreamUpdater 统一不同平台的流式输出模式
4. **解耦路由**：InboundHandler 回调模式，Channel 不感知 Agent
