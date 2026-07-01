# 15 · tool — 工具层与拦截链

> 包路径 `internal/tool` · 蓝图 §6「保留：拦截链迁出至 action」

## 职责

工具实现 + 拦截链骨架 + 权限引擎 + 沙箱后端。工具直接在宿主执行（文件工具有路径围栏，但无 OS 级隔离除非走 seatbelt 后端）。行动治理（可逆性/trust/hold/undo）作为拦截链一段从这里挂入（[08-action.md](08-action.md)）。

## 核心类型

```go
// internal/tool/tool.go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx, input []byte) (Result, error)
    RequiresApproval() bool
}

type ToolCapabilities struct {
    IsReadOnly      bool           // 只读无副作用
    IsDestructive   bool           // 可能不可逆变更
    RequiresNetwork bool
    ApprovalMode    string         // "never" | "always" | "auto"
    ParallelSafety  ParallelSafety // "never" | "safe" | "path_scoped"
}
```

可选接口（鸭子类型，按需实现）：
- `ReadOnlyTool` / `CapableTool`：声明只读 / 富能力 → 决定并发安全。
- `PathScopedTool`：`ExtractPaths` → 写文件工具（file_write/edit）按路径去重，允许并行写不同文件、阻止并发写同文件。
- `AvailableTool`：运行时可用性（外部 binary/shell 可能缺失）。

`Registry`（`sync.RWMutex` map）：`Register`/`Get`/`All`/`UnregisterByPrefix`。`Get` 排除不可用工具。

## 路径围栏

```go
func ResolveWorkPath(ctx, path string) (string, error)  // 拒绝逃出工作目录的 .. / 绝对路径
```

`resolvePathInRoot`：`filepath.Rel(root, resolved)`，若 `rel == ".."` 或前缀 `../` 或绝对 → 报错 "escapes working directory"。这是文件工具的字符串级围栏；undo executor 的 `fencedRealPath` 再加 `EvalSymlinks` 父目录重校验闭中间 symlink 逃逸（[08-action.md](08-action.md)）。

## 拦截链

```go
type ToolInterceptor interface {
    Name() string
    Intercept(ctx, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
}
type InterceptorChain struct { ... }  // 反序包裹 final func
```

**确切链序**（`subsystem_tool.go:185-223`）：

```
permission → hook → user_hooks → read_before_edit → [verify] → [audit] → action → activity
```

| 段 | 拦截器 | 职责 |
|---|---|---|
| permission | `PermissionInterceptor` | 权限引擎决策 + 审批（notifier/approver/audit sink/decision reporter）|
| hook | `HookInterceptor` | 内置 hook（git/workdir 上下文注入）|
| user_hooks | `userHookInterceptor` | 用户 YAML hook（pre/post tool use）|
| read_before_edit | `ReadBeforeEditInterceptor` | 编辑前确认已读 |
| verify | `VerifyInterceptor`（条件）| 写类工具后客观判据（test/lint/build 提示）|
| audit | `AuditInterceptor`（条件）| 审计日志 |
| **action** | `action.Interceptor` | **可逆性分类 + trust + hold + undo + receipt**（[08-action.md](08-action.md)）|
| activity | `ActivityInterceptor` | 最内层，包真实执行最紧，只报通过 permission/hook 的工具 |

action 段在 permission 放行后、activity 之前——只看到被许可的调用与原始执行结果。

## 内置工具

`InitTools` 按配置注册（`subsystem_tool.go`）：

| 类别 | 工具 | 说明 |
|---|---|---|
| 世界模型 | `world_read` `world_edit` `commitment` | 读写三层世界（[06-world.md](06-world.md)）|
| 价值 | `values` | 读写用户价值条目 |
| Shell | `bash`（channel-routing backend）`test_run` | host/seatbelt 后端路由 |
| 文件 | `file_read` `file_write` `file_edit` `file_patch` `file_list` | 路径围栏 |
| 代码探索 | `grep`(GrepCode) `find_symbol` `list_imports` `semantic_search` | 语义搜索需嵌入索引 |
| 网络 | `http` | 需审批 |
| 通讯 | `email`（SMTP 配置时）| 审批经 compensable hold 窗口 |
| 记忆 | `memory` | 查/更新记忆（[18-supporting.md](18-supporting.md)）|
| 元 | `tool_search`（DeferredCatalog）| 按需加载工具目录 |
| 子代理 | `workflow`（multiAgent 就绪时）| 子代理编排 |
| 内核保留 | `episode_close` | episode 运行时注入（非 registry）|

## 沙箱后端

```go
shellBackend := NewChannelRoutingBackend(NewHostShellBackend(), NewSeatbeltShellBackend(),
    cfg.Tools.Exec.Backend == "seatbelt")
```

- `HostShellBackend`：直接 shell。
- `SeatbeltShellBackend`：macOS `sandbox-exec` 沙箱。
- `ChannelRoutingBackend`：按 channel class 路由——远程/定时/internal/background 强制 sandbox，本地按配置默认。sandbox 不可用时非本地来源 fail-closed；只有本地 seatbelt opt-in 可降级 host + 警告。

## 权限引擎

`PermissionEngine`（`permissions.go`）：按 `PermissionRule`（tool/pattern/path）+ `ToolCapabilities` + channel class profile 决策（autonomous / 需审批）。`NewGatewayToolApprover` 用 Telegram inline 审批（替内存 always-approve）。

## 后台索引

codebase index（嵌入）在 gateway-lifetime ctx 上独立 goroutine 跑（不在 30s init 预算内）——初始全仓索引数百文件每 chunk 一次网络嵌入，不能在 init 内完成，也不阻塞启动；查询嵌入用 per-call ctx 尊重取消。

## 跨包接缝

- **→ action**：行动拦截器挂在链上。
- **→ world / values / memory**：对应工具读写。
- **← gateway**：`InitTools` 装配；notifier/approver/audit sink 由 gateway 提供。
- **→ mcp**：MCP 工具经 adapter 注入 registry。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 拦截链正交 | 可扩展 | permission/hook/verify/action/audit 解耦 |
| 路径围栏 | 安全 | 文件工具不逃出工作目录 |
| 远程触发强制 seatbelt | 宪法 4 分域 | 远程 bash 是真实 RCE 风险面 |
| 能力模型（IsDestructive/ParallelSafety）| 前瞻性 | 并发安全 + 审批策略派生 |

下一篇：[16-channels-agent.md](16-channels-agent.md) — 渠道与运行时集成。
