# 安全沙箱与执行隔离 设计文档

**Date**: 2026-04-19
**Status**: Approved
**Scope**: 工具执行安全隔离——拦截器链架构、四级权限、Docker 会话容器、文件路径守卫、网络策略

---

## 概述

IronClaw 的工具（bash、file、http）当前直接在主进程中执行，仅靠 `policy.go` 的子串黑名单和 `permissions.go` 的三级模式匹配做安全控制。本设计引入中间件链架构，将权限判定、容器化沙箱、文件路径限制、网络策略作为可组合的拦截器，统一插入工具执行路径。

**核心决策摘要：**

| 决策点 | 选择 | 理由 |
|---|---|---|
| Docker 隔离粒度 | 按会话（session container） | 命令间状态保持，开销低于 per-call |
| 权限层级 | none/notify/approve/deny 四级 | notify 填补"自动执行"与"需审批"之间的空白 |
| 文件路径限制 | 统一配置（FileGuard + Docker volume） | 单一配置源，两处生效，避免不一致 |
| 默认启用策略 | 优雅降级 | Docker 可用时用容器，不可用时退回宿主机执行 + 软沙箱 |
| 架构模式 | 拦截器链（middleware chain） | Go 惯用模式，可组合，与现有 hook 系统正交 |

---

## 1. 核心架构：拦截器链

在工具执行路径上引入 `ToolInterceptor` 接口，多个拦截器组成有序链。每个拦截器可以短路执行（拒绝）、修改执行方式（重定向到容器）、或透传给下一环。

### 1.1 接口定义

```go
// InterceptorFunc is the function signature for the next step in the chain.
type InterceptorFunc func(ctx context.Context, call *ToolCall) (*ToolResult, error)

// ToolInterceptor wraps tool execution with additional behavior.
type ToolInterceptor interface {
    Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
    Name() string
}

// ToolCall carries all information about a pending tool invocation.
type ToolCall struct {
    ToolName  string
    Input     string            // raw JSON input
    SessionID string            // for session-scoped resources (Docker container, etc.)
    Metadata  map[string]string // extensible context
}

// ToolResult wraps the output of a tool execution through the interceptor chain.
type ToolResult struct {
    Output string // tool output (same format as existing tool.Execute return)
    Error  string // non-empty if the tool was blocked or failed
}

// InterceptorChain composes interceptors into an ordered execution pipeline.
type InterceptorChain struct {
    interceptors []ToolInterceptor
}

func (c *InterceptorChain) Execute(ctx context.Context, call *ToolCall, final InterceptorFunc) (*ToolResult, error) {
    handler := final
    for i := len(c.interceptors) - 1; i >= 0; i-- {
        ic := c.interceptors[i]
        next := handler
        handler = func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
            return ic.Intercept(ctx, call, next)
        }
    }
    return handler(ctx, call)
}
```

### 1.2 执行流

```
LLM 返回 tool_use
    ↓
InterceptorChain.Execute()
    ↓
┌─ PermissionInterceptor ─── none/notify → 继续 ──┐
│                            approve → 等待人工   │
│                            deny → 短路返回      │
├─ HookInterceptor ────────── pre_tool_use hooks ──┤
├─ SandboxInterceptor ─────── bash → Docker 会话  ─┤
│                             file → 路径校验      │
│                             http → URL 过滤      │
│                             其他 → 透传          │
└─ Tool.Execute() ─────────── 实际执行 ────────────┘
```

### 1.3 与现有代码的关系

- 现有 `PermissionEngine` 被包装进 `PermissionInterceptor`，内部逻辑复用
- 现有 `pre_tool_use` hook 成为 `HookInterceptor`
- `act.go` / `concurrent.go` 中的权限检查和执行逻辑统一收敛到 `InterceptorChain`
- `ToolCall.SessionID` 由 agent runtime 在创建 chain 时注入，用于 Docker 会话容器的生命周期绑定

### 1.4 新文件

- `internal/tool/interceptor.go` — 接口定义 + `InterceptorChain`
- `internal/tool/interceptor_permission.go` — 权限拦截器
- `internal/tool/interceptor_sandbox.go` — 沙箱分发拦截器
- `internal/tool/interceptor_hook.go` — hook 拦截器（封装已有逻辑）

---

## 2. 四级权限系统

### 2.1 权限升级

从三级升级到四级，保持向后兼容：

| 新 Action | 旧 Action | 行为 |
|---|---|---|
| `none` | `allow` | 自动执行，用户无感知 |
| `notify` | *(新增)* | 自动执行，向 channel 发送非阻塞通知 |
| `approve` | `ask` | 阻塞等待用户批准 |
| `deny` | `deny` | 直接拒绝 |

配置向后兼容：解析时 `allow` 自动映射为 `none`，`ask` 映射为 `approve`，旧配置无需修改。

### 2.2 通知与审批接口

```go
type PermissionInterceptor struct {
    engine   *PermissionEngine
    notifier ToolNotifier
    approver ToolApprover
}

// ToolNotifier — channel 实现此接口接收通知
type ToolNotifier interface {
    NotifyToolExecution(ctx context.Context, call *ToolCall) error
}

// ToolApprover — channel 实现此接口支持审批（已有 ApprovalSender 可适配）
type ToolApprover interface {
    RequestApproval(ctx context.Context, call *ToolCall) (approved bool, err error)
}
```

### 2.3 拦截逻辑

```go
func (p *PermissionInterceptor) Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
    result := p.engine.Evaluate(call.ToolName, call.Input)

    switch result.Action {
    case PermissionNone:
        return next(ctx, call)
    case PermissionNotify:
        _ = p.notifier.NotifyToolExecution(ctx, call)
        return next(ctx, call)
    case PermissionApprove:
        approved, err := p.approver.RequestApproval(ctx, call)
        if err != nil || !approved {
            return &ToolResult{Error: "execution denied by user"}, nil
        }
        return next(ctx, call)
    case PermissionDeny:
        return &ToolResult{Error: fmt.Sprintf("tool %s denied by policy: %s", call.ToolName, result.Reason)}, nil
    }
    return next(ctx, call)
}
```

### 2.4 Channel 适配

- **Telegram**：已有 `ApprovalSender`（inline keyboard），适配 `ToolApprover`；新增 `ToolNotifier` 实现为发送一条普通消息
- **TUI**：已有交互式审批对话框，适配 `ToolApprover`；`ToolNotifier` 实现为显示一行通知文本
- **不支持通知的 channel**：`notify` 降级为 `none`（静默执行）

### 2.5 配置示例

```yaml
permissions:
  default: approve
  rules:
    - tool: "file_read"
      action: none
    - tool: "bash"
      pattern: "ls *"
      action: notify
    - tool: "bash"
      pattern: "rm *"
      action: approve
    - tool: "http"
      action: notify
```

---

## 3. Docker 会话容器沙箱

### 3.1 核心概念

每个 agent 会话对应一个长驻 Docker 容器。该会话内所有 bash 调用通过 `docker exec` 转发到同一容器中执行，命令间状态保持。

### 3.2 DockerSessionManager

```go
type DockerSessionManager struct {
    mu       sync.Mutex
    sessions map[string]*DockerSession
    config   DockerSandboxConfig
    available bool
}

type DockerSession struct {
    containerID string
    sessionID   string
    workDir     string
    createdAt   time.Time
    lastUsedAt  time.Time
}

type DockerSandboxConfig struct {
    Enabled         bool     `yaml:"enabled"`
    Image           string   `yaml:"image"`
    NetworkMode     string   `yaml:"network"`
    MemoryLimit     string   `yaml:"memory_limit"`
    CPULimit        string   `yaml:"cpu_limit"`
    AllowedDirs     []string // 从 sandbox.allowed_directories 继承
    ReadOnlyDirs    []string // 从 sandbox.readonly_directories 继承
    IdleTimeout     time.Duration `yaml:"idle_timeout"`
}
```

### 3.3 容器生命周期

```
会话首次 bash 调用
    ↓
docker create --name ironclaw-sandbox-{sessionID}
    -v {workspace}:{workspace}        ← allowed_directories 中的读写目录
    -v {readonly}:{readonly}:ro       ← readonly_directories 中的只读目录
    --memory {limit} --cpus {limit}
    --network {network_mode}
    --label ironclaw=sandbox
    {image} sleep infinity
    ↓
docker start {containerID}
    ↓
每次 bash 调用 → docker exec -w {workdir} {containerID} bash -c "{command}"
    ↓
空闲超时 (idle_timeout) or 会话结束
    ↓
docker rm -f {containerID}
```

### 3.4 SandboxInterceptor 中的 bash 处理

```go
func (s *SandboxInterceptor) interceptBash(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error) {
    if !s.dockerMgr.Available() {
        return next(ctx, call) // 降级：宿主机执行
    }

    session, err := s.dockerMgr.GetOrCreate(ctx, call.SessionID)
    if err != nil {
        return nil, fmt.Errorf("sandbox: failed to create container: %w", err)
    }

    command := extractCommand(call.Input)
    stdout, stderr, exitCode, duration, err := session.Exec(ctx, command)
    if err != nil {
        return nil, err
    }

    return &ToolResult{
        Output: formatBashResult(stdout, stderr, exitCode, duration),
    }, nil
}
```

### 3.5 与现有 backend_docker.go 的关系

不复用。`backend_docker.go` 面向"运行整个子 agent"（`docker run --rm -i` + JSON IPC），而 `DockerSessionManager` 面向"单次命令执行"（`docker create` + `docker exec`），抽象层级完全不同。Docker 镜像可共用同一个 `ironclaw-sandbox` 基础镜像。

### 3.6 容器清理

- **正常退出**：`Gateway.Stop()` 调用 `DockerSessionManager.CleanupAll()`
- **异常退出保护**：启动时检查遗留的 `ironclaw-sandbox-*` 容器（通过 `--label ironclaw=sandbox`），清理孤儿
- **空闲回收**：后台 goroutine 每分钟检查 `lastUsedAt`，超过 `idle_timeout` 的容器自动销毁

### 3.7 新文件

- `internal/sandbox/docker_session.go` — `DockerSessionManager` + `DockerSession`
- `internal/sandbox/docker_probe.go` — Docker 可用性探测

---

## 4. 文件系统守卫

### 4.1 FileGuard

```go
type FileGuard struct {
    allowedDirs  []string // 规范化后的绝对路径
    readonlyDirs []string
}

func NewFileGuard(allowed, readonly []string) (*FileGuard, error)

func (g *FileGuard) ValidateAccess(path string, write bool) error {
    // 1. filepath.Abs + filepath.EvalSymlinks 规范化
    // 2. 检查是否在任一 allowedDir 下（strings.HasPrefix）
    // 3. 如果是写操作，检查不在 readonlyDirs 中
    // 4. 防止 path traversal：规范化后再检查，阻止 "../" 逃逸
}
```

### 4.2 安全要点

- **Symlink 解析**：先 `EvalSymlinks` 再判断前缀，防止 symlink 指向范围外
- **规范化顺序**：`Abs → Clean → EvalSymlinks → HasPrefix`
- **空配置 = 不限制**：`allowed_directories` 为空时 FileGuard 不生效，保持向后兼容
- **复用现有 `extractFilePath`**：`permissions.go` 中已有此函数

### 4.3 新文件

- `internal/sandbox/file_guard.go`

---

## 5. 网络策略

### 5.1 NetworkPolicy

```go
type NetworkPolicy struct {
    mode      NetworkPolicyMode // whitelist | blacklist | none
    whitelist []string          // host 或 CIDR
    blacklist []string
}

func (p *NetworkPolicy) CheckURL(rawURL string) error {
    // 1. url.Parse 提取 host
    // 2. DNS 解析 host → IP（防止域名绕过 IP 黑名单）
    // 3. 按 mode 校验
}
```

### 5.2 内置默认黑名单

```go
var defaultBlacklist = []string{
    "169.254.169.254",         // AWS/GCP metadata endpoint
    "metadata.google.internal",
    "127.0.0.1",
    "localhost",
    "0.0.0.0",
    "[::1]",
}
```

用户配置的 blacklist 追加到内置黑名单之上。

### 5.3 新文件

- `internal/sandbox/network_policy.go`

---

## 6. Gateway 集成

### 6.1 初始化流程（init_tools.go 变更）

```
NewRegistry → NewPolicy → 注册工具
→ NewFileGuard(cfg.Sandbox)
→ NewNetworkPolicy(cfg.Sandbox.Network)
→ NewDockerSessionManager(cfg.Sandbox.Bash.Docker)
→ NewPermissionInterceptor(permEngine, channel)
→ NewHookInterceptor(hookManager)
→ NewSandboxInterceptor(dockerMgr, fileGuard, networkPolicy)
→ NewInterceptorChain(permissionIC, hookIC, sandboxIC)
→ runtime.SetInterceptorChain(chain)
```

### 6.2 act.go 改造

现有分散逻辑收敛为：

```go
result, err := r.interceptorChain.Execute(ctx, &ToolCall{
    ToolName:  name,
    Input:     input,
    SessionID: r.sessionID,
}, func(ctx context.Context, call *ToolCall) (*ToolResult, error) {
    return tool.Execute(ctx, call.Input)
})
```

`concurrent.go` 做相同改造。

### 6.3 Gateway.Stop() 新增

```go
func (gw *Gateway) Stop() {
    // ... 现有清理 ...
    if gw.dockerSessionMgr != nil {
        gw.dockerSessionMgr.CleanupAll()
    }
}
```

---

## 7. 配置结构

```yaml
sandbox:
  enabled: true

  allowed_directories:
    - "${WORKSPACE_DIR}"
    - "/tmp/ironclaw"
  readonly_directories:
    - "${HOME}/.ssh"

  bash:
    backend: docker          # docker | host
    docker:
      image: "ironclaw-sandbox:latest"
      network: none          # none | bridge | host
      memory_limit: "512m"
      cpu_limit: "1.0"
      idle_timeout: 30m

  network:
    mode: blacklist          # none | blacklist | whitelist
    blacklist:
      - "internal.corp.com"
    whitelist: []
```

对应 Go 结构新增于 `internal/config/config.go`：

```go
type SandboxConfig struct {
    Enabled             bool     `yaml:"enabled"`
    AllowedDirectories  []string `yaml:"allowed_directories"`
    ReadonlyDirectories []string `yaml:"readonly_directories"`
    Bash                BashSandboxConfig `yaml:"bash"`
    Network             NetworkConfig     `yaml:"network"`
}

type BashSandboxConfig struct {
    Backend string              `yaml:"backend"`
    Docker  DockerSandboxConfig `yaml:"docker"`
}

type NetworkConfig struct {
    Mode      string   `yaml:"mode"`
    Blacklist []string `yaml:"blacklist"`
    Whitelist []string `yaml:"whitelist"`
}
```

---

## 8. 降级矩阵

| 组件 | Docker 可用 | Docker 不可用 |
|---|---|---|
| bash 沙箱 | 容器内执行 | 宿主机执行 + policy 黑名单 + 日志警告 |
| FileGuard | 路径校验 | 路径校验（不依赖 Docker） |
| NetworkPolicy | URL 过滤 | URL 过滤（不依赖 Docker） |
| 四级权限 | 正常 | 正常（不依赖 Docker） |

`sandbox.enabled = false` 时，`SandboxInterceptor` 变为透传，仅 `PermissionInterceptor` + `HookInterceptor` 生效，行为与当前版本完全一致。

---

## 9. 测试策略

| 层级 | 覆盖内容 | 方式 |
|---|---|---|
| 单元测试 | `InterceptorChain` 组合、短路、顺序 | mock interceptor |
| 单元测试 | `FileGuard` 路径校验、symlink、traversal | 临时目录 + symlink |
| 单元测试 | `NetworkPolicy` URL/IP 黑白名单 | 纯函数测试 |
| 单元测试 | `PermissionInterceptor` 四级行为 | mock notifier/approver |
| 集成测试 | Docker 会话容器创建/exec/清理 | `//go:build docker` tag |
| 集成测试 | 完整链路：权限 → 沙箱 → 执行 | `TestInterceptorChainIntegration` |
| 向后兼容 | 空 sandbox 配置 = 现有行为 | 现有测试不修改应仍然通过 |

---

## 10. 新增文件清单

| 文件路径 | 职责 |
|---|---|
| `internal/tool/interceptor.go` | `ToolInterceptor` 接口、`ToolCall`、`InterceptorChain` |
| `internal/tool/interceptor_permission.go` | `PermissionInterceptor`、`ToolNotifier`/`ToolApprover` 接口 |
| `internal/tool/interceptor_sandbox.go` | `SandboxInterceptor`（按工具类型分发） |
| `internal/tool/interceptor_hook.go` | `HookInterceptor`（封装现有 hook 逻辑） |
| `internal/sandbox/docker_session.go` | `DockerSessionManager` + `DockerSession` |
| `internal/sandbox/docker_probe.go` | Docker 可用性探测 |
| `internal/sandbox/file_guard.go` | `FileGuard` 路径白名单 |
| `internal/sandbox/network_policy.go` | `NetworkPolicy` URL 过滤 |

**修改文件：**

| 文件路径 | 变更 |
|---|---|
| `internal/config/config.go` | 新增 `SandboxConfig` 等结构 |
| `internal/tool/permissions.go` | `PermissionAction` 新增 `none`/`notify`，兼容旧值映射 |
| `internal/agent/act.go` | 工具执行收敛到 `InterceptorChain` |
| `internal/agent/concurrent.go` | 同上 |
| `internal/gateway/init_tools.go` | 初始化沙箱组件、构建拦截器链 |
| `internal/gateway/gateway.go` | `Stop()` 新增容器清理 |
| `configs/ironclaw.example.yaml` | 新增 `sandbox` 配置段 |
