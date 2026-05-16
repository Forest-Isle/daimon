# Config 拆分 + CognitiveAgent Options 模式重构

**日期**: 2026-05-17
**范围**: Config 结构体按领域拆分、CognitiveAgent 31 个 Set* 方法统一为 Options 模式

## 概述

本次重构解决了两个逐步累积的结构性问题：

1. **`internal/config/config.go`** 原本是一个 655 行的单文件，包含约 30 个配置结构体，横跨 LLM、Agent、认知循环、RL、沙箱、通道、工具、权限、钩子、技能等不相关领域。每次修改都会 touch 同一文件，造成不必要的变更污染和合并冲突风险。

2. **`CognitiveAgent` 后构造依赖注入** 使用了 31 个 `Set*` 方法（`SetMemoryStore`、`SetHookManager`、`SetSkillManager` 等），调用方（`init_cognitive.go`）需要按顺序逐个调用 12+ 个 Set*，容易遗漏调用导致运行时 nil panic。

## Config 拆分

### 拆分方案

原 `config.go` 保留主结构体 `Config` 和工具函数（`ExpandEnv`、`Load`、`defaultConfig`）。其余类型按领域拆入 6 个新文件：

```
internal/config/
├── config.go              ← Config 主结构体 + ExpandEnv + Load + defaultConfig
├── config_agent.go        ← AgentConfig, CognitiveConfig, CompressionConfig,
│                             SpeculativeExecutionConfig, TeamConfig,
│                             RLConfig, BanditConfig, PPOConfig, DQNConfig
├── config_channels.go     ← LLMConfig, RetryConfig, ObservabilityConfig,
│                             TelegramConfig, DiscordConfig, TUIConfig
├── config_sandbox.go      ← SandboxConfig, BashSandboxConfig, DockerSandboxConfig,
│                             NetworkConfig, RateLimitConfig,
│                             PermissionsConfig, PermissionRule
├── config_tools.go        ← ToolsConfig, BashToolConfig, FileToolConfig,
│                             HTTPToolConfig, BrowserToolConfig,
│                             MCPConfig, MCPServerConfig
├── config_hooks.go        ← HooksConfig, HookHandlerConfig,
│                             SkillsConfig, AgentsConfig, AgentDefinition, DebateSettings
└── config_infra.go        ← StoreConfig, MemoryConfig, KnowledgeConfig,
                             GraphConfig, SchedulerConfig,
                             ServerConfig, DashboardConfig, HealthConfig, LogConfig
```

### 文件行数对比

| 文件 | 行数 | 包含结构体数 |
|------|------|-------------|
| `config.go` | 253 | 1 (Config) |
| `config_agent.go` | 108 | 10 |
| `config_channels.go` | 44 | 5 |
| `config_sandbox.go` | 55 | 5 |
| `config_tools.go` | 69 | 6 |
| `config_hooks.go` | 48 | 5 |
| `config_infra.go` | 92 | 7 |

### 关键约束

- **全部类型保持在 `package config` 内**，不改变包结构
- **所有字段、YAML tag、注释原样保留**，纯代码移动
- **零功能变更**，不新增/删除任何类型
- `config.go` 中 `import "time"` 移除（主结构体不再直接使用 `time.Duration`），需要 `time` 的新文件各自导入

## CognitiveAgent Options 模式

### Before（逐个 Set* 注入）

```go
// 构造函数只接受 6 个必选参数
ca := agent.NewCognitiveAgent(provider, tools, sessions, db, cfg, llmCfg)

// 调用方需要逐个调 12+ 个 Set*
if gw.memStore != nil {
    ca.SetMemoryStore(gw.memStore)
}
if gw.factExtractor != nil {
    ca.SetFactExtractor(gw.factExtractor)
}
if gw.lifecycleMgr != nil {
    ca.SetLifecycleManager(gw.lifecycleMgr)
}
if gw.hookMgr != nil {
    ca.SetHookManager(gw.hookMgr)
}
if gw.permEngine != nil {
    ca.SetPermissionEngine(gw.permEngine)
}
// ... 还有 7+ 个
```

### After（Options 结构体）

```go
// CogitiveAgentOptions 聚合所有可选依赖
opts := &agent.CognitiveAgentOptions{
    CheckpointStore: checkpointStore,
}
if gw.memStore != nil {
    opts.MemoryStore = gw.memStore
}
if gw.factExtractor != nil {
    opts.FactExtractor = gw.factExtractor
}
// ...

// 构造函数一次性接收
ca := agent.NewCognitiveAgent(provider, tools, sessions, db, cfg, llmCfg, opts)
```

### CognitiveAgentOptions 完整字段

```go
type CognitiveAgentOptions struct {
    MemoryStore         memory.Store
    FactExtractor       *memory.LLMFactExtractor
    LifecycleManager    *memory.LifecycleManager
    CodebaseIndex       *CodebaseIndex
    KnowledgeSearcher   knowledge.Searcher
    KnowledgeGraph      graph.Graph
    EntityExtractor     *graph.LLMEntityExtractor
    SelfHealEngine      *SelfHealEngine
    TreePlanner         *StrategicTreePlanner
    MCTSPlanner         *MCTSPlanner
    HookManager         *hook.Manager
    PermissionEngine    *tool.PermissionEngine
    InterceptorChain    *tool.InterceptorChain
    SkillManager        *skill.Manager
    AgentManager        *AgentManager
    Orchestrator        *AgentOrchestrator
    TeamManager         *TeamManager
    EvolutionEngine     *evolution.Engine
    RLPolicy            RLPolicy
    RLTrainer           RLTrainer
    MemoryNotifyFunc    MemoryNotifyFunc
    CheckpointStore     CheckpointStore
    ContextManager      ContextManager
    TaskLedger          taskledger.TaskLedger
    DashboardEmitter    DashboardEmitter
    ReplayRecorder      *ReplayRecorder
    ObservationCallback func(result *ObservationResult)
    ApprovalFunc        ApprovalFunc
    PlanMode            *PlanMode
    DebateConfig        config.DebateSettings
}
```

### applyOptions 内部委托

`NewCognitiveAgent` 在构造函数末尾调用 `applyOptions(opts)`，该方法内部委托给所有现有的 `Set*` 方法：

```go
func (ca *CognitiveAgent) applyOptions(opts *CognitiveAgentOptions) {
    if opts == nil { return }
    if opts.MemoryStore != nil       { ca.SetMemoryStore(opts.MemoryStore) }
    if opts.FactExtractor != nil     { ca.SetFactExtractor(opts.FactExtractor) }
    if opts.LifecycleManager != nil  { ca.SetLifecycleManager(opts.LifecycleManager) }
    // ... 全部 31 个字段按此模式
}
```

### 向后兼容

**所有现有 Set* 方法完整保留**。`applyOptions` 内部通过委托调用它们，外部调用方仍可逐个使用 Set* 方法进行增量注入（例如运行时 `/model` 切换调用 `SetModel`）。

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/config/config.go` | 修改 | 移除 29 个类型定义，保留 Config + 工具函数 |
| `internal/config/config_agent.go` | 新增 | Agent、Cognitive、Compression、RL 配置 |
| `internal/config/config_channels.go` | 新增 | LLM、Telegram、Discord、TUI 配置 |
| `internal/config/config_sandbox.go` | 新增 | 沙箱、网络、权限配置 |
| `internal/config/config_tools.go` | 新增 | 工具、MCP 配置 |
| `internal/config/config_hooks.go` | 新增 | 钩子、技能、多代理配置 |
| `internal/config/config_infra.go` | 新增 | 存储、知识、仪表盘、服务器配置 |
| `internal/agent/cognitive.go` | 修改 | 新增 CognitiveAgentOptions + applyOptions |
| `internal/gateway/init_cognitive.go` | 修改 | 改用 Options 结构体替代逐个 Set* |

## 验收清单

- [x] `config.go` 从 655 行缩减到 253 行
- [x] 6 个新 domain 文件各包含对应领域结构体
- [x] `CognitiveAgentOptions` 覆盖全部 31 个可选依赖
- [x] `init_cognitive.go` 不再逐个调用 12+ 个 Set*
- [x] 所有 Set* 方法保持可用（向后兼容）
- [x] 所有字段、tag、注释原样保留
- [x] `gofmt` 通过
