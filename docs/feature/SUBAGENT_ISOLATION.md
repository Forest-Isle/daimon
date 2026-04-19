# 子 Agent 隔离与编排

**日期**: 2026-04-19
**范围**: 引入 `SubAgentManager` 统一子 Agent 生命周期管理，实现上下文窗口隔离、工具白名单、模型路由、结构化结果聚合、并行执行策略、Markdown 声明式 Agent 定义

## 概述

此前 IronClaw 的子 Agent 系统存在三个核心问题：

1. **上下文污染**: `AgentTool` 虽然创建独立 Runtime，但会话管理不够隔离，长任务中子 Agent 的上下文可能互相干扰
2. **代码重复**: `AgentTool`（~580 行）内联了完整的 Runtime 构建、工具白名单、输出捕获等逻辑；`TeamCoordinator` 的 executor 仅做单次 LLM 调用而非完整 agent loop
3. **缺乏声明式定义**: Agent 只能通过 YAML 配置定义，无法像 Claude Code 的 `.claude/agents/*.md` 那样使用 Markdown 格式（YAML frontmatter + 自然语言 system prompt）

本次改动引入 `SubAgentManager` 作为子 Agent 生命周期的统一入口。`AgentTool` 和 `TeamCoordinator` 均委托给它，代码从分散的 ~700 行收敛到一个 343 行的核心文件。共 15 次提交（+1,800 行代码，涉及 18 个文件），零新增外部依赖。

**核心决策摘要：**

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 架构模式 | 中间层抽象（SubAgentManager） | 统一 AgentTool 和 TeamCoordinator 的执行路径，同时保持现有 Runtime 不变 |
| 隔离粒度 | 每次调用独立 session | `subagent_{name}_{uuid8}` 格式，调用结束后自动清理 |
| 模型路由 | 同 Provider 不同 Model | 复用已有 Provider，按 AgentSpec.Model 切换模型，避免多后端复杂性 |
| 结果聚合 | XML 模板优先 + LLM 摘要回退 | 模板提取零开销且确定性高，LLM 回退处理非结构化输出 |
| 并行策略 | 可配置 best_effort / fail_fast | 默认 best_effort 适合独立任务，fail_fast 适合有依赖链的场景 |
| Agent 定义格式 | .md 与 .yaml 共存 | .md 格式贴合 Claude Code 习惯，.yaml 保持向后兼容 |

## 架构

### 整体设计

```
父 Agent Runtime
    │
    ├── AgentTool.Execute(task)           ← LLM 发起 tool_use 调用
    │       │
    │       └── SubAgentManager.Spawn(SpawnRequest)
    │               │
    │               ├── 生成唯一 sessionID: subagent_{name}_{uuid8}
    │               ├── 构建 scoped tool Registry（工具白名单，排除 agent_*）
    │               ├── 应用模型/参数覆盖（buildSubConfig）
    │               ├── 创建独立 Runtime
    │               ├── Runtime.HandleMessage → 完整 agent loop
    │               ├── 捕获输出（subagentCapture channel）
    │               ├── 提取结构化结果（extractStructuredResult / summarizeWithLLM）
    │               ├── 清理临时 session（session.Manager.Delete）
    │               └── 返回 SubAgentResult
    │
    └── TeamCoordinator.executor(task)    ← /team 命令分发任务
            │
            └── SubAgentManager.Spawn(SpawnRequest)  ← 同一执行路径
```

### 与改动前的对比

| 维度 | 改动前 | 改动后 |
|------|--------|--------|
| AgentTool 代码量 | ~580 行（内联所有逻辑） | ~132 行（委托给 SubAgentManager） |
| TeamCoordinator executor | 单次 `provider.Complete()` 调用 | 完整 `SubAgentManager.Spawn()` agent loop |
| 上下文隔离 | 部分隔离（共享 session key 前缀） | 完全隔离（唯一 `subagent_{name}_{uuid8}` session，用完即删） |
| 结果格式 | 原始文本输出 | 结构化 `SubAgentResult`（status/summary/artifacts/duration） |
| Agent 定义 | 仅 .yaml | .yaml + .md（YAML frontmatter + Markdown body） |
| 并行策略 | 无 | best_effort / fail_fast |
| 模型路由 | 固定使用全局模型 | 每个 AgentSpec 可指定独立模型 |

## SubAgentManager

### 核心结构

```go
type SubAgentManager struct {
    provider  Provider           // LLM 后端
    sessions  *session.Manager   // 会话管理（含 Delete 清理）
    db        *store.DB          // SQLite 存储
    memStore  memory.Store       // 记忆系统（可选）
    tools     *tool.Registry     // 父级工具注册表
    cfg       config.AgentConfig // Agent 配置
    llmCfg    config.LLMConfig   // LLM 配置
    bgManager *BackgroundManager // 后台执行管理器（可选）
    agentMCP  *AgentMCPManager   // MCP 工具管理器（可选）
}
```

### Spawn 流程

```
SpawnRequest{Spec, Task, TaskContext, ParentID, ParentDepth, ChainID}
    │
    ├── [1] 检查 ExecutionMode
    │       background → spawnBackground() → BackgroundManager.Spawn()
    │       spawn/fork → 继续同步执行
    │
    ├── [2] 生成唯一 sessionID
    │       格式: subagent_{spec.Name}_{uuid[:8]}
    │       例: subagent_reviewer_a3f2c1d9
    │
    ├── [3] 构建 scoped 工具注册表
    │       ├── 遍历父级 Registry.All()
    │       ├── 排除所有 agent_* 工具（防递归）
    │       └── 若 Spec.Tools 非空，仅保留白名单中的工具
    │
    ├── [4] 构建子配置（buildSubConfig）
    │       ├── AgentConfig: 继承父级，覆盖 MaxIterations/SystemPrompt
    │       ├── LLMConfig: 继承父级，覆盖 Model/MaxTokens
    │       └── SystemPrompt 追加 subagentOutputInstruction
    │
    ├── [5] 创建独立 Runtime
    │       ├── NewRuntime(provider, scopedTools, sessions, db, subCfg, subLLMCfg)
    │       ├── SetMemoryStore (如有)
    │       └── SetAgentID / SetParentID / SetDepth / SetChainID（谱系追踪）
    │
    ├── [6] 执行 Runtime.HandleMessage
    │       ├── subagentCapture 作为 channel 捕获输出
    │       └── InboundMessage{Channel:"subagent", ChannelID:sessionID}
    │
    ├── [7] 清理临时 session
    │       session.Manager.Delete(ctx, "subagent", sessionID)
    │       ├── 从 sync.Map 内存缓存删除
    │       └── 从 SQLite sessions/messages 表删除
    │
    └── [8] 构建结果（buildResult）
            ├── 优先: extractStructuredResult(raw) 解析 <result> XML 块
            ├── 回退: 截断原始输出作为 summary
            └── 返回 SubAgentResult
```

### SpawnParallel

```go
func (m *SubAgentManager) SpawnParallel(
    ctx context.Context,
    reqs []SpawnRequest,
    strategy FailureStrategy,
) ([]*SubAgentResult, error)
```

并行启动多个子 Agent，支持两种策略：

| 策略 | 行为 | 适用场景 |
|------|------|---------|
| `best_effort` (默认) | 所有子 Agent 运行至完成，即使部分失败 | 独立任务（代码审查 + 测试 + 文档） |
| `fail_fast` | 首个失败立即 cancel 其余子 Agent | 有隐含依赖的并行任务 |

`fail_fast` 通过 `context.WithCancel` + `cancel()` 实现。所有 goroutine 通过 `sync.WaitGroup` 同步等待。结果数组保持与请求的索引对应关系。

## 上下文隔离

### 会话隔离

每次 `Spawn()` 调用生成唯一的 `sessionID`（格式 `subagent_{name}_{uuid8}`），确保：

1. **独立消息历史**: 每个子 Agent 有自己的消息列表，不与父 Agent 或其他子 Agent 共享
2. **独立 token 计数**: 子 Agent 的 context window 从零开始，不受父 Agent 累积历史的影响
3. **自动清理**: 执行完成后 `session.Manager.Delete()` 从内存和 SQLite 中移除临时会话

```go
// session.Manager.Delete — 专为临时子 Agent 会话设计
func (m *Manager) Delete(ctx context.Context, channel, channelID string) error {
    key := sessionKey(channel, channelID)
    if v, ok := m.sessions.Load(key); ok {
        sess := v.(*Session)
        _, _ = m.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sess.ID)
        _, _ = m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID)
    }
    m.sessions.Delete(key)
    return nil
}
```

### 工具隔离

`buildScopedRegistryStandalone` 为每个子 Agent 创建独立的工具注册表：

- **自动排除 `agent_*` 工具**: 防止子 Agent 递归调用其他 Agent 工具（无限嵌套保护）
- **白名单过滤**: 若 `AgentSpec.Tools` 非空，仅注册白名单中的工具
- **空白名单 = 全部可用**: 白名单为空时继承父级所有非 agent 工具

### 谱系追踪

每个子 Agent Runtime 被注入四项元数据：

| 字段 | 来源 | 用途 |
|------|------|------|
| `agentID` | `uuid.New()` | 子 Agent 实例唯一标识 |
| `parentID` | 父 Runtime | 追踪调用链 |
| `depth` | 父 depth + 1 | 嵌套深度控制 |
| `chainID` | 继承或新生成 | 同一调用链内所有 Agent 共享 |

## 结构化结果聚合

### SubAgentResult

```go
type SubAgentResult struct {
    AgentName  string         // 子 Agent 名称
    Status     SubAgentStatus // success | error | timeout | background
    Summary    string         // 一段话摘要
    Output     string         // 完整原始输出
    Artifacts  []string       // 文件路径、URL 等关键产物
    Duration   time.Duration  // 执行耗时
    TokensUsed int            // token 消耗
    Error      string         // 错误信息（Status=error 时）
}
```

### 结果提取策略

```
子 Agent 原始输出
    │
    ├── [1] extractStructuredResult(raw)
    │       ├── 正则匹配 <result>...</result> XML 块
    │       ├── 解析 <status>、<summary>、<artifacts>
    │       └── 成功 → 返回 SubAgentResult
    │
    └── [2] 回退: 截断原始输出
            ├── 取前 500 字符作为 summary
            └── Status = success
```

子 Agent 的 system prompt 被追加了 `subagentOutputInstruction`，指引其在任务完成时输出 `<result>` XML 块：

```xml
<result>
<status>success|error</status>
<summary>One paragraph summary of what was accomplished</summary>
<artifacts>Comma-separated list of file paths, URLs, or key outputs</artifacts>
</result>
```

XML 提取使用预编译正则，零 LLM 开销。对于不遵循格式的输出，直接截断原始文本作为摘要。

### 父 Agent 视角

`formatResultForParent` 将 `SubAgentResult` 格式化为父 Agent 可读的文本：

```
Agent: reviewer | Status: success | Duration: 2.3s
Summary: Found 3 issues in auth module, all fixable.
Artifacts: /src/auth.go, /src/auth_test.go
```

## Markdown 声明式 Agent 定义

### 格式

支持 `.ironclaw/agents/*.md` 文件，使用 YAML frontmatter + Markdown body（作为 system prompt）：

```markdown
---
name: "code-reviewer"
description: "Reviews code for correctness and security issues."
model: claude-haiku
max_iterations: 5
tools:
  - bash
  - file_read
timeout: "120s"
failure_strategy: fail_fast
tags:
  - review
  - quality
---

You are an expert code reviewer. Focus on:

1. **Correctness** — logic errors, edge cases, off-by-one
2. **Security** — injection, auth bypass, data leaks
3. **Performance** — unnecessary allocations, N+1 queries

Be concise. Cite specific line numbers.
```

### 加载机制

`AgentManager.LoadDir` 同时支持 `.yaml`/`.yml` 和 `.md` 两种格式：

```go
switch {
case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
    spec, loadErr = loadAgentSpec(path)        // 已有 YAML 加载器
case strings.HasSuffix(name, ".md"):
    spec, loadErr = loadMarkdownAgentSpec(path) // 新增 Markdown 加载器
default:
    continue
}
```

`loadMarkdownAgentSpec` 流程：

1. 读取文件 → `config.ExpandEnv` 展开 `${VAR}` 环境变量
2. `splitFrontmatter` 分离 YAML frontmatter 和 Markdown body
3. `yaml.Unmarshal` 解析 frontmatter 到 `AgentSpec`
4. Markdown body 赋值给 `spec.SystemPrompt`

### 与 YAML 格式的共存

| 维度 | .yaml/.yml | .md |
|------|-----------|-----|
| 元数据 | 全部在 YAML 中 | YAML frontmatter |
| System Prompt | `system_prompt:` 字段 | Markdown body（更自然，支持格式化） |
| 环境变量 | `${VAR}` 展开 | `${VAR}` 展开 |
| 优先级 | 同名时 .md 后加载可覆盖 | — |

## FailureStrategy

新增 `FailureStrategy` 类型到 `AgentSpec`：

```go
type FailureStrategy string

const (
    StrategyBestEffort FailureStrategy = "best_effort" // 默认
    StrategyFailFast   FailureStrategy = "fail_fast"
)
```

- `Validate()` 中空值自动设为 `best_effort`
- 非法值（非 `best_effort` / `fail_fast`）返回验证错误
- 用于 `SpawnParallel` 控制并行子 Agent 的失败行为

## AgentTool 简化

### 改动前（~580 行）

```
AgentTool
├── 10+ 字段（spec, provider, sessions, db, memStore, tools, cfg, llmCfg, breaker, bgManager, agentMCP）
├── NewAgentTool(spec, provider, sessions, db, memStore, tools, cfg, llmCfg)  ← 8 参数
├── Execute → executeSpawn / executeFork / executeBackground
├── executeAgentCore — 内联构建 Runtime + HandleMessage
├── buildScopedRegistry — 工具白名单
├── captureChannel / captureUpdater — 输出捕获
└── SetBackgroundManager / SetAgentMCPManager
```

### 改动后（~132 行）

```
AgentTool
├── 3 字段（spec, manager, breaker）
├── NewAgentTool(spec, manager)  ← 2 参数
├── Execute → manager.Spawn(SpawnRequest)  ← 一行委托
└── Name / Description / InputSchema / RequiresApproval
```

所有执行逻辑、工具白名单构建、输出捕获、结果提取均收敛到 `SubAgentManager`。`AgentTool` 仅负责：
1. 输入验证和 JSON 反序列化
2. 超时控制
3. 熔断器（CircuitBreaker）状态管理
4. 委托执行并格式化结果

## Gateway 集成

### 初始化顺序

```
initMultiAgent()
    ├── NewAgentManager(...)
    ├── NewSubAgentManager(provider, sessions, db, memStore, tools, cfg, llmCfg)
    │       → gw.subAgentMgr = subAgentMgr
    ├── agentMgr.SetSubAgentManager(subAgentMgr)
    ├── agentMgr.LoadDir(agentsDir)   ← 支持 .yaml + .md
    ├── agentMgr.RegisterAll(tools)    ← 使用 SubAgentManager 创建 AgentTool
    ├── NewBackgroundManager(...)
    │       → subAgentMgr.SetBackgroundManager(bgManager)
    └── NewAgentMCPManager(...)
            → subAgentMgr.SetAgentMCPManager(agentMCPMgr)
```

### TeamCoordinator 升级

```go
tc.SetExecutor(func(ctx context.Context, task taskledger.Task) (string, error) {
    if gw.subAgentMgr == nil {
        return gw.executeTeamTask(ctx, task) // 回退到单次 LLM 调用
    }
    spec := &agent.AgentSpec{
        Name:          fmt.Sprintf("team_%s", taskIDShort),
        Description:   "Team task worker",
        SystemPrompt:  "You are an agent executing a specific task. Be concise and focused.",
        MaxIterations: 10,
    }
    if gw.cfg.Agent.Team.Model != "" {
        spec.Model = gw.cfg.Agent.Team.Model
    }
    _ = spec.Validate()
    result, err := gw.subAgentMgr.Spawn(ctx, agent.SpawnRequest{
        Spec: spec,
        Task: task.Description,
    })
    // ...
    return result.Summary, nil
})
```

每个 Team 任务现在获得完整的 agent loop（多轮 LLM + 工具调用），而非此前的单次 LLM 补全。

## 配置

### AgentSpec 新增字段

```yaml
# .ironclaw/agents/reviewer.yaml 或 .md frontmatter
name: reviewer
description: "Code review agent"
model: claude-haiku              # 可选: 覆盖全局模型
max_iterations: 5                # 可选: 覆盖全局迭代上限
max_tokens: 4096                 # 可选: 覆盖全局 max_tokens
failure_strategy: best_effort    # best_effort (默认) | fail_fast
tools:                           # 可选: 工具白名单（空 = 全部）
  - bash
  - file_read
  - file_write
timeout: "120s"                  # 可选: 执行超时
```

### Team 模型路由

```yaml
agent:
  team:
    enabled: true
    max_workers: 3
    model: "claude-haiku"  # Team 任务使用便宜快速的模型
```

## 降级矩阵

| 组件 | 正常 | 降级 |
|------|------|------|
| SubAgentManager | Spawn → 独立 session + scoped tools + agent loop | — |
| BackgroundManager | background 模式异步执行 | 无 BackgroundManager → 回退到同步 Spawn |
| TeamCoordinator | SubAgentManager.Spawn（完整 agent loop） | 无 SubAgentManager → 回退到单次 LLM 调用 |
| Markdown 加载 | .md → YAML frontmatter + Markdown body | 格式错误 → 跳过并 warn 日志 |
| 结构化结果提取 | `<result>` XML 解析 | 无 XML 块 → 截断原始输出作为 summary |

## 新增文件清单

| 文件路径 | 职责 |
|---------|------|
| `internal/agent/subagent.go` | `SubAgentManager`、`SpawnRequest`、`Spawn`、`SpawnParallel`、`buildSubConfig`、`buildScopedRegistryStandalone`、`subagentCapture` |
| `internal/agent/subagent_result.go` | `SubAgentResult`、`SubAgentStatus`、`extractStructuredResult`、`summarizeWithLLM`、`formatResultForParent`、`subagentOutputInstruction` |
| `internal/agent/subagent_test.go` | Spawn 独立 session、模型覆盖、后台回退、工具白名单、并行执行 |
| `internal/agent/subagent_result_test.go` | XML 提取（合法/无块/错误状态/无 artifact）、格式化 |
| `internal/agent/subagent_integration_test.go` | AgentTool → SubAgentManager → Runtime 全链路集成 |
| `internal/agent/agent_manager_test.go` | Markdown 加载、frontmatter 分割、混合格式 LoadDir |
| `internal/agent/spec_test.go` | FailureStrategy 验证（空默认、合法值、非法值） |
| `internal/session/manager_test.go` | Delete 方法（创建→缓存命中→删除→新 session） |

## 修改文件清单

| 文件路径 | 变更 |
|---------|------|
| `internal/agent/spec.go` | 新增 `FailureStrategy` 类型/常量/字段，`Validate()` 新增默认值和校验 |
| `internal/agent/agent_tool.go` | 重构: 580→132 行，删除内联执行逻辑，委托 `SubAgentManager.Spawn()` |
| `internal/agent/agent_manager.go` | 新增 `subAgentMgr` 字段、`SetSubAgentManager`、`loadMarkdownAgentSpec`、`splitFrontmatter`；`LoadDir` 支持 .md；`RegisterAll` 使用 SubAgentManager |
| `internal/agent/act.go` | `SubAgentResult` 字段 `DurationMs` → `Duration`（类型对齐） |
| `internal/agent/task_context.go` | 移除旧 `SubAgentResult`（4 字段版本），由 `subagent_result.go` 中的丰富版本替代 |
| `internal/session/manager.go` | 新增 `Delete` 方法用于临时 session 清理 |
| `internal/gateway/gateway.go` | 新增 `subAgentMgr` 字段；TeamCoordinator executor 升级为 `SubAgentManager.Spawn()` |
| `internal/gateway/init_multiagent.go` | 创建 `SubAgentManager`，接入 `AgentManager`/`BackgroundManager`/`AgentMCPManager` |
| `CLAUDE.md` | 新增 SubAgentManager 架构描述、Gateway 初始化顺序更新 |

## 测试

共 16 个测试函数，覆盖全部核心逻辑：

| 组件 | 测试文件 | 数量 | 覆盖内容 |
|------|---------|------|---------|
| FailureStrategy | `spec_test.go` | 1 (4 子测试) | 空值默认、best_effort 合法、fail_fast 合法、非法值报错 |
| session.Delete | `manager_test.go` | 1 | 创建 session → 缓存命中 → Delete → 新 session 得到不同 ID |
| SubAgentResult | `subagent_result_test.go` | 5 | 合法 XML、无块返回 nil、error 状态、无 artifacts、formatResultForParent |
| SubAgentManager | `subagent_test.go` | 5 | 独立 session（两次 Spawn 不互相影响）、模型覆盖传播、后台回退降级、工具白名单过滤、并行 best_effort |
| AgentManager | `agent_manager_test.go` | 3 | Markdown 加载（全字段校验）、frontmatter 分割（合法/无头/未闭合）、混合格式 LoadDir |
| 集成测试 | `subagent_integration_test.go` | 1 | AgentTool.Execute → SubAgentManager.Spawn → Runtime.HandleMessage → subagentCapture → extractStructuredResult → formatResultForParent 全链路 |

## 与现有子系统的关系

### 与 Agent Teams（TeamCoordinator）

Agent Teams 的 `TeamCoordinator` 此前使用 `executeTeamTask`（单次 LLM 调用）作为 executor。本次改动将 executor 升级为 `SubAgentManager.Spawn()`，每个 Team 任务获得：

- 独立 session 和 context window
- 多轮 LLM + 工具调用能力
- 结构化结果返回
- 可配置的模型路由（`team.model`）

### 与 BackgroundManager

`SubAgentManager` 在 `ExecModeBackground` 时委托给 `BackgroundManager.Spawn()`，返回 `StatusBackground` 的 `SubAgentResult`。若 `BackgroundManager` 未设置，自动降级为同步 Spawn。

### 与拦截器链（InterceptorChain）

子 Agent 的 Runtime 继承 Gateway 级别的拦截器链设置。若父 Runtime 配置了拦截器链（权限 → Hook → 沙箱），子 Agent 的工具执行同样受到安全策略约束。

### 与记忆系统

`SubAgentManager` 可选注入 `memory.Store`。注入后，子 Agent 可在执行过程中读写记忆（但使用独立的 session scope），不与父 Agent 的 session 记忆冲突。

## 未来扩展

以下功能不在本次改动范围内，但设计已预留接口：

| 扩展点 | 现状 | 未来方向 |
|--------|------|---------|
| 多 Provider 路由 | 同 Provider 不同 Model | 按 AgentSpec 选择不同 Provider（OpenAI/Claude/本地模型） |
| 进程级隔离 | goroutine 级 | 子 Agent 运行在独立进程或容器中 |
| 会话持久化 | 临时 session（用完即删） | `persist_session: true` 选项保留子 Agent 会话 |
| token 预算 | 继承父级配置 | 按子 Agent 独立设定 token 预算上限 |
| 结果缓存 | 每次重新执行 | 相同 task + spec 命中缓存，跳过执行 |
