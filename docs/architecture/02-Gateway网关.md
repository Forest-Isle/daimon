# 02 - Gateway 网关（中央编排器）

## 文件

```
internal/gateway/gateway.go
```

## 核心职责

Gateway 是 IronClaw 的**中央编排器**，负责：
1. 初始化并接线所有模块
2. 路由消息：Channel → Agent
3. 管理组件生命周期（启动/关闭）
4. 处理工具审批回调

## 数据结构

```go
type Gateway struct {
    cfg            *config.Config
    db             *store.DB            // SQLite 数据库
    sessions       *session.Manager     // 会话管理
    runtime        *agent.Runtime       // Simple 模式运行时
    cognitiveAgent *agent.CognitiveAgent // Cognitive 模式代理
    tools          *tool.Registry       // 工具注册表
    channels       map[string]channel.Channel // 通道适配器
    sched          *scheduler.Scheduler // 定时任务
    mcpManager     *mcp.Manager         // MCP 服务器管理
    rlTrainer      *rl.Trainer          // RL 训练器
}
```

## 初始化顺序（New 函数）

这是整个系统最关键的函数，严格按序构建所有组件：

```
gateway.New(cfg)
    │
    ├── 1. store.Open()              # SQLite WAL 模式
    ├── 2. session.NewManager()      # 会话管理器
    ├── 3. tool.NewRegistry()        # 工具注册表
    │   ├── NewBashTool()            # bash 执行工具
    │   ├── NewFileTool()            # 文件读写工具
    │   └── NewHTTPTool()            # HTTP 请求工具
    ├── 4. agent.NewClaudeProvider() # Claude LLM 适配器
    ├── 5. agent.NewRuntime()        # Simple 模式运行时
    │
    ├── 6. 【Memory 系统】（if cfg.Memory.Enabled）
    │   ├── EmbeddingProvider（OpenAI or Noop）
    │   ├── NewFileMemoryStore()     # 文件存储
    │   ├── runtime.SetMemoryStore()
    │   ├── NewIncrementalCompressor()
    │   ├── NewForgettingCurveManager()
    │   ├── NewLLMFactExtractor()    # （if FactExtraction）
    │   ├── NewReflectionTracker()
    │   ├── NewLifecycleManager()
    │   ├── NewCompactor() → Start()
    │   ├── NewProfiler()
    │   ├── NewMemoryManageTool() → Register()
    │   └── 启动遗忘曲线 goroutine (24h 周期)
    │
    ├── 7. 【Cognitive Agent】（if mode=cognitive）
    │   ├── agent.NewCognitiveAgent()
    │   ├── SetMemoryStore()
    │   ├── SetFactExtractor()
    │   └── SetLifecycleManager()
    │
    ├── 8. 【RL 系统】（if rl.enabled + cognitive）
    │   ├── rl.NewStorage()
    │   ├── rl.NewPolicy() → LoadCheckpoint()
    │   ├── rl.NewTrainer()
    │   └── cognitiveAgent.SetRL{Policy,Trainer}()
    │
    ├── 9. 【Knowledge Base】（if knowledge.enabled）
    │   ├── knowledge.New()
    │   ├── NewHybridRetriever()
    │   ├── IngestDir() 初始摄取
    │   ├── cognitiveAgent.SetKnowledgeSearcher()
    │   │
    │   └── 【Knowledge Graph】（if graph_enabled）
    │       ├── graph.NewSQLiteGraph()
    │       ├── graph.NewLLMEntityExtractor()
    │       ├── 后台实体提取 goroutine
    │       ├── lifecycleMgr.SetGraphSync()
    │       └── GraphDecayTask (24h 周期)
    │
    ├── 10. 【Skill Manager】（if skills.enabled）
    │   ├── skill.New() → LoadBuiltin() → LoadDir()
    │   ├── runtime.SetSkillManager()
    │   └── tool.NewSkillTool() → Register()
    │
    ├── 11. 【Multi-Agent】（if agents.enabled）
    │   ├── agent.NewAgentManager()
    │   ├── LoadDir() + LoadDir(extra)
    │   ├── Add(inline definitions)
    │   └── RegisterAll(tools)
    │
    └── 12. scheduler.New()
```

## 适配器模式

Gateway 使用多个适配器来解耦模块间的循环依赖：

### completerAdapter
```go
// 桥接 agent.Provider → memory.Completer
type completerAdapter struct {
    provider agent.Provider
    model    string
}
```
**用途**：Memory 模块需要 LLM 补全能力（事实提取、生命周期决策、反射），但不能直接依赖 agent 包。`completerAdapter` 将 `agent.Provider` 适配为 `memory.Completer` 接口。

### noopKBEmbedder
```go
// 无 OpenAI key 时的降级方案
type noopKBEmbedder struct{}
```
**用途**：知识库在没有 OpenAI API key 时降级为纯 BM25 文本搜索。

## 消息路由

```
handleInbound(ctx, InboundMessage)
    │
    ├── 空消息 → 忽略
    ├── /new 或 /start → 重置会话
    │
    ├── cognitiveAgent != nil?
    │   ├── YES → cognitiveAgent.HandleMessage()
    │   └── NO  → runtime.HandleMessage()
    │
    └── 错误 → ch.Send("⚠️ Error: ...")
```

## 工具审批机制

```
handleApproval(ctx, ch, target, toolName, input)
    │
    ├── ch 实现了 ApprovalSender?
    │   ├── YES → sender.SendApprovalRequest()  # 交互式审批
    │   └── NO  → return true (自动批准)
    │
    └── 审批结果: approved/denied
```

## MCP 热重载

```
watchMCPDir(ctx)
    │
    └── 每 30 秒轮询 ~/.IronClaw/mcp/
        ├── 扫描 YAML 配置文件
        ├── 合并项目级 MCP 配置（优先级更高）
        └── SyncServers() — 启动新/关闭旧
```

## 生命周期

```
Start(ctx)
    ├── MCP 服务器启动
    ├── MCP 目录监控 goroutine
    ├── 各 Channel 启动（绑定 handleInbound）
    ├── Scheduler 启动
    ├── HTTP Admin 服务器（if enabled）
    └── RL Trainer 启动

Stop(ctx)
    ├── 各 Channel 停止
    ├── Scheduler 停止
    ├── MCP 关闭
    ├── RL Trainer 停止
    └── DB 关闭
```

## 设计特点

1. **顺序构建，并行运行**：New() 严格按序，Start() 并行启动
2. **可选组件**：通过配置开关控制 Memory/Knowledge/RL/Skills/Agents
3. **优雅降级**：无 OpenAI key → 纯文本搜索；无 FTS5 → LIKE 查询
4. **松耦合**：通过适配器模式隔离循环依赖
5. **后台任务**：遗忘曲线、图谱衰减、实体提取都在独立 goroutine 运行
