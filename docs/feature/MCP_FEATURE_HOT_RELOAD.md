# MCP 服务器热重载特性

**日期**: 2026-04-21
**范围**: `internal/mcp/manager.go` + `internal/gateway/features.go` + `internal/feature/`
**前置**: [FEATURE_REGISTRY_RUNTIME_CONTROL.md](FEATURE_REGISTRY_RUNTIME_CONTROL.md)（Feature Registry 基础设施）

## 概述

MCP（Model Context Protocol）服务器允许 IronClaw 通过标准协议调用外部工具（GitHub、数据库、文件系统等）。在引入热重载之前，MCP 服务器通过 `Gateway.Start()` 中的 `StartServers()` 批量启动，无法在运行时控制单个服务器。这带来了几个痛点：

1. **故障传播**：单个 MCP 服务器连接失败会导致整批日志噪声，且只有重启才能触发重试
2. **无法隔离停用**：若 `mcp_github` 服务器出现异常，无法单独关闭而不影响其他服务器
3. **调试困难**：用户看不到哪些 MCP 工具当前可用，无法区分"服务器未配置"与"服务器已启动但工具未注册"

**解决方案**：将每个配置的 MCP 服务器注册为 Feature Registry 中的独立热重载特性（`mcp_<name>`）。用户可以通过 `/feature enable|disable mcp_<name>` 在运行时启停单个 MCP 服务器，状态持久化到 `~/.IronClaw/feature_state.json`，重启后恢复。

## 特性注册

在 `registerFeatures()` 中，每个 `cfg.Tools.MCP.Servers` 条目生成一个对应特性：

```go
// internal/gateway/features.go
for name, srv := range cfg.Tools.MCP.Servers {
    name := name  // 捕获循环变量，避免闭包问题
    srv := srv
    cmdName := srv.Command

    r.Register(feature.Feature{
        Name:          "mcp_" + name,
        Description:   fmt.Sprintf("MCP server: %s (%s)", name, cmdName),
        Default:       true,              // 已配置的 MCP 服务器默认启动
        Phase:         feature.PhaseStart, // 在 Gateway.Start() 阶段初始化
        HotReloadable: true,              // 支持运行时 enable/disable
        AutoDetect: func(ctx context.Context) feature.DetectResult {
            // 不在 AutoDetect 阶段检测命令是否存在 — MCP 启动时会给出
            // 更明确的错误信息（如 "command not found"）
            return feature.DetectResult{Available: true}
        },
    })
}
```

### 设计决策：为什么 AutoDetect 始终返回 true

MCP AutoDetect 故意返回 `Available: true`，原因是：

- AutoDetect 在 `ResolveAndInit()` 中同步执行，此时 `Gateway.Start()` 尚未运行
- 探测命令是否存在（`which npx`）既不可靠又平台相关
- `startServerWithRetry` 在实际连接时会给出精确的错误信息（exit code、stderr）
- 若连接失败，`Gateway.Start()` 会调用 `features.Disable("mcp_"+name)` 更新状态

## 生命周期钩子绑定

在 `bindFeatureLifecycleHooks()` 中，为每个 `mcp_*` 特性绑定 OnEnable（启动服务器）和 OnDisable（停止服务器）：

```go
// internal/gateway/features.go
func (gw *Gateway) bindFeatureLifecycleHooks() {
    // ...dashboard, evolution, scheduler 的钩子...

    // MCP servers — 遍历所有已注册的 mcp_* 特性
    for _, srv := range gw.features.List() {
        if !strings.HasPrefix(srv.Name, "mcp_") {
            continue
        }
        serverName := strings.TrimPrefix(srv.Name, "mcp_")
        srvCfg, ok := gw.cfg.Tools.MCP.Servers[serverName]
        if !ok {
            continue
        }
        sName := serverName  // 捕获变量
        sCfg := srvCfg

        _ = gw.features.SetOnEnable("mcp_"+sName, func(ctx context.Context) error {
            return gw.mcpManager.StartServer(ctx, sName, sCfg, gw.tools)
        })
        _ = gw.features.SetOnDisable("mcp_"+sName, func(ctx context.Context) error {
            gw.mcpManager.StopServer(sName, gw.tools)
            return nil
        })
    }
}
```

### 为什么使用 SetOnEnable 而非在 Register 时传入

`bindFeatureLifecycleHooks()` 在所有子系统初始化**之后**才调用（`Gateway.Start()` 末尾），此时 `gw.mcpManager` 已经构建完毕。如果在 `registerFeatures()` 时传入 OnEnable 闭包，`gw.mcpManager` 此时还是 `nil`，会导致空指针崩溃。

## StartServer —— 热重载专用的公开方法

`mcp.Manager.StartServer` 是专门为热重载 OnEnable 钩子导出的方法，内部委托给 `startServerWithRetry`：

```go
// internal/mcp/manager.go

// StartServer connects to a single MCP server with retry logic. Used by hot-reload.
func (m *Manager) StartServer(ctx context.Context, name string,
    srv config.MCPServerConfig, registry *tool.Registry) error {
    return m.startServerWithRetry(ctx, name, srv, registry)
}
```

与批量启动的 `StartServers` 相比，`StartServer` 针对单个服务器，并且：

- 使用指数退避重试（最多 5 次）：初始退避 1s，每次 ×2，上限 30s
- 成功后清除 `m.degraded[name]` 标记
- 失败后将服务器标记为 degraded，调用方（OnEnable 钩子）会将错误透传给 Feature Registry，Feature Registry 将特性标记为禁用

### StopServer —— 单服务器停止

```go
// internal/mcp/manager.go
func (m *Manager) StopServer(name string, registry *tool.Registry) {
    m.mu.Lock()
    c, ok := m.clients[name]
    if ok {
        delete(m.clients, name)
    }
    m.mu.Unlock()

    if ok {
        c.Close()  // 关闭 stdio 子进程
    }

    // 注销所有以 "mcp_<name>_" 为前缀的工具
    prefix := "mcp_" + name + "_"
    removed := registry.UnregisterByPrefix(prefix)
    slog.Info("mcp server stopped", "server", name, "tools_removed", len(removed))
}
```

停止操作分两步：
1. 关闭 MCP 客户端（终止子进程的 stdio 连接）
2. 从工具注册表中注销该服务器的所有工具（`mcp_github_list_repos`、`mcp_github_create_pr` 等）

## 启动失败处理

`Gateway.Start()` 中，每个 MCP 服务器在独立的 goroutine 中启动：

```go
// internal/gateway/gateway.go（简化）
for name, srv := range gw.cfg.Tools.MCP.Servers {
    if !featureEnabled(gw.features, "mcp_"+name) {
        continue  // 特性被禁用（持久化状态或配置文件），跳过
    }
    go func(name string, srv config.MCPServerConfig) {
        ctx := context.Background()
        if err := gw.mcpManager.StartServer(ctx, name, srv, gw.tools); err != nil {
            slog.Error("mcp server failed to start after retries",
                "server", name, "err", err)
            // 自动禁用特性，在 /feature list 中显示为 ❌
            _ = gw.features.Disable(ctx, "mcp_"+name)
        }
    }(name, srv)
}
```

启动失败后：
- 特性在 `/feature list` 中显示为 `❌ mcp_github ... (disabled at runtime)`
- `IsDegraded("github")` 返回 true
- 用户可以在修复问题后使用 `/feature enable mcp_github` 手动重试

## watchMCPDir 与 Feature Registry 的共存

系统中存在两套 MCP 管理机制，互不干扰：

| 机制 | 数据来源 | 控制粒度 | 用途 |
|------|---------|---------|------|
| Feature Registry (`mcp_*` 特性) | `configs/ironclaw.yaml` 中的 `tools.mcp.servers` | 单服务器运行时 enable/disable | 标准配置中的 MCP 服务器 |
| `watchMCPDir` goroutine | `~/.IronClaw/mcp/*.yaml` 文件变更 | 批量 `SyncServers()` | 动态外部 MCP 配置（开发/测试用途） |

`SyncServers()` 只操作 `~/.IronClaw/mcp/` 目录中的服务器，不读取 Feature Registry 状态；Feature Registry 只管理 `ironclaw.yaml` 中配置的服务器。两者通过不同的服务器名称空间隔离。

## 运行时操作示例

### 查看 MCP 特性状态

```
/feature list

📋 Features

  ✅ mcp_github           MCP server: github (npx) 🔄
  ✅ mcp_postgres         MCP server: postgres (uvx) 🔄
  ❌ mcp_slack            MCP server: slack (node) 🔄 (disabled at runtime)
  ...

🔄 = hot-reloadable (no restart needed)
```

### 停止单个 MCP 服务器

```
/feature disable mcp_github

✅ Feature "mcp_github" disabled.
```

执行后，所有 `mcp_github_*` 工具从注册表中移除，Agent 再次调用这些工具时会收到"工具不存在"的错误。

### 重新连接 MCP 服务器

```
/feature enable mcp_github

✅ Feature "mcp_github" enabled.
```

执行后，`StartServer` 触发最多 5 次重试连接，成功后重新注册所有工具。

### 持久化验证

操作后检查状态文件：

```bash
cat ~/.IronClaw/feature_state.json
```

```json
{
  "dashboard": false,
  "evolution": false,
  "mcp_github": false,
  "mcp_postgres": true,
  "memory": true,
  ...
}
```

重启后，`mcp_github` 将保持禁用状态（不会启动），直到用户再次 enable。

## 配置文件示例

```yaml
# configs/ironclaw.yaml
tools:
  mcp:
    servers:
      github:
        command: npx
        args: ["-y", "@modelcontextprotocol/server-github"]
        env:
          GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}"
        requires_approval: false
      postgres:
        command: uvx
        args: ["mcp-server-postgres", "${DATABASE_URL}"]
        requires_approval: true
```

对应自动生成的特性：
- `mcp_github`：描述 `"MCP server: github (npx)"`，默认 true，热重载
- `mcp_postgres`：描述 `"MCP server: postgres (uvx)"`，默认 true，热重载

## 完整数据流

```
ironclaw.yaml
    │
    ▼
registerFeatures(cfg)
    │  每个 servers 条目 →  Feature{Name: "mcp_<name>", Default: true, HotReloadable: true}
    │
    ▼
ResolveAndInit(ctx)
    │  AutoDetect → true，OnEnable 此时为 nil（延迟绑定）
    │  → enabled = true（因 Default=true 且无覆盖）
    │
    ▼
bindFeatureLifecycleHooks()
    │  SetOnEnable("mcp_<name>", → mcpManager.StartServer)
    │  SetOnDisable("mcp_<name>", → mcpManager.StopServer)
    │
    ▼
Gateway.Start()
    │  for each mcp_* feature that is enabled:
    │      go mcpManager.StartServer(...)  ←── 指数退避重试 (×5)
    │          ├── 成功 → 工具注册到 registry，degraded=false
    │          └── 失败 → features.Disable("mcp_<name>")，显示 ❌
    │
    ▼
运行时 /feature enable mcp_<name>
    │  Registry.Enable() → 释放写锁 → OnEnable() → mcpManager.StartServer
    │                                              → 工具重新注册
    │  → st.enabled = true，reason = "enabled at runtime"
    │  → persistFeatureState() → feature_state.json
    │
    ▼
运行时 /feature disable mcp_<name>
    │  Registry.Disable() → 检查无依赖 → 释放写锁 → OnDisable()
    │                                              → mcpManager.StopServer
    │                                              → 工具从 registry 注销
    │  → st.enabled = false，reason = "disabled at runtime"
    │  → persistFeatureState() → feature_state.json
```

## 涉及文件

| 文件 | 变更/说明 |
|------|---------|
| `internal/mcp/manager.go` | `StartServer(ctx, name, cfg, registry)` — 新增公开方法，封装 `startServerWithRetry`；`StopServer(name, registry)` — 关闭连接 + `UnregisterByPrefix` |
| `internal/gateway/features.go` | `registerFeatures()` — MCP 服务器循环注册为 `mcp_*` 特性；`bindFeatureLifecycleHooks()` — MCP 热重载钩子绑定 |
| `internal/gateway/gateway.go` | `Start()` — 独立 goroutine 启动 MCP 服务器，失败时调用 `features.Disable` |
| `internal/feature/registry.go` | `Enable`/`Disable` — 锁释放后调用钩子，防止死锁（MCP 钩子内可能调用 `IsEnabled`） |
| `internal/feature/persistence.go` | `SaveOverrides` — 每次 enable/disable 后原子写入状态文件 |
| `internal/tool/registry.go` | `UnregisterByPrefix(prefix string) []string` — StopServer 用于批量注销 MCP 工具 |

## 测试覆盖

| 测试 | 位置 | 验证内容 |
|------|------|---------|
| `TestStartServerRetry` | `internal/mcp/manager_test.go` | 前 N 次失败后成功，degraded 状态正确清除 |
| `TestStartServerExhausted` | `internal/mcp/manager_test.go` | 5 次全部失败后返回错误，degraded=true |
| `TestStopServerUnregistersTools` | `internal/mcp/manager_test.go` | StopServer 后 registry 中 `mcp_<name>_*` 工具全部消失 |
| `TestStopServerIdempotent` | `internal/mcp/manager_test.go` | 对未运行的服务器调用 StopServer 不 panic |
| `TestMCPFeatureRegistration` | `internal/gateway/features_test.go` | 每个配置的服务器生成对应 `mcp_*` 特性且默认启用 |
| `TestMCPHotReloadOnEnable` | `internal/gateway/features_test.go` | `/feature enable mcp_foo` 触发 `StartServer` 调用 |
| `TestMCPHotReloadOnDisable` | `internal/gateway/features_test.go` | `/feature disable mcp_foo` 触发 `StopServer` 调用 |
| `TestMCPFeatureDisableOnStartFailure` | `internal/gateway/gateway_test.go` | 启动失败后特性自动标记为禁用 |
| `TestMCPPersistence` | `internal/gateway/command_feature_test.go` | disable 后重启，mcp 服务器不再启动 |
| `TestEnableHookCanCallIsEnabled` | `internal/feature/registry_test.go` | MCP OnEnable 调用 `IsEnabled` 不死锁（通用死锁回归测试） |

## 后续扩展方向

| 功能 | 扩展方式 |
|------|---------|
| MCP 工具列表 API | 新增 `/api/mcp/servers` REST 端点，返回每个服务器的运行状态和已注册工具列表 |
| 运行时新增 MCP 服务器 | 结合 `watchMCPDir` 机制，将新 YAML 文件也注册为 Feature Registry 特性 |
| 连接健康检查 | 定期 ping MCP 服务器，连续失败后自动 `Disable` 并告警 |
| 工具调用审批粒度 | 将 `requires_approval` 从服务器级别下移到工具级别 |
| MCP 服务器指标 | 记录每个服务器的工具调用次数、延迟、失败率，暴露到 Web Dashboard |
