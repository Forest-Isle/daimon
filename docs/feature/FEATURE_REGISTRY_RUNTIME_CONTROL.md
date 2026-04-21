# Feature Registry 与运行时特性控制

**日期**: 2026-04-21
**范围**: `internal/feature/` + `internal/gateway/features.go` + `internal/gateway/command_feature.go` + `internal/channel/tui/suggestions.go`
**前置**: 无（本文档描述的是基础基础设施层）

## 概述

在引入 Feature Registry 之前，IronClaw 的每个子系统都通过配置文件中的独立字段来控制开关（`cfg.Dashboard.Enabled`、`cfg.Evolution.Enabled`、`cfg.Sandbox.Enabled` 等）。这种方式存在几个问题：

1. **修改摩擦大**：每次开关某个功能都需要编辑 YAML 文件并重启进程
2. **依赖关系散乱**：`knowledge_graph` 依赖 `knowledge`、`rl` 依赖 `evolution`，这些约束分散在各个 `init_*.go` 文件中，没有统一声明
3. **启动顺序脆弱**：各子系统的初始化顺序依赖手动维护，一旦出现循环依赖无法自动检测
4. **热重载不可能**：Dashboard、MCP 服务器等运行时可以安全重启的子系统，也必须整体重启才能切换

**解决方案**是引入集中式的 Feature Registry：每个功能作为一个 `Feature` 注册，声明自身的默认值、依赖、阶段和生命周期钩子；Registry 在启动时用 Kahn 拓扑排序解析依赖，运行时通过 `/feature enable|disable` 斜杠命令动态控制，状态以 JSON 持久化到 `~/.IronClaw/feature_state.json`。

## 核心数据结构

### Feature 结构体

```go
// internal/feature/feature.go
type Feature struct {
    Name          string
    Description   string
    Default       bool                              // 未配置时的默认开关状态
    Phase         Phase                             // 初始化阶段
    Dependencies  []string                          // 必须先启用的特性名
    HotReloadable bool                              // 是否支持运行时 Enable/Disable
    AutoDetect    func(ctx context.Context) DetectResult  // 可选：探测外部依赖
    OnEnable      func(ctx context.Context) error   // 启用时调用
    OnDisable     func(ctx context.Context) error   // 禁用时调用
}

// Phase 控制特性在 Gateway 启动序列中的初始化时机
const (
    PhaseConstruct  Phase = iota // Gateway.New() 阶段（对象构造期）
    PhaseStart                   // Gateway.Start() 阶段（服务启动期）
    PhaseBackground              // 启动后在后台协程中初始化
)

// DetectResult 由 AutoDetect 函数返回
type DetectResult struct {
    Available bool
    Reason    string
}
```

### FeatureInfo（只读快照）

```go
type FeatureInfo struct {
    Name          string
    Description   string
    Enabled       bool
    Reason        string   // "enabled" / "disabled by configuration" / "auto-detect: ..." 等
    Phase         Phase
    Dependencies  []string
    HotReloadable bool
}
```

## Registry 方法

| 方法 | 说明 |
|------|------|
| `Register(f Feature)` | 注册特性定义，必须在 `ResolveAndInit` 之前调用 |
| `ApplyOverrides(map[string]bool)` | 应用配置文件覆盖，可调用多次（后覆盖前） |
| `ResolveAndInit(ctx)` | 拓扑排序 + 按序调用 `resolveFeature`（含 AutoDetect、依赖检查、OnEnable） |
| `Enable(ctx, name)` | 运行时启用，释放锁后调用 OnEnable，防止死锁 |
| `Disable(ctx, name)` | 运行时禁用，先检查是否有其他已启用特性依赖自身 |
| `IsEnabled(name)` | 线程安全读取当前状态（使用 `RLock`） |
| `List()` | 返回所有特性的 `FeatureInfo` 切片（按拓扑初始化顺序） |
| `EnabledNames()` | 返回所有已启用特性名称列表 |
| `SetOnEnable(name, fn)` | 替换已注册特性的 OnEnable 钩子（用于延迟绑定） |
| `SetOnDisable(name, fn)` | 替换已注册特性的 OnDisable 钩子 |
| `RuntimeOverrides()` | 返回所有特性当前状态的 `map[string]bool`（用于持久化） |

## 三级特性分层

当前注册的 19 个特性按默认状态分为三层：

### Tier 1 — 默认开启，无外部依赖

| 特性名 | 描述 | Phase | HotReloadable |
|--------|------|-------|---------------|
| `memory` | 文件存储 + 事实提取记忆系统 | Construct | ❌ |
| `skills` | SKILL.md 加载和 read_skill 工具 | Construct | ❌ |
| `multi_agent` | 子代理派生与编排 | Construct | ❌ |
| `team` | `/team` 命令的团队协调器（依赖 `multi_agent`） | Construct | ❌ |
| `speculative` | 流式响应期间的只读工具预执行 | Construct | ❌ |
| `scheduler` | 定时任务执行 | Start | ✅ |

### Tier 2 — AutoDetect 驱动

| 特性名 | 描述 | AutoDetect 逻辑 | 依赖 |
|--------|------|-----------------|------|
| `knowledge` | 文档摄取和混合检索 | 始终可用（BM25 降级） | `memory` |
| `knowledge_graph` | 实体/关系提取和图遍历 | 无 | `knowledge` |
| `reranker` | LLM 搜索结果重排 | 无 | `knowledge` |
| `sandbox` | Docker 容器隔离执行 bash | `sandbox.ProbeDocker(ctx)` | 无 |

### Tier 3 — 手动启用（默认关闭）

| 特性名 | 描述 | Phase | HotReloadable |
|--------|------|-------|---------------|
| `evolution` | 自进化引擎（偏好学习 + 技能合成） | Start | ✅ |
| `rl` | 强化学习系统（依赖 `evolution`） | Construct | ❌ |
| `model_routing` | 按任务复杂度动态选模型（依赖 `evolution`） | Construct | ❌ |
| `dashboard` | Web Dashboard 实时监控 | Construct | ✅ |
| `server` | 独立 HTTP 管理服务器 | Construct | ❌ |

另外每个配置的 MCP 服务器自动生成一个 `mcp_<name>` 特性（默认开启，热重载，详见 [MCP_FEATURE_HOT_RELOAD.md](MCP_FEATURE_HOT_RELOAD.md)）。

## 初始化流程

### Gateway 启动序列中的特性注册

```
Gateway.New()
  │
  ├─ registerFeatures(cfg)        // 注册全部 Feature 定义
  │     └─ r.Register(...)        // 逐个注册，此时 enabled=false, reason="not initialized"
  │
  ├─ r.ApplyOverrides(configToOverrides(cfg))   // 应用 YAML 配置覆盖
  │     └─ 对每个 cfg.XXX.Enabled 设置 st.override
  │
  ├─ LoadOverrides(featureStatePath)            // 加载持久化的运行时状态
  │     └─ 若文件存在，再次 r.ApplyOverrides(persisted)  // 持久化状态优先级最高
  │
  └─ r.ResolveAndInit(ctx)        // Kahn 拓扑排序 → 按序初始化
        ├─ 检查 AutoDetect
        ├─ 检查 override / Default
        ├─ 检查依赖是否已启用
        └─ 调用 OnEnable（若有）
```

### 优先级顺序

```
持久化状态 (~/.IronClaw/feature_state.json)
    > 配置文件覆盖 (configs/ironclaw.yaml 中的 .Enabled 字段)
    > Feature.Default
```

### Kahn 拓扑排序

`ResolveAndInit` 内部调用 `topoSort()`，用 Kahn 算法对所有注册特性进行拓扑排序：

```go
// internal/feature/registry.go
func (r *Registry) topoSort() ([]string, error) {
    inDegree := make(map[string]int, len(r.states))
    dependents := make(map[string][]string, len(r.states))

    for name, st := range r.states {
        for _, dep := range st.feature.Dependencies {
            if _, ok := r.states[dep]; !ok {
                return nil, fmt.Errorf("feature %q has unknown dependency %q", name, dep)
            }
            dependents[dep] = append(dependents[dep], name)
            inDegree[name]++
        }
    }
    // BFS 队列处理入度为 0 的节点...
    if len(sorted) != len(r.states) {
        return nil, fmt.Errorf("circular dependency detected among features")
    }
    return sorted, nil
}
```

循环依赖在启动时立即报错，而不是静默失败。

## 特性状态持久化

### 文件格式与路径

```go
// internal/feature/persistence.go
func DefaultStatePath(baseDir string) string {
    return filepath.Join(baseDir, "feature_state.json")
}
// 默认路径: ~/.IronClaw/feature_state.json
```

文件内容为扁平 JSON，键为特性名，值为当前启用状态：

```json
{
  "dashboard": true,
  "evolution": false,
  "mcp_github": true,
  "sandbox": false
}
```

### 原子写入

`SaveOverrides` 使用先写临时文件、再原子 rename 的策略，避免写入过程中进程崩溃导致文件损坏：

```go
func SaveOverrides(path string, overrides map[string]bool) error {
    os.MkdirAll(filepath.Dir(path), 0o750)
    data, _ := json.MarshalIndent(overrides, "", "  ")
    tmp := path + ".tmp"
    os.WriteFile(tmp, data, 0o640)
    os.Rename(tmp, path)  // 原子操作
    return nil
}
```

### 加载顺序

在 `Gateway.New()` 中，配置文件覆盖和持久化状态按顺序叠加应用：

```go
// 第一层：YAML 配置覆盖
r.ApplyOverrides(configToOverrides(cfg))

// 第二层：持久化运行时状态（更高优先级）
if persisted, err := feature.LoadOverrides(gw.featureStatePath); err == nil {
    r.ApplyOverrides(persisted)
}

r.ResolveAndInit(ctx)
```

### 保存时机

每次 `/feature enable|disable` 命令执行成功后，`persistFeatureState()` 立即将当前所有特性状态写回文件：

```go
// internal/gateway/command_feature.go
func (gw *Gateway) persistFeatureState() {
    overrides := gw.features.RuntimeOverrides()
    feature.SaveOverrides(gw.featureStatePath, overrides)
}
```

## 热重载生命周期钩子

### 设计动机

`bindFeatureLifecycleHooks()` 在 Gateway 所有子系统初始化完成**之后**才调用，原因是：

- `OnEnable` 钩子需要引用具体对象（如 `gw.evoEngine`、`gw.sched`），这些对象在 Feature 注册时还不存在
- 使用 `SetOnEnable`/`SetOnDisable` 延迟绑定，而不是在注册时传入，避免了 `nil` 引用问题

```go
// internal/gateway/features.go
func (gw *Gateway) bindFeatureLifecycleHooks() {
    _ = gw.features.SetOnEnable("dashboard", func(ctx context.Context) error {
        return gw.startDashboard()
    })
    _ = gw.features.SetOnDisable("dashboard", func(ctx context.Context) error {
        return gw.stopDashboard()
    })

    _ = gw.features.SetOnEnable("scheduler", func(ctx context.Context) error {
        if gw.sched != nil {
            gw.sched.Start(ctx)
        }
        return nil
    })
    // ... evolution、mcp_* 等
}
```

### 死锁预防

`Enable()` 和 `Disable()` 在调用 OnEnable/OnDisable 钩子**之前**释放写锁，调用完成后重新获取写锁更新状态。这是关键设计，因为钩子函数（如 `startDashboard`）内部可能调用 `IsEnabled()`（使用读锁），形成锁递归死锁：

```go
// internal/feature/registry.go — Enable() 实现
func (r *Registry) Enable(ctx context.Context, name string) error {
    r.mu.Lock()

    // ...依赖检查...

    onEnable := st.feature.OnEnable
    r.mu.Unlock()  // ★ 释放锁，防止钩子回调 IsEnabled() 时死锁

    if onEnable != nil {
        if err := onEnable(ctx); err != nil {
            return fmt.Errorf("OnEnable for %q failed: %w", name, err)
        }
    }

    r.mu.Lock()
    st.enabled = true
    st.reason = "enabled at runtime"
    r.mu.Unlock()
    return nil
}
```

### 非热重载特性的警告

当用户对 `HotReloadable: false` 的特性执行 enable/disable 时，命令依然执行（状态被记录），但会显示重启提示：

```
✅ Feature "multi_agent" enabled.
⚠️ Not hot-reloadable. Restart IronClaw for full effect.
```

## 斜杠命令

### 命令列表

| 命令 | 说明 |
|------|------|
| `/feature list` | 显示所有特性的当前状态表格 |
| `/feature enable <name>` | 启用特性，调用 OnEnable 钩子，持久化状态 |
| `/feature disable <name>` | 禁用特性（先检查依赖），调用 OnDisable，持久化状态 |
| `/model <model-name>` | 运行时切换 LLM 模型，同步到 `Runtime` 和 `CognitiveAgent` |
| `/config show` | 显示当前有效配置（provider、model、agent mode、已启用特性数） |
| `/compact` | 手动触发当前会话的上下文压缩 |

### `/feature list` 输出示例

```
📋 Features

  ✅ memory               Memory system with file storage and fact extraction
  ✅ skills               SKILL.md loading and read_skill tool
  ✅ multi_agent          Sub-agent spawning and orchestration
  ✅ team                 Team coordinator for /team command
  ✅ speculative          Read-only tool pre-execution during streaming
  ✅ scheduler            Scheduled task execution 🔄
  ✅ knowledge            Document ingestion and hybrid retrieval
  ✅ knowledge_graph      Entity/relation extraction and graph traversal
  ✅ reranker             LLM-based search result reranking
  ❌ sandbox              Docker container isolation for bash execution (auto-detect: Docker not available)
  ❌ evolution            Self-evolution engine (preference learning, skill synthesis)
  ❌ dashboard            Web dashboard for real-time agent monitoring
  ✅ mcp_github           MCP server: github (npx) 🔄

🔄 = hot-reloadable (no restart needed)
Use /feature enable <name> or /feature disable <name>
```

### `/model` 命令实现

```go
// internal/gateway/command_feature.go
func (gw *Gateway) handleModelCommand(ctx context.Context, ch channel.Channel,
    msg channel.InboundMessage, args string) {

    old := gw.cfg.LLM.Model
    gw.cfg.LLM.Model = args
    gw.runtime.SetModel(args)
    if gw.cognitiveAgent != nil {
        gw.cognitiveAgent.SetModel(args)
    }
    gw.sendReply(ctx, ch, msg, fmt.Sprintf("✅ Model switched: %s → %s", old, args))
}
```

## TUI 参数自动补全

### ArgCompleter 接口

```go
// internal/channel/tui/suggestions.go
type ArgCompleter func(cmd, subCmd, argSoFar string) []string
// cmd      — 命令名（不含 /，如 "feature"）
// subCmd   — 第一个子参数（如 "enable" 或 "disable"）
// argSoFar — 用户正在输入的参数前缀
```

### Gateway 侧构建

`BuildArgCompleter()` 在 `command_feature.go` 中实现，动态查询 Registry 状态返回候选列表：

```go
// internal/gateway/command_feature.go
func (gw *Gateway) BuildArgCompleter() func(cmd, subCmd, argSoFar string) []string {
    return func(cmd, subCmd, argSoFar string) []string {
        switch cmd {
        case "feature":
            switch subCmd {
            case "enable":
                // 仅返回当前已禁用的特性名
                var names []string
                for _, f := range gw.features.List() {
                    if !f.Enabled {
                        names = append(names, f.Name)
                    }
                }
                return names
            case "disable":
                // 仅返回当前已启用的特性名
                var names []string
                for _, f := range gw.features.List() {
                    if f.Enabled {
                        names = append(names, f.Name)
                    }
                }
                return names
            }
        }
        return nil
    }
}
```

### TUI 侧接入

在 `cmd/ironclaw/tui.go` 中，TUI adapter 创建后立即注入 completer：

```go
// cmd/ironclaw/tui.go
tuiAdapter := tuichannel.New(cfg.Agent.Mode, version)
tuiAdapter.SetArgCompleter(gw.BuildArgCompleter())  // ★ 接入动态补全
gw.AddChannel(tuiAdapter)
```

### 补全触发逻辑

`GenerateSuggestions()` 在用户输入带空格的斜杠命令时触发参数补全（相对于命令名补全）：

```
输入 "/feature "        → 静态补全 SubArgs: ["list", "enable", "disable"]
输入 "/feature enable " → ArgCompleter("feature", "enable", "") → 所有已禁用特性名
输入 "/feature en"      → ArgCompleter("feature", "", "en")     → 静态 SubArgs 前缀过滤
```

`ApplySuggestion()` 将用户选中的候选项替换到输入框并在末尾追加空格：

```go
// 参数补全 — 替换最后一个 token 并追加空格
parts[len(parts)-1] = suggestion.ArgValue
return strings.Join(parts, " ") + " "
```

## 添加新特性（How-to）

### 第一步：在 `registerFeatures()` 中注册

```go
// internal/gateway/features.go
r.Register(feature.Feature{
    Name:          "my_feature",
    Description:   "What my_feature does",
    Default:       true,                     // 默认开启
    Phase:         feature.PhaseConstruct,
    Dependencies:  []string{"memory"},       // 若有依赖
    HotReloadable: true,                     // 若支持热重载
    AutoDetect: func(ctx context.Context) feature.DetectResult {
        // 可选：探测外部依赖是否可用
        return feature.DetectResult{Available: true}
    },
})
```

### 第二步（仅热重载特性）：在 `bindFeatureLifecycleHooks()` 中绑定钩子

```go
_ = gw.features.SetOnEnable("my_feature", func(ctx context.Context) error {
    if gw.mySubsystem != nil {
        return gw.mySubsystem.Start(ctx)
    }
    return nil
})
_ = gw.features.SetOnDisable("my_feature", func(ctx context.Context) error {
    if gw.mySubsystem != nil {
        gw.mySubsystem.Stop()
    }
    return nil
})
```

### 第三步（可选）：在初始化代码中用 `featureEnabled` 门控

```go
// internal/gateway/init_xxx.go
func (gw *Gateway) initMyFeature(ctx context.Context) error {
    if !featureEnabled(gw.features, "my_feature") {
        return nil
    }
    // 初始化逻辑...
}
```

**无需**修改：配置结构体、YAML 解析代码、`configToOverrides()` 函数（除非需要兼容旧 YAML 字段）。

## 涉及文件

### 核心实现文件

| 文件 | 说明 |
|------|------|
| `internal/feature/feature.go` | `Feature` 结构体、`Phase` 枚举、`DetectResult`、`FeatureInfo` 定义 |
| `internal/feature/registry.go` | `Registry` 实现：`Register`、`ApplyOverrides`、`ResolveAndInit`（Kahn 排序）、`Enable`/`Disable`（锁释放后调用钩子）、`List`、`SetOnEnable`/`SetOnDisable` |
| `internal/feature/persistence.go` | `LoadOverrides`、`SaveOverrides`（原子写）、`DefaultStatePath`、`RuntimeOverrides` |
| `internal/gateway/features.go` | `registerFeatures(cfg)`（注册全部 19+ 特性）、`bindFeatureLifecycleHooks()`、`configToOverrides(cfg)` |
| `internal/gateway/command_feature.go` | `/feature`、`/model`、`/config`、`/compact` 命令处理；`BuildArgCompleter()`；`persistFeatureState()` |
| `internal/channel/tui/suggestions.go` | `ArgCompleter` 类型定义；`GenerateSuggestions()`；`ApplySuggestion()` |
| `cmd/ironclaw/tui.go` | `tuiAdapter.SetArgCompleter(gw.BuildArgCompleter())` 接入点 |

### 关联文件

| 文件 | 关联方式 |
|------|---------|
| `internal/gateway/gateway.go` | 持有 `features *feature.Registry` 和 `featureStatePath string` 字段 |
| `internal/gateway/init_*.go` | 各子系统初始化时调用 `featureEnabled(gw.features, "xxx")` 门控 |
| `internal/mcp/manager.go` | `StartServer` / `StopServer` 被 `mcp_*` 特性的 OnEnable/OnDisable 钩子调用 |
| `internal/sandbox/docker_probe.go` | `sandbox` 特性的 AutoDetect 调用 `ProbeDocker(ctx)` |

## 测试覆盖

| 测试 | 位置 | 验证内容 |
|------|------|---------|
| `TestEnableHookCanCallIsEnabled` | `internal/feature/registry_test.go` | OnEnable 钩子内调用 `IsEnabled()` 不产生死锁 |
| `TestDisableHookCanCallIsEnabled` | `internal/feature/registry_test.go` | OnDisable 钩子内调用 `IsEnabled()` 不产生死锁 |
| `TestCircularDependencyDetected` | `internal/feature/registry_test.go` | 循环依赖在 `ResolveAndInit` 时返回错误 |
| `TestDependencyOrder` | `internal/feature/registry_test.go` | 依赖特性在被依赖特性之前初始化 |
| `TestApplyOverrides` | `internal/feature/registry_test.go` | 配置覆盖正确覆盖 Default 值 |
| `TestAutoDetectDisables` | `internal/feature/registry_test.go` | AutoDetect 返回 false 时特性被禁用 |
| `TestSaveAndLoadOverrides` | `internal/feature/persistence_test.go` | 写入后读取内容一致 |
| `TestAtomicWrite` | `internal/feature/persistence_test.go` | tmp 文件在 rename 前存在，rename 后消失 |
| `TestLoadOverridesMissingFile` | `internal/feature/persistence_test.go` | 文件不存在时返回空 map 而非错误 |
| `TestRuntimeOverrides` | `internal/feature/persistence_test.go` | `RuntimeOverrides()` 返回所有特性的当前状态 |
| `TestBuildArgCompleterEnable` | `internal/gateway/command_feature_test.go` | 只返回已禁用的特性名 |
| `TestBuildArgCompleterDisable` | `internal/gateway/command_feature_test.go` | 只返回已启用的特性名 |
| `TestHotReloadWarning` | `internal/gateway/command_feature_test.go` | 非热重载特性触发 enable/disable 时返回 ⚠️ 提示 |
| `TestGenerateSuggestions_ArgCompletion` | `internal/channel/tui/suggestions_test.go` | 参数补全返回正确候选列表并过滤前缀 |
| `TestApplySuggestion_ArgValue` | `internal/channel/tui/suggestions_test.go` | 参数选中后替换末尾 token 并追加空格 |

## 后续扩展方向

| 功能 | 扩展方式 |
|------|---------|
| 特性依赖图可视化 | 利用 `FeatureInfo.Dependencies` 生成 DOT 图，通过 Dashboard REST API 暴露 |
| Web Dashboard 特性控制面板 | 新增 `/api/features` GET/POST 端点，在前端展示特性开关 UI |
| 特性变更事件总线 | Feature Registry 在 Enable/Disable 后发布事件到 `dashboard.EventBus`，前端实时感知状态变化 |
| 每特性独立健康检查 | 扩展 `FeatureInfo` 增加 `Health` 字段，AutoDetect 升级为定期轮询 |
| 特性灰度发布 | 增加 `RolloutPercent int` 字段，基于 session hash 决定是否启用 |
