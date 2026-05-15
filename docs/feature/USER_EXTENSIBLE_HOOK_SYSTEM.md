# User-Extensible Hook System

**日期**: 2026-05-15
**范围**: 新增 `internal/hook/user_hooks.go`，用户可在 `~/.IronClaw/hooks/` 放置脚本，在 agent 事件上自动执行——对标 Claude Code hooks。

## 概述

Claude Code 的 hooks 系统让用户在特定事件（工具调用前后、消息到达、停止等）时自动运行脚本。IronClaw 此前仅有内部 HookInterceptor（代码级 hook），用户无法扩展。

`UserHookManager` 扫描 `~/.IronClaw/hooks/` 目录，按文件命名约定发现和分类 hook 脚本，在对应事件触发时传递 JSON payload 到脚本 stdin 并执行。

## 架构

### Hook 事件类型

```go
type HookEventType string

const (
    HookPreToolUse    HookEventType = "pre_tool_use"     // 工具执行前
    HookPostToolUse   HookEventType = "post_tool_use"    // 工具执行后
    HookOnUserMessage HookEventType = "on_user_message"  // 用户发消息
    HookPreCompact    HookEventType = "pre_compact"      // 上下文压缩前
    HookOnStop        HookEventType = "on_stop"          // agent 停止
    HookNotification  HookEventType = "notification"     // 系统通知
)
```

### 文件命名约定

```
{priority}_{event}_{name}.{ext}

示例:
10_pre_tool_use_security_check.sh     # priority=10, 最先执行
20_pre_tool_use_format_validate.py    # priority=20, 第二批
50_on_stop_cleanup.sh                 # priority=50
99_post_tool_use_logger.rb            # priority=99, 最后执行
```

优先级决定执行顺序（低→高）。非可执行文件被静默跳过。

### UserHookManager

```go
type UserHookManager struct {
    hooksDir string
    hooks    map[HookEventType][]UserHook
    mu       sync.RWMutex
    timeout  time.Duration          // 全局超时（默认 30s）
}

type UserHook struct {
    Name     string                 // 文件名
    Path     string                 // 脚本完整路径
    Event    HookEventType          // 触发事件
    Priority int                    // 执行顺序（低=先）
    Timeout  time.Duration          // 每 hook 超时
}
```

### API

| 方法 | 说明 |
|------|------|
| `NewUserHookManager(dir, timeout)` | 扫描 hooksDir，加载并按优先级排序所有 hook |
| `ReloadHooks()` | 重新扫描目录，检测新增/删除的 hook |
| `RunHooks(ctx, event, payload)` | 按优先级执行所有匹配 hook，返回 `[]HookResult` |
| `ListHooks()` | 返回所有已加载的 hook |
| `HasHooks(event)` | 快速检查某事件是否有 hook |

### HookResult

```go
type HookResult struct {
    HookName string
    Event    HookEventType
    Success  bool
    ExitCode int
    Output   string         // stdout (trimmed)
    Error    string         // stderr 或执行错误
    Duration time.Duration
}
```

### JSON Payload（stdin）

每个事件类型的 payload 结构：

**pre_tool_use / post_tool_use**:
```json
{"tool_name": "bash", "tool_input": "ls -la", "tool_output": "...", "tool_error": ""}
```

**on_user_message**:
```json
{"user_id": "telegram_123", "message": "帮我重构这个函数"}
```

**pre_compact**:
```json
{"session_id": "abc-def", "token_count": 85000}
```

**on_stop**:
```json
{"reason": "user_requested"}
```

**notification**:
```json
{"message": "Memory consolidated: 3 facts promoted"}
```

### 拦截器集成

`userHookInterceptor` 实现 `tool.ToolInterceptor`，在工具执行前后调用 `UserHookManager.RunHooks`：

```go
func (u *userHookInterceptor) Intercept(ctx, call, next) (*ToolResult, error) {
    // 1. Pre-tool hooks
    u.mgr.RunHooks(ctx, HookPreToolUse, prePayload)

    // 2. 执行实际工具
    result, err := next(ctx, call)

    // 3. Post-tool hooks
    u.mgr.RunHooks(ctx, HookPostToolUse, postPayload)

    return result, err
}
```

在拦截器链中的位置：Permission → Hook (internal) → **User Hooks** → Sandbox → Audit。

### 执行模型

- 每个 hook 在独立进程中运行（`os/exec.CommandContext`）
- 超时：hook 级别 > manager 全局默认（30s）
- 错误不致命：单个 hook 失败不影响后续 hook 或工具执行
- 不阻塞：所有 hook 在同一事件内同步执行，但不同事件间异步
- stdin 传递 JSON payload，hook 脚本自行解析

## 配置

Hook 脚本放置在 `~/.IronClaw/hooks/`。目录在 `init_tools.go` 中自动解析：

```go
hooksDir := filepath.Join(home, ".IronClaw", "hooks")
gw.userHookMgr = hook.NewUserHookManager(hooksDir, 30*time.Second)
```

## 文件

| 文件 | 说明 |
|------|------|
| `internal/hook/user_hooks.go` | UserHookManager + Hook 发现/排序/执行/重载 |
| `internal/hook/user_hooks_test.go` | 发现/执行/超时/优先级/重载/空目录/非可执行文件过滤 |
| `internal/gateway/init_tools.go` | userHookInterceptor 定义 + 拦截器链集成 |
