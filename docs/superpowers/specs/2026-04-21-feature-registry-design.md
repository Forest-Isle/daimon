# Feature Registry — 功能注册表与运行时控制

## Overview

将 IronClaw 从"配置文件是功能入口"转为"配置文件是默认值覆盖层"。引入 Feature Registry 作为功能生命周期的单一管控点，通过斜杠命令实现运行时控制，减少新功能的集成成本（从改 4 处降到注册 1 处）。

## 问题

当前每个功能的启停散布在 `config.Config` struct、`ironclaw.example.yaml`、`internal/gateway/init_*.go` 的 `if cfg.XXX.Enabled` 判断中。新增一个功能需要修改至少 4 个位置。运行时无法开关功能（除 `/mode`），所有变更需要重启进程。

## Architecture

### 新增包：`internal/feature/`

```
internal/feature/
├── feature.go      // Feature struct、Phase enum、DetectResult
├── registry.go     // Registry：注册、依赖解析、生命周期管理
└── registry_test.go
```

### Feature struct

```go
type Phase int

const (
    PhaseConstruct  Phase = iota // Gateway.New 阶段初始化
    PhaseStart                   // Gateway.Start 阶段启动
    PhaseBackground              // 后台 goroutine（MCP hot-reload、ticker 等）
)

type DetectResult struct {
    Available bool
    Reason    string
}

type Feature struct {
    Name         string
    Description  string
    Default      bool
    Phase        Phase
    Dependencies []string
    AutoDetect   func(ctx context.Context) DetectResult
    OnEnable     func(ctx context.Context) error
    OnDisable    func(ctx context.Context) error
}
```

### Registry

```go
type featureState struct {
    Feature
    enabled    bool
    reason     string // "config override", "auto-detect: Docker not found", etc.
    initDone   bool
}

type Registry struct {
    mu       sync.RWMutex
    features map[string]*featureState
    order    []string // 拓扑排序后的初始化顺序
}
```

**核心方法**：

| 方法 | 作用 |
|------|------|
| `Register(f Feature)` | 注册功能定义 |
| `ApplyOverrides(overrides map[string]bool)` | 应用配置文件覆盖 |
| `ResolveAndInit(ctx)` | 拓扑排序 → AutoDetect → OnEnable |
| `Enable(ctx, name) error` | 运行时启用（检查依赖后调用 OnEnable） |
| `Disable(ctx, name) error` | 运行时禁用（检查反向依赖后调用 OnDisable） |
| `IsEnabled(name) bool` | 查询功能状态（替代 `cfg.XXX.Enabled`） |
| `List() []FeatureInfo` | 返回所有功能状态（给 `/feature list`） |
| `EnabledNames() []string` | 返回所有启用功能名称 |

### 依赖解析

使用 Kahn 算法进行拓扑排序。`Enable` 时自动启用未启用的依赖；`Disable` 时拒绝关闭被其他已启用功能依赖的功能（返回错误说明依赖链）。

### AutoDetect

`ResolveAndInit` 阶段按拓扑顺序执行 AutoDetect：
- 返回 `Available=true` → 依据 Default 或 configOverride 决定启用
- 返回 `Available=false` → 强制禁用，reason 记录原因

运行时 `Enable` 也会先执行 AutoDetect，不可用则返回错误。

### 与 Gateway 的集成

**Gateway struct** 新增 `features *feature.Registry` 字段。

**`Gateway.New` 改造**：

```
1. initDatabase()          — 不变（始终执行）
2. features.Register(...)  — 注册所有功能
3. features.ApplyOverrides(configToOverrides(cfg))
4. features.ResolveAndInit(ctx)  — 拓扑排序 + AutoDetect + OnEnable
5. 现有 init_*.go 函数保持，但内部用 features.IsEnabled() 替代 cfg.XXX.Enabled
```

### 配置文件映射

`configToOverrides(cfg) map[string]bool` 从 Config struct 中提取所有 `Enabled` 字段，映射为 `featureName → bool`：

| Config 字段 | Feature Name |
|-------------|-------------|
| `Memory.Enabled` | `memory` |
| `Knowledge.Enabled` | `knowledge` |
| `Knowledge.GraphEnabled` | `knowledge_graph` |
| `Evolution.Enabled` | `evolution` |
| `Agent.RL.Enabled` | `rl` |
| `Agents.Enabled` | `multi_agent` |
| `Agent.Team.Enabled` | `team` |
| `Agent.SpeculativeExecution.Enabled` | `speculative` |
| `Skills.Enabled` | `skills` |
| `Dashboard.Enabled` | `dashboard` |
| `Sandbox.Enabled` | `sandbox` |
| `Server.Enabled` | `server` |
| `Evolution.Router.Enabled` | `model_routing` |

## 斜杠命令

### `/feature` 命令族

处理位置：`Gateway.handleInbound`，与 `/mode`、`/tasks` 同级。

```
/feature                   → 等同 /feature list
/feature list              → 列出所有功能及状态
/feature enable <name>     → 运行时启用
/feature disable <name>    → 运行时禁用
```

**输出格式（/feature list）**：

```
📋 Feature Status:

  ✅ memory          Memory system with file storage
  ✅ skills          SKILL.md loading and read_skill tool
  ✅ multi_agent     Sub-agent spawning and orchestration
  ✅ team            Team coordinator for /team command
  ✅ speculative     Read-only tool pre-execution during streaming
  ❌ knowledge       Document ingestion + hybrid retrieval (no OpenAI key)
  ❌ evolution       Self-evolution engine (disabled by config)
  ❌ dashboard       Web dashboard UI (disabled by config)
  ⚠️  sandbox         Docker sandbox (Docker not available)

Use /feature enable <name> or /feature disable <name>
```

### `/config show`

显示当前生效配置的关键信息：模型、模式、token 限制、已启用功能数等。

### `/compact`

手动触发 `ContextManager.Compress()`（当前只能被动触发）。

### `/model [name]`

查询或切换当前 LLM 模型。切换时更新 provider 的 model 参数。

### TUI 注册

在 `commandRegistry` 中注册所有新命令，提供 Description 和 ArgHint 支持自动补全。所有命令都不是 local command，全部转发到 gateway。

## 默认值调整

### Tier 1 — 默认开启（无外部依赖）

| Feature | 当前默认 | 新默认 | AutoDetect |
|---------|---------|--------|-----------|
| `memory` | `false` | `true` | 无（始终可用，embedding 降级为 noop） |
| `skills` | `true` | `true` | 无 |
| `multi_agent` | `false` | `true` | 无 |
| `team` | `false` | `true` | 无（无 SubAgentMgr 时退化为单次 LLM） |
| `speculative` | `false` | `true` | 无 |
| `scheduler` | `false` | `true` | 无 |

### Tier 2 — AutoDetect 驱动

| Feature | 当前默认 | 新默认 | AutoDetect |
|---------|---------|--------|-----------|
| `knowledge` | `false` | `true` | OpenAI key 存在 → full mode；不存在 → BM25-only |
| `knowledge_graph` | `false` | `true` | 跟随 knowledge |
| `sandbox` | `false` | `true` | `ProbeDocker()` → Docker 可用则启用 |
| `reranker` | `false` | `true` | completer 可用则启用 |

### Tier 3 — 保持 opt-in

| Feature | 默认 | 原因 |
|---------|------|------|
| `evolution` | `false` | 额外 LLM 调用成本 |
| `rl` | `false` | 重计算，实验性质 |
| `model_routing` | `false` | 需额外模型配置 |
| `dashboard` | `false` | 开网络端口，安全考量 |
| `server` | `false` | 同上 |

## 依赖图

```
memory ─────────────────┐
                        ├─→ knowledge ──→ knowledge_graph
                        │                    │
skills (独立)           │                    └─→ reranker
                        │
multi_agent ────────────┼─→ team
                        │
speculative (独立)      │
scheduler (独立)        │
                        │
evolution ──────────────┼─→ rl
                        │   └─→ model_routing
                        │
sandbox (独立)          │
dashboard (独立)        │
server (独立)
```

## 错误处理

- `OnEnable` 失败 → 功能标记为 disabled，reason 记录错误，`ResolveAndInit` 继续初始化其他功能（不阻塞启动）
- `OnDisable` 失败 → log.Error，功能标记为 disabled（best effort）
- 依赖循环 → `ResolveAndInit` 返回 error（启动失败）
- 运行时 `Enable` 依赖不可用 → 返回错误消息说明哪些依赖需先启用

## 向后兼容

- 配置文件的 `enabled` 字段继续生效，作为 override
- 未配置 `enabled` 的功能使用 Feature.Default
- 现有 `if cfg.XXX.Enabled` 逐步替换为 `features.IsEnabled()`，在同一个 init 函数内完成，不改变初始化顺序
- 不改变 Config struct（保留所有 Enabled 字段），只改变消费方式

## Files Changed

| File | Change |
|------|--------|
| `internal/feature/feature.go` | 新增：Feature struct, Phase enum, DetectResult, FeatureInfo |
| `internal/feature/registry.go` | 新增：Registry 实现（注册、拓扑排序、生命周期管理） |
| `internal/feature/registry_test.go` | 新增：单元测试 |
| `internal/gateway/gateway.go` | 新增 `features` 字段；`handleInbound` 增加 `/feature`、`/config`、`/compact`、`/model` 拦截 |
| `internal/gateway/features.go` | 新增：`registerFeatures()` 注册所有功能定义；`configToOverrides()` |
| `internal/gateway/command_feature.go` | 新增：`handleFeatureCommand()`、`handleConfigCommand()`、`handleCompactCommand()`、`handleModelCommand()` |
| `internal/channel/tui/commands.go` | 注册新命令到 `commandRegistry` |
| `internal/gateway/init_*.go` | 逐步将 `cfg.XXX.Enabled` 替换为 `gw.features.IsEnabled()` |
| `internal/config/config.go` | `defaultConfig()` 中调整默认值（Tier 1 全部 true） |
