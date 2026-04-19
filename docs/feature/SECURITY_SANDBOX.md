# 安全沙箱与执行隔离

**日期**: 2026-04-19
**范围**: 工具执行路径引入拦截器链架构，实现四级权限、Docker 会话容器沙箱、文件路径守卫、网络策略

## 概述

IronClaw 的工具（bash、file、http）此前直接在主进程中执行，安全控制仅依赖 `policy.go` 的子串黑名单和 `permissions.go` 的三级模式匹配（allow/ask/deny）。本次改动引入拦截器链（Interceptor Chain）架构，将权限判定、Hook 触发、容器化沙箱、文件路径限制、网络策略作为可组合的中间件，统一插入工具执行路径。共 16 次提交（+2,013 行代码，涉及 43 个文件），零新增外部依赖。

**核心决策摘要：**

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 架构模式 | 拦截器链（middleware chain） | Go 惯用模式，可组合，与现有 hook 系统正交 |
| Docker 隔离粒度 | 按会话（session container） | 命令间状态保持，开销低于 per-call |
| 权限层级 | none/notify/approve/deny 四级 | `notify` 填补"自动执行"与"需审批"之间的空白 |
| 文件路径限制 | 统一配置（FileGuard + Docker volume） | 单一配置源，两处生效，避免不一致 |
| 默认启用策略 | 优雅降级 | Docker 可用时用容器，不可用时退回宿主机 + 软沙箱 |

## 拦截器链架构

### 核心接口

所有安全层通过统一的 `ToolInterceptor` 接口组成有序链。每个拦截器可以短路执行（拒绝）、修改执行方式（重定向到容器）、或透传给下一环。

```go
type InterceptorFunc func(ctx context.Context, call *ToolCall) (*ToolResult, error)

type ToolInterceptor interface {
    Intercept(ctx context.Context, call *ToolCall, next InterceptorFunc) (*ToolResult, error)
    Name() string
}

type ToolCall struct {
    ToolName  string            // 工具名称
    Input     string            // 原始 JSON 输入
    SessionID string            // 会话 ID（用于 Docker 容器绑定）
    Metadata  map[string]string // 可扩展上下文
}

type ToolResult struct {
    Output   string            // 执行输出
    Error    string            // 非空表示被拦截或执行失败
    Metadata map[string]string
}
```

`InterceptorChain` 将多个拦截器按注册顺序逆序包装为嵌套函数调用，构成洋葱模型：外层拦截器先于内层执行，内层完成后外层可以后处理。

### 执行流

```
LLM 返回 tool_use
    ↓
InterceptorChain.Execute()
    ↓
┌─ PermissionInterceptor ─── none/notify → 继续 ──┐
│                            approve → 等待人工     │
│                            deny → 短路返回        │
├─ HookInterceptor ────────── pre_tool_use hooks ──┤
├─ SandboxInterceptor ─────── bash → Docker 会话  ─┤
│                             file → 路径校验       │
│                             http → URL 过滤       │
│                             其他 → 透传           │
└─ Tool.Execute() ─────────── 实际执行 ────────────┘
```

### 与现有代码的关系

- 现有 `PermissionEngine` 被包装进 `PermissionInterceptor`，内部逻辑完全复用
- 现有 `pre_tool_use` hook 逻辑封装为 `HookInterceptor`
- `act.go`（认知模式）和 `concurrent.go`（简单模式）中分散的权限检查和执行逻辑统一收敛到 `InterceptorChain`
- 当 `interceptorChain` 未设置时（nil），原有内联逻辑照常工作，保持向后兼容

**涉及文件**: `internal/tool/interceptor.go`, `internal/agent/runtime.go`, `internal/agent/act.go`, `internal/agent/concurrent.go`

## 四级权限系统

### 权限升级

从三级升级到四级，保持向后兼容：

| 新 Action | 旧 Action | 行为 |
|-----------|-----------|------|
| `none` | `allow` | 自动执行，用户无感知 |
| `notify` | *(新增)* | 自动执行，向 channel 发送非阻塞通知 |
| `approve` | `ask` | 阻塞等待用户批准 |
| `deny` | `deny` | 直接拒绝，返回错误信息 |

配置解析时 `allow` 自动映射为 `none`，`ask` 映射为 `approve`，旧配置文件无需修改。`parseAction` 函数统一处理新旧两套命名：

```go
func parseAction(s string) PermissionAction {
    switch s {
    case "none", "allow":   return PermissionNone
    case "notify":          return PermissionNotify
    case "approve", "ask":  return PermissionApprove
    case "deny":            return PermissionDeny
    default:                return PermissionApprove
    }
}
```

### 通知与审批接口

`PermissionInterceptor` 依赖两个 channel 层接口：

- **`ToolNotifier`** — 用于 `notify` 级别。channel 实现此接口后，工具执行前会收到非阻塞通知。不实现则 `notify` 降级为 `none`（静默执行）
- **`ToolApprover`** — 用于 `approve` 级别。channel 实现此接口后，工具执行前会阻塞等待用户审批。不实现则自动放行

```go
type ToolNotifier interface {
    NotifyToolExecution(ctx context.Context, call *ToolCall) error
}

type ToolApprover interface {
    RequestApproval(ctx context.Context, call *ToolCall) (approved bool, err error)
}
```

### 配置示例

```yaml
permissions:
  default: approve              # 未匹配任何规则时的默认行为
  rules:
    - tool: "file_read"
      action: none              # 读文件自动执行
    - tool: "bash"
      pattern: "ls *"
      action: notify            # ls 命令自动执行但通知用户
    - tool: "bash"
      pattern: "rm *"
      action: approve           # rm 命令需要人工批准
    - tool: "http"
      action: notify
```

**涉及文件**: `internal/tool/permissions.go`, `internal/tool/interceptor_permission.go`

## Docker 会话容器沙箱

### 核心概念

每个 agent 会话对应一个长驻 Docker 容器（`ironclaw-sandbox-{sessionID}`）。该会话内所有 bash 调用通过 `docker exec` 转发到同一容器中执行，命令间状态保持（环境变量、工作目录、临时文件）。

### DockerSessionManager

管理器维护 `sessionID → DockerSession` 的映射表，线程安全（`sync.Mutex`）。核心方法：

| 方法 | 作用 |
|------|------|
| `GetOrCreate(ctx, sessionID)` | 返回已有会话或创建新容器 |
| `Remove(sessionID)` | 停止并删除指定会话的容器 |
| `CleanupAll()` | 关闭所有容器并停止 idle reaper |
| `Available()` | 报告 Docker 是否可用 |

`DockerSession.Exec(ctx, command)` 通过 `docker exec {containerID} bash -c {command}` 执行命令，返回结构化的 `(stdout, stderr, exitCode, duration, error)`。

### 容器生命周期

```
会话首次 bash 调用
    ↓
docker create --name ironclaw-sandbox-{sessionID}
    --label ironclaw=sandbox
    --network {network_mode}
    --memory {limit} --cpus {limit}
    -v {dir}:{dir}           ← allowed_directories（读写）
    -v {dir}:{dir}:ro        ← readonly_directories（只读）
    {image} sleep infinity
    ↓
docker start {containerID}
    ↓
每次 bash 调用 → docker exec {containerID} bash -c "{command}"
    ↓                        返回结构化 JSON（stdout/stderr/exit_code/duration_ms/sandbox:true）
空闲超时 (idle_timeout) or 会话结束
    ↓
docker rm -f {containerID}
```

### 容器清理机制

| 场景 | 清理方式 |
|------|---------|
| 正常退出 | `Gateway.Stop()` 调用 `DockerSessionManager.CleanupAll()` |
| 异常退出 | 启动时 `CleanupOrphans()` 检查 `--label ironclaw=sandbox` 的遗留容器并清理 |
| 空闲回收 | 后台 goroutine 每分钟检查 `lastUsedAt`，超过 `idle_timeout` 自动销毁 |

### Docker 可用性探测

`ProbeDocker()` 通过执行 `docker info`（5 秒超时）检测 Docker daemon 是否可达。探测结果决定 `DockerSessionManager.Available()` 的返回值。

**涉及文件**: `internal/sandbox/docker_session.go`, `internal/sandbox/docker_probe.go`

## 文件系统守卫

### FileGuard

`FileGuard` 基于配置的 `allowed_directories` 和 `readonly_directories` 校验文件路径的合法性。

**校验流程**：

1. `filepath.Clean` → `filepath.Abs` 规范化路径
2. `filepath.EvalSymlinks` 解析符号链接（防止 symlink 逃逸）
3. `filepath.Rel` 判断目标路径是否为某个 allowed 目录的子路径
4. 若为写操作，额外检查目标路径不在 `readonly_directories` 下

**安全要点**：

- **Symlink 解析**：先 `EvalSymlinks` 再判断子路径关系，防止 symlink 指向白名单之外
- **路径遍历防护**：`../` 在规范化后被消除，`isSubPath` 检查 `filepath.Rel` 结果不以 `..` 开头
- **不存在路径处理**：`resolvePathSafe` 对不存在的路径逐级回溯找到最近的存在祖先进行解析
- **空配置 = 不限制**：`allowed_directories` 为空时 FileGuard 放行所有路径，保持向后兼容
- **统一配置源**：同一份 `allowed_directories` 同时用于 FileGuard 校验和 Docker volume 挂载

**涉及文件**: `internal/sandbox/file_guard.go`

## 网络策略

### NetworkPolicy

`NetworkPolicy` 对 HTTP 工具的请求 URL 实施黑名单/白名单过滤。

| 模式 | 行为 |
|------|------|
| `blacklist` | 拒绝黑名单中的主机（含内置 SSRF 保护名单），其余放行 |
| `whitelist` | 仅允许白名单中的主机，其余拒绝 |
| `none` | 不过滤 |

### 内置 SSRF 保护

无论用户是否配置自定义黑名单，以下地址始终被拦截：

```
169.254.169.254         # AWS/GCP 元数据端点
metadata.google.internal
127.0.0.1
localhost
0.0.0.0
[::1]
```

用户配置的 `blacklist` 条目追加到内置黑名单之上。

### 校验流程

1. `url.Parse` 提取 host
2. `strings.ToLower` 规范化
3. 按模式查 `map[string]bool` 匹配

**涉及文件**: `internal/sandbox/network_policy.go`

## Gateway 集成

### 初始化流程

在 `initToolsAndHooks()` 中，沙箱组件在权限引擎之后初始化：

```
NewRegistry → NewPolicy → 注册工具
→ BuildManager（hook 系统）
→ NewPermissionEngine（权限引擎）
→ [sandbox.enabled?]
    → NewFileGuard(allowed_dirs, readonly_dirs)
    → NewNetworkPolicy(mode, whitelist, blacklist)
    → CleanupOrphans → ProbeDocker → NewDockerSessionManager(config, available)
→ NewInterceptorChain([PermissionInterceptor, HookInterceptor, SandboxInterceptor])
→ runtime.SetInterceptorChain(chain)      ← 简单模式
→ cognitiveAgent.SetInterceptorChain(chain) ← 认知模式
```

### 工具执行路径改造

简单模式（`concurrent.go`）和认知模式（`act.go`）的工具执行逻辑统一为：

```go
if r.interceptorChain != nil {
    return r.executeToolCallViaChain(ctx, ch, sess, target, tc, t)
}
// 否则走原有内联逻辑（向后兼容）
```

`executeToolCallViaChain` / `executeSubTaskViaChain` 将 `Tool.Execute()` 包装为 chain 的 final 函数，由拦截器链统一调度。执行完成后的后处理（结果持久化、压缩、RL 记录、PostToolUse hook）保持不变。

### Gateway.Stop() 新增

```go
if gw.dockerSessionMgr != nil {
    gw.dockerSessionMgr.CleanupAll()
}
```

**涉及文件**: `internal/gateway/init_tools.go`, `internal/gateway/gateway.go`, `internal/gateway/init_agent.go`, `internal/gateway/init_cognitive.go`

## 配置结构

### 新增 SandboxConfig

```go
type SandboxConfig struct {
    Enabled             bool              `yaml:"enabled"`
    AllowedDirectories  []string          `yaml:"allowed_directories"`
    ReadonlyDirectories []string          `yaml:"readonly_directories"`
    Bash                BashSandboxConfig `yaml:"bash"`
    Network             NetworkConfig     `yaml:"network"`
}

type BashSandboxConfig struct {
    Backend string              `yaml:"backend"` // "docker" | "host"
    Docker  DockerSandboxConfig `yaml:"docker"`
}

type DockerSandboxConfig struct {
    Image       string        `yaml:"image"`
    Network     string        `yaml:"network"`       // "none" | "bridge" | "host"
    MemoryLimit string        `yaml:"memory_limit"`
    CPULimit    string        `yaml:"cpu_limit"`
    IdleTimeout time.Duration `yaml:"idle_timeout"`
}

type NetworkConfig struct {
    Mode      string   `yaml:"mode"`      // "none" | "blacklist" | "whitelist"
    Blacklist []string `yaml:"blacklist"`
    Whitelist []string `yaml:"whitelist"`
}
```

### 默认值

| 配置项 | 默认值 |
|--------|--------|
| `sandbox.enabled` | `false` |
| `sandbox.bash.backend` | `"host"` |
| `sandbox.bash.docker.image` | `"ironclaw-sandbox:latest"` |
| `sandbox.bash.docker.network` | `"none"` |
| `sandbox.bash.docker.memory_limit` | `"512m"` |
| `sandbox.bash.docker.cpu_limit` | `"1.0"` |
| `sandbox.bash.docker.idle_timeout` | `30m` |
| `sandbox.network.mode` | `"blacklist"` |

### 完整配置示例

```yaml
sandbox:
  enabled: true

  allowed_directories:
    - "${WORKSPACE_DIR}"
    - "/tmp/ironclaw"
  readonly_directories:
    - "${HOME}/.ssh"

  bash:
    backend: docker
    docker:
      image: "ironclaw-sandbox:latest"
      network: none
      memory_limit: "512m"
      cpu_limit: "1.0"
      idle_timeout: 30m

  network:
    mode: blacklist
    blacklist:
      - "internal.corp.com"
    whitelist: []
```

**涉及文件**: `internal/config/config.go`, `configs/ironclaw.example.yaml`

## 降级矩阵

| 组件 | Docker 可用 | Docker 不可用 |
|------|-------------|---------------|
| bash 沙箱 | 容器内执行（隔离进程、网络、文件系统） | 宿主机执行 + policy 黑名单 + 日志警告 |
| FileGuard | 路径校验生效 | 路径校验生效（不依赖 Docker） |
| NetworkPolicy | URL 过滤生效 | URL 过滤生效（不依赖 Docker） |
| 四级权限 | 正常工作 | 正常工作（不依赖 Docker） |

`sandbox.enabled = false` 时，`SandboxInterceptor` 变为透传（所有工具直接走 `next(ctx, call)`），仅 `PermissionInterceptor` + `HookInterceptor` 生效，行为与改动前完全一致。

## 新增文件清单

| 文件路径 | 职责 |
|---------|------|
| `internal/tool/interceptor.go` | `ToolInterceptor` 接口、`ToolCall`/`ToolResult` 类型、`InterceptorChain` |
| `internal/tool/interceptor_permission.go` | `PermissionInterceptor`、`ToolNotifier`/`ToolApprover` 接口 |
| `internal/tool/interceptor_sandbox.go` | `SandboxInterceptor`（按工具类型分发到 Docker/FileGuard/NetworkPolicy） |
| `internal/tool/interceptor_hook.go` | `HookInterceptor`（封装现有 pre_tool_use hook） |
| `internal/sandbox/docker_session.go` | `DockerSessionManager`、`DockerSession`、`CleanupOrphans` |
| `internal/sandbox/docker_probe.go` | `ProbeDocker` Docker 可用性探测 |
| `internal/sandbox/file_guard.go` | `FileGuard` 路径白名单校验 |
| `internal/sandbox/network_policy.go` | `NetworkPolicy` URL 黑白名单过滤 |

## 修改文件清单

| 文件路径 | 变更 |
|---------|------|
| `internal/tool/permissions.go` | `PermissionAction` 新增 `none`/`notify`，`parseAction` 兼容旧值映射 |
| `internal/tool/permissions_test.go` | 新增 `TestPermissionAction_NoneAndNotify`、`TestPermissionAction_BackwardCompat` |
| `internal/config/config.go` | 新增 `SandboxConfig`/`BashSandboxConfig`/`DockerSandboxConfig`/`NetworkConfig` 及默认值 |
| `internal/gateway/init_tools.go` | 初始化 FileGuard、NetworkPolicy、DockerSessionManager，构建拦截器链 |
| `internal/gateway/gateway.go` | `Gateway` 新增 `dockerSessionMgr`/`interceptorChain` 字段，`Stop()` 清理容器 |
| `internal/gateway/init_agent.go` | 注入 `interceptorChain` 到简单模式 Runtime |
| `internal/gateway/init_cognitive.go` | 注入 `interceptorChain` 到认知模式 CognitiveAgent |
| `internal/agent/runtime.go` | 新增 `interceptorChain` 字段和 `SetInterceptorChain` setter |
| `internal/agent/cognitive.go` | 新增 `SetInterceptorChain` 同时注入 executor 和 inner runtime |
| `internal/agent/concurrent.go` | 新增 `executeToolCallViaChain`，简单模式工具执行走拦截器链 |
| `internal/agent/act.go` | 新增 `executeSubTaskViaChain`，认知模式工具执行走拦截器链 |
| `configs/ironclaw.example.yaml` | 新增 `sandbox:` 配置段及详细注释 |

## 测试

共 37 个测试用例，覆盖全部安全层和集成场景：

| 组件 | 测试文件 | 测试数量 | 覆盖内容 |
|------|---------|----------|---------|
| InterceptorChain | `interceptor_test.go` | 3 | 空链透传、执行顺序（洋葱模型）、短路拦截 |
| PermissionInterceptor | `interceptor_permission_test.go` | 5 | none 放行、notify 通知、deny 拒绝、approve 批准/拒绝 |
| SandboxInterceptor | `interceptor_sandbox_test.go` | 5 | 文件路径拦截/放行、HTTP URL 拦截、禁用透传、未知工具透传 |
| HookInterceptor | `interceptor_hook_test.go` | 1 | 无 hook manager 时透传 |
| FileGuard | `file_guard_test.go` | 6 | 允许路径、拒绝路径、只读写保护、路径遍历防护、symlink 逃逸防护、空配置不限制 |
| NetworkPolicy | `network_policy_test.go` | 5 | 黑名单拦截、SSRF 默认黑名单、白名单允许/拒绝、none 模式放行、无效 URL 拒绝 |
| DockerSessionManager | `docker_session_test.go` | 3 | 不可用时报告状态、可用时报告状态、不可用时 GetOrCreate 返回错误 |
| PermissionEngine | `permissions_test.go` | 2 (新增) | none/notify 新动作、allow→none / ask→approve 向后兼容映射 |
| 全链路集成 | `interceptor_integration_test.go` | 6 (子测试) | bash 放行、bash rm 权限拒绝、file_write 路径内/外、http 安全/危险主机 |
