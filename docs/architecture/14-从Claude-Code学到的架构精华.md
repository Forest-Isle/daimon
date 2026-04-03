# IronClaw 从 Claude Code 学到的子代理架构精华

> 本文系统分析 IronClaw 如何从 Claude Code 的子代理系统中提取核心架构模式，并适配到 Go 生态。
> 涵盖设计哲学迁移、9 个核心模式的对比实现，以及未采纳部分的取舍分析。

---

## 一、背景

### 1.1 两个项目的定位

| 项目 | 语言 | 定位 | 子代理成熟度 |
|------|------|------|------------|
| **Claude Code** | TypeScript | 桌面端 CLI AI 编码助手 | 生产级，经过大规模用户验证 |
| **IronClaw** | Go | 本地优先 AI 代理运行时 | 核心功能完备，子代理系统待增强 |

### 1.2 优化前的差距

优化前 IronClaw 的子代理系统是一个**单一模式**设计——每次调用子代理都创建全新的独立 Runtime，类似于每次开一个新终端窗口。Claude Code 的设计则更像一个**多模态操作系统**——不同任务用不同的执行策略。

| 能力 | 优化前 IronClaw | Claude Code |
|------|----------------|-------------|
| 执行模式 | 仅 Spawn（独立创建） | Fork / Spawn / Background |
| 上下文传递 | 无（每次从零开始） | 字节级 Prompt Cache 共享 |
| 调度能力 | 顺序执行 | 多 agent 并发 |
| 权限控制 | 仅 requires_approval | bubble / acceptEdits / bypass |
| 执行记录 | 无 | Sidechain Transcript |
| 生命周期 | 无钩子 | SubagentStart + Skill 预加载 |
| 执行后端 | 仅 In-Process | InProcess / Tmux / iTerm |
| 独立 MCP | 无 | 每个 agent 可有独立 MCP Server |

---

## 二、核心设计哲学的迁移

### 2.1 Claude Code 的核心哲学

> **"用最少的上下文差异，获得最大的缓存收益"**

Claude Code 的每一个架构决策都围绕一个核心问题：**如何让多个 agent 共享尽可能多的请求前缀，从而命中 Anthropic API 的 Prompt Cache。** 这决定了：

- Fork Agent 复用父级完整消息历史（字节级相同）
- `CacheSafeParams` 确保 system prompt、user context、tool context 在所有 fork 子代间保持一致
- `maxOutputTokens` 差异会导致 cache miss，因此需要谨慎控制

### 2.2 IronClaw 的适配哲学

> **"用 Go 的并发原语，实现精确的隔离与共享控制"**

Go 没有 TypeScript 那样灵活的 JSON 序列化控制，无法做到字节级 Prompt Cache。但 Go 的 goroutine、channel、context.Context 让并发和隔离控制更加自然：

| Claude Code (TypeScript) | IronClaw (Go) | 优劣对比 |
|-------------------------|---------------|---------|
| AsyncGenerator 流式消息 | goroutine + channel | Go 更简洁 |
| Promise + callback 权限冒泡 | channel 请求-响应 | Go 类型更安全 |
| 闭包捕获上下文 | context.Context 值传递 | Go 显式、可追踪 |
| errgroup 替代品（自实现） | golang.org/x/sync/errgroup | Go 标准库支持 |

---

## 三、9 个核心架构模式的对比与实现

### 3.1 Fork Agent — 上下文继承

**这是从 Claude Code 学到的最核心的设计。**

#### Claude Code 的实现

当用户在对话中途需要并行处理子任务时，Claude Code 不会创建一个"什么都不知道"的新 agent，而是**分叉（fork）当前对话**——子 agent 拿到父级的完整消息历史和系统提示词，只追加一条新指令。这就像 Unix 的 `fork()` 系统调用：子进程继承父进程的全部内存。

```typescript
// Claude Code: Fork 消息构建
export function buildForkedMessages(directive, assistantMessage) {
  // 1. 完整克隆父级助手消息（字节相同，用于 Prompt Cache）
  const fullAssistantMessage = { ...assistantMessage, uuid: randomUUID() }

  // 2. 收集所有 tool_use blocks
  const toolUseBlocks = assistantMessage.message.content.filter(
    block => block.type === 'tool_use'
  )

  // 3. 构建相同的占位 tool_results（每个 fork 子代都一样）
  const toolResultBlocks = toolUseBlocks.map(block => ({
    type: 'tool_result',
    tool_use_id: block.id,
    content: [{ type: 'text', text: 'Fork started — processing in background' }]
  }))

  // 4. 只在最后追加不同的 directive
  // → 所有 fork 子代的请求前缀完全相同 → Prompt Cache 命中
  return [fullAssistantMessage, createUserMessage({
    content: [...toolResultBlocks, { type: 'text', text: directive }]
  })]
}
```

Claude Code 还通过 `CacheSafeParams` 强制保证五个参数（systemPrompt, userContext, systemContext, toolUseContext, forkContextMessages）在所有 fork 子代间**字节级一致**：

```typescript
export type CacheSafeParams = {
  systemPrompt: SystemPrompt
  userContext: { [k: string]: string }
  systemContext: { [k: string]: string }
  toolUseContext: ToolUseContext
  forkContextMessages: Message[]
}

// 全局保存，确保所有 fork 子代共享
let lastCacheSafeParams: CacheSafeParams | null = null
```

#### IronClaw 的适配实现

Go 没有 TypeScript 那样灵活的 JSON 序列化控制，无法做到字节级的 Prompt Cache。但我们借鉴了核心理念——**复制而非重建**：

```go
// IronClaw: Fork 消息构建
func BuildForkMessages(parentMessages []session.Message, directive string) []session.Message {
    // 完整复制父级消息历史（不可变，不修改原始切片）
    msgs := make([]session.Message, len(parentMessages), len(parentMessages)+1)
    copy(msgs, parentMessages)

    // 只追加 fork 指令（XML 标签包裹，便于检测）
    forkMsg := session.Message{
        Role:    "user",
        Content: fmt.Sprintf("<fork-directive>\n%s\n</fork-directive>", directive),
    }
    msgs = append(msgs, forkMsg)
    return msgs
}
```

同时用 `PromptCache` 在 Go 层面去重系统提示词构建：

```go
// 相同配置的 agent 共享缓存的系统提示词（应用层缓存，非 API 层）
func (r *Runtime) buildSystemPrompt(ctx context.Context, userText string) string {
    if r.promptCache != nil && r.agentID != "" {
        cacheKey := fmt.Sprintf("runtime:%s:%s", r.agentID, sha256Hex(userText)[:8])
        return r.promptCache.GetOrBuild(cacheKey, func() string {
            return r.buildSystemPromptUncached(ctx, userText)
        })
    }
    return r.buildSystemPromptUncached(ctx, userText)
}
```

**关键差异**：Claude Code 的 cache 是 API 层面的（减少 token 费用），IronClaw 的 cache 是应用层面的（减少 CPU 开销）。理念相同，层次不同。

---

### 3.2 三种执行模式的统一分发

#### Claude Code 的实现

Claude Code 有一套完整的 agent 类型系统，每种类型有不同的行为。以 `BuiltInAgentDefinition` 定义：

```typescript
// Claude Code: 内置 Agent 类型
export const FORK_AGENT = {
  agentType: 'fork',
  tools: ['*'],                 // 所有父级工具（保持字节一致）
  maxTurns: 200,
  model: 'inherit',            // 继承父级模型
  permissionMode: 'bubble',    // 权限冒泡到父级
  getSystemPrompt: () => '',   // 不生成新的系统提示词
}

// 其他内置类型：general-purpose, code, explore, plan, session-memory
```

通过 `isForkSubagentEnabled()` feature gate 控制 fork 路径的启用：

```typescript
if (isForkSubagentEnabled() && !subagent_type) {
  // 隐式 fork — 当用户省略 subagent_type 时触发
  return executeForkPath(directive)
}
// 显式 spawn — 按 subagent_type 创建
return executeSpawnPath(agentType, instructions)
```

#### IronClaw 的适配实现

我们没有硬编码 agent 类型，而是通过 `ExecutionMode` 让用户在 YAML 中自由组合。这比 Claude Code 更灵活——用户可以定义任意 mode + permission + tools 的组合：

```go
// IronClaw: 统一分发
func (a *AgentTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
    // ... 断路器检查、输入解析、超时设置 ...

    switch a.spec.ExecutionMode {
    case ExecModeFork:
        return a.executeFork(ctx, in)       // 继承上下文
    case ExecModeBackground:
        return a.executeBackground(ctx, in) // 异步执行
    default:
        return a.executeSpawn(ctx, in)      // 独立创建（默认）
    }
}
```

**设计取舍**：Claude Code 用预定义类型减少用户配置负担（"选一个类型就好"），IronClaw 用字段组合提供更大灵活性（"自由搭配"）。两种方式各有优劣，适合不同的用户群体。

---

### 3.3 并行调度与 DAG 依赖

#### Claude Code 的实现

Claude Code 在工具层面实现了并发：只读工具（Read、Glob、Grep）最多 10 个并行，写工具串行。这个思路可以扩展到 agent 层面——多个 agent 并发工作但需要协调依赖。

Claude Code 通过三种后端实现并发：

```typescript
// InProcess — 共享堆，异步函数
const isolatedContext = createSubagentContext(parentContext)
for await (const msg of runAgent({ toolUseContext: isolatedContext })) { ... }

// Tmux — 进程隔离，tmux 会话
const cmd = `${buildInheritedEnvVars()} ${getTeammateCommand()} ${buildInheritedCliFlags()}`
tmux new-session -d -s <session-id> <cmd>

// iTerm — macOS 原生，AppleScript
// (省略)
```

#### IronClaw 的适配实现

Go 的 goroutine/channel 让并发调度更加自然——Claude Code 需要复杂的 `AsyncGenerator` + Promise 模式，Go 只需要 `errgroup.Go()` 一行：

```go
// IronClaw: 并行执行（errgroup 自动限制并发数）
func (o *AgentOrchestrator) ExecuteParallel(ctx, tasks, executor) ([]*AgentResult, error) {
    results := make([]*AgentResult, len(tasks))
    g, gctx := errgroup.WithContext(ctx)
    g.SetLimit(o.maxParallel) // 默认 4

    for i, task := range tasks {
        i, task := i, task
        g.Go(func() error {
            result, err := executor(gctx, task)
            if err != nil {
                results[i] = &AgentResult{Error: err} // 失败不中断其他
                return nil
            }
            results[i] = result
            return nil
        })
    }
    _ = g.Wait()
    return results, nil
}

// DAG 依赖调度：Kahn 拓扑排序 → 逐层并行
func (o *AgentOrchestrator) ExecuteDAG(ctx, tasks, executor) ([]*AgentResult, error) {
    layers, err := TopologicalSort(tasks) // a → [b,c]（并行）→ d
    for _, layer := range layers {
        ExecuteParallel(ctx, layer, executor)
    }
}
```

**IronClaw 独有**：DAG 依赖调度是 Claude Code 没有的。Claude Code 的 agent 之间没有显式依赖关系，而 IronClaw 的 `AgentTask.DependsOn` 字段允许声明"任务 B 必须等任务 A 完成才能开始"。

---

### 3.4 SubagentContext — 精确隔离控制

#### Claude Code 的实现

Claude Code 的 `createSubagentContext()` 是一个精心设计的隔离边界控制器。每个字段都有明确的默认策略和可覆盖选项：

```typescript
// Claude Code: 隔离策略表
return {
  // 克隆 — 防止文件缓存交叉污染
  readFileState: cloneFileStateCache(parentContext.readFileState),

  // 链接 — 父级取消传播到子级，子级取消不影响父级
  abortController: createChildAbortController(parentContext.abortController),

  // 包装 — 后台 agent 避免弹出权限框
  getAppState: () => ({
    ...state,
    toolPermissionContext: { shouldAvoidPermissionPrompts: true }
  }),

  // No-op — 子代理默认不能修改父级状态
  setAppState: () => {},

  // 新集合 — 嵌套触发器独立
  nestedMemoryAttachmentTriggers: new Set(),
  discoveredSkillNames: new Set(),
}
```

关键的设计思想是：**每个字段的隔离级别都是有意识的选择，而不是统一的"全部隔离"或"全部共享"。**

#### IronClaw 的适配实现

```go
// IronClaw: 分层隔离
type SubagentContext struct {
    // 隔离层 — 每个子代理独立拥有
    ToolRegistry *tool.Registry      // 受限工具集（agent_* 始终排除）
    Permission   PermissionMode      // 独立权限模式
    Cancel       context.CancelFunc  // 独立取消（Go context 自然支持链式取消）
    AbortOnParent bool               // 父级取消时是否跟随

    // 继承层 — 只读共享引用
    ParentMessages []session.Message // 父级消息历史（仅 fork 模式填充）
    SystemPrompt   string            // 父级系统提示词（复用）
    Memory         memory.Store      // 共享记忆（只读查询）
    Sessions       *session.Manager  // 共享会话管理器
    DB             *store.DB         // 共享数据库

    // 追踪层 — 调用链审计
    AgentID  string                  // 唯一 ID
    ParentID string                  // 父级 ID（空 = 顶层）
    Depth    int                     // 嵌套深度（0 = 顶层）
    ChainID  string                  // 调用链 ID（同链所有 agent 共享）
}
```

通过 `context.Context` 在代理调用链中传递（Go 的显式上下文传递比 TypeScript 的闭包捕获更可追踪）：

```go
// 存储：父级 Runtime 注入到 context
ctx = RuntimeToContext(ctx, parentRuntime)
ctx = SubagentContextToCtx(ctx, subCtx)

// 读取：子代理从 context 获取父级信息
parent := RuntimeFromContext(ctx)   // 获取父级 Runtime
sc := SubagentContextFromCtx(ctx)   // 获取隔离上下文
```

---

### 3.5 权限分级模型 — Permission Bubble

#### Claude Code 的实现

Claude Code 最巧妙的安全设计之一是 **Permission Bubble**。核心问题：后台 agent 不能弹出权限对话框（那会冻结 UI），所以权限请求必须"冒泡"到父级终端。

```typescript
// Claude Code: 三种权限模式
const agentGetAppState = () => ({
  ...state,
  toolPermissionContext: {
    // 根据 agent 的 permissionMode 覆盖
    permissionMode: agent.permissionMode || state.permissionMode,
    shouldAvoidPermissionPrompts: (
      agent.permissionMode === 'bubble' && !overrides?.shareAbortController
    ),
    awaitAutomatedChecksBeforeDialog: agent.permissionMode === 'bubble',
  }
})

// 权限模式定义
| 模式             | 行为                        |
|-----------------|----------------------------|
| bubblePermissions | 权限请求冒泡到父级终端         |
| acceptEdits      | 自动批准所有写操作            |
| bypassPermissions | 不弹框（规划 agent 用）      |
```

#### IronClaw 的适配实现

用 Go channel 实现了更清晰的请求-响应模式（比 JavaScript 的 callback 更类型安全）：

```go
// IronClaw: 权限评估器
type PermissionEvaluator struct {
    mode     PermissionMode
    parentCh chan<- PermissionRequest  // bubble 模式：发送到父级
}

func (pe *PermissionEvaluator) Check(ctx, toolName, input) (bool, string) {
    switch pe.mode {
    case PermModeBypass:
        return true, "bypass: 全部允许"

    case PermModeAcceptEdits:
        if IsDangerousOperation(toolName, input) {
            return pe.bubbleToParent(ctx, ...)  // 危险操作冒泡到父级
        }
        return true, "accept_edits: 自动批准"   // 安全操作自动通过

    case PermModeBubble:
        return pe.bubbleToParent(ctx, ...)       // 所有操作冒泡

    default:
        return true, "default: 遵循标准检查"
    }
}

// Bubble 实现：channel 请求-响应（带超时保护）
func (pe *PermissionEvaluator) bubbleToParent(ctx, toolName, input) (bool, string) {
    req := PermissionRequest{
        ToolName:   toolName,
        Input:      input,
        ResponseCh: make(chan PermissionResponse, 1),
    }
    select {
    case pe.parentCh <- req:            // 发送请求
    case <-time.After(5 * time.Second): // 发送超时
        return false, "发送超时"
    }
    select {
    case resp := <-req.ResponseCh:      // 等待响应
        return resp.Allowed, resp.Reason
    case <-time.After(30 * time.Second): // 等待超时
        return false, "等待响应超时"
    }
}
```

**IronClaw 扩展**：危险操作黑名单（`rm -rf`, `chmod 777`, `kill -9`, `iptables`, `shutdown` 等），Claude Code 没有这个显式列表——它依赖外部权限系统。

---

### 3.6 Sidechain 执行记录

#### Claude Code 的实现

Claude Code 为每个 agent 维护独立的 **sidechain transcript**，与主对话历史分离。每条消息链式引用上一条的 UUID：

```typescript
// Claude Code: Sidechain 记录
let lastRecordedUuid = initialMessages[initialMessages.length - 1]?.uuid

for await (const message of query({...})) {
    outputMessages.push(message)

    // 每条消息记录到独立的 sidechain（不是主对话）
    if (message.type === 'assistant' || message.type === 'user') {
        await recordSidechainTranscript([message], agentId, lastRecordedUuid)
        lastRecordedUuid = message.uuid
    }
}

// 用途：
// 1. 主对话不被子 agent 中间过程"污染"
// 2. 完整执行历史可用于审计
// 3. 支持 resume（从 sidechain 恢复状态）
```

#### IronClaw 的适配实现

IronClaw 提供了**双存储**实现，比 Claude Code 更灵活：

```go
// 统一接口
type SidechainStore interface {
    Append(entry SidechainEntry) error
    GetByAgent(agentID string) ([]SidechainEntry, error)
    GetByChain(chainID string) ([]SidechainEntry, error)
}

// 实现一：文件存储（调试友好）
// 每个 agent 一个目录，每个条目一个 JSON 文件
// ~/.ironclaw/sidechains/agent-abc123/20260402T150405_entry1.json
store, _ := NewFileSidechainStore("~/.ironclaw/sidechains/")

// 实现二：SQLite 存储（生产环境）
// 使用已有的 store.DB，通过迁移 015_sidechain_entries.sql 建表
store := NewSQLiteSidechainStore(db)

// 使用方式（两种存储接口一致）
recorder := NewSidechainRecorder(agentID, parentID, chainID, store)
recorder.RecordMessage("user", "问题")
recorder.RecordToolCall("bash", `{"command":"ls"}`)
recorder.RecordToolResult("bash", "file1.go\nfile2.go", "success")
recorder.RecordStatus("completed", "代理完成")

// 按调用链查询整个执行树
entries, _ := store.GetByChain(chainID) // 获取同链所有 agent 的记录
```

**IronClaw 独有**：`GetByChain()` 支持按调用链查询——可以看到父 agent 和所有子 agent 的完整执行时间线。Claude Code 的 sidechain 是 per-agent 的，没有跨 agent 的链式查询能力。

---

### 3.7 后台异步执行

#### Claude Code 的实现

Claude Code 的后台 agent 有三个关键的隔离行为：

```typescript
// 1. setAppState 是 no-op — 不会改变父级 UI 状态
setAppState: overrides?.shareSetAppState ? parentContext.setAppState : () => {}

// 2. shouldAvoidPermissionPrompts = true — 不弹出权限框
shouldAvoidPermissionPrompts: true

// 3. 完成时通过 task notification 通知
setAppStateForTasks  // 到达 root store，注册后台任务
// 父级退出时，通过 root store 清理后台 bash tasks（防止僵尸进程）
```

#### IronClaw 的适配实现

Go 的 goroutine 让这个实现更加自然：

```go
// BackgroundManager — 管理所有后台 agent
type BackgroundManager struct {
    agents   map[string]*BackgroundAgent
    notifyCh chan AgentStatus  // 缓冲 64 条，聚合所有后台 agent 通知
}

// 启动 — 立即返回 ID（fire-and-forget）
agentID := bgManager.Spawn(ctx, spec, func(bgCtx context.Context) (*AgentResult, error) {
    // 在独立 goroutine 中执行
    return executeAgentWork(bgCtx)
})

// 三种查询方式
result, done := bgManager.GetResult(agentID) // 非阻塞
result, err := bgManager.Wait(ctx, agentID)  // 阻塞
bgManager.Cancel(agentID)                    // 取消

// 聚合通知（所有后台 agent 的状态变化）
for status := range bgManager.NotifyCh() {
    // status: {AgentID, State(running/completed/failed/cancelled), UpdatedAt}
}

// 清理已完成的记录
removed := bgManager.Cleanup()
```

**IronClaw 扩展**：`Cleanup()` 方法是 Claude Code 没有的——Claude Code 依赖 root store 清理，IronClaw 可以显式批量清理已完成的后台 agent 记录。

---

### 3.8 多执行后端

#### Claude Code 的实现

Claude Code 支持三种后端，面向桌面终端场景：

```typescript
// InProcess — 共享堆，零开销（默认）
// Tmux — 新终端会话，继承 27 个环境变量 + CLI flags
const cmd = `${buildInheritedEnvVars()} ${getTeammateCommand()} ${buildInheritedCliFlags()}`
// tmux new-session -d -s <session-id> <cmd>

// iTerm — macOS 原生，通过 AppleScript 启动新 tab
// 继承相同的环境变量和 CLI 参数
```

后端选择逻辑：按优先级检查可用性，自动降级。

#### IronClaw 的适配实现

IronClaw 面向服务器部署场景，用**接口抽象 + 工厂模式**实现：

```go
// 统一接口
type ExecutionBackend interface {
    Execute(ctx, config) (<-chan *AgentResult, error)  // 返回 result channel
    Available() bool                                    // 是否可用
    Name() string                                       // 后端标识
    Cleanup() error                                     // 资源清理
}

// 三种实现
InProcessBackend   // goroutine（对应 Claude Code 的 InProcess）
SubprocessBackend  // os/exec 子进程（对应 Claude Code 的 Tmux）
DockerBackend      // 容器化执行（IronClaw 独有）

// 自动降级工厂
func SelectBackend(backendType BackendType, executor) ExecutionBackend {
    switch backendType {
    case BackendDocker:
        be := NewDockerBackend("ironclaw:latest", "")
        if be.Available() { return be }
        return NewInProcessBackend(executor) // Docker 不可用 → 降级
    // ...
    }
}
```

**IronClaw 独有**：Docker 后端。Claude Code 是桌面工具不需要容器隔离，但 IronClaw 作为可部署服务，Docker 隔离（文件系统、网络、资源限制）对生产环境非常有价值。

---

### 3.9 生命周期钩子

#### Claude Code 的实现

Claude Code 在 agent frontmatter 中支持 `hooks.SubagentStart`，在 agent 启动时执行初始化逻辑。同时支持 Skills 预加载：

```typescript
// Claude Code: Agent frontmatter hooks
export type BuiltInAgentDefinition = {
  // ...
  frontmatter?: {
    skills?: string[]                // 预加载的 Skills
    mcpServers?: MCP.ServerConfig[]  // Agent 专属 MCP
    hooks?: {
      SubagentStart?: () => void     // 启动时回调
    }
  }
}

// Skill 预加载注入系统提示词
const skillsMarkdown = agent.frontmatter?.skills?.map(skillName => {
    const skill = resolveSkillName(skillName, availableSkills)
    return skill?.content
}).join('\n\n')
```

#### IronClaw 的扩展实现

IronClaw 将钩子扩展到了 **5 个生命周期点**（Claude Code 只有 1 个），并支持 YAML 配置：

```go
// 5 个钩子点
type AgentHooks struct {
    OnStart    []AgentHookFunc  // 代理启动时
    OnComplete []AgentHookFunc  // 成功完成时
    OnError    []AgentHookFunc  // 出错时
    OnTimeout  []AgentHookFunc  // 超时时
    OnToolCall []AgentHookFunc  // 每次工具调用前
}

// 两种内置钩子类型
type AgentHookEntry struct {
    Type    string `yaml:"type"`    // "log" — 结构化日志
    Message string `yaml:"message"` //        slog.Info 输出
    Command string `yaml:"command"` // "exec" — shell 命令（10s 超时）
}

// 关键设计：钩子失败不阻塞代理执行
func (r *AgentHookRunner) runHooks(ctx, phase, hooks, hctx) {
    for i, hook := range hooks {
        if err := hook(ctx, hctx); err != nil {
            slog.Warn("钩子失败", "phase", phase, "err", err)
            // 继续执行下一个钩子，不 return
        }
    }
}
```

---

## 四、未采纳的部分及原因

| Claude Code 特性 | 未采纳原因 | IronClaw 的替代方案 |
|------------------|-----------|-------------------|
| **字节级 Prompt Cache** | 需要控制 API 请求的 JSON 序列化细节，Go 标准库做不到精确的字节控制 | 应用层 `PromptCache` 去重（减少 CPU，非减少 token） |
| **iTerm Backend** | macOS 专属桌面功能，IronClaw 面向跨平台服务器 | Docker Backend（服务器场景更有价值） |
| **Skill 预加载** | IronClaw 已有独立的 `skill.Manager`，在 Runtime 层面处理 | 通过 `OnStart` 钩子在启动时注入自定义逻辑 |
| **Coordinator Mode** | Claude Code 的多实例分布式协调，IronClaw 是单进程架构 | `AgentOrchestrator` 的 DAG 调度在进程内实现类似效果 |
| **Perfetto Tracing** | 性能追踪框架，当前优先级低于功能实现 | `slog` 结构化日志 + `ChainID` 调用链追踪 |
| **Reactive Compact** | Claude Code 的有损上下文压缩，IronClaw 已有独立的 `CompressionPipeline` | 复用现有的分层压缩（tool eviction → summarize → slim → emergency） |
| **递归 Fork 检测** | Claude Code 用 `isInForkChild()` 检查消息内容中的标签 | `CheckForkDepth()` + `SubagentContext.Depth` 数值限制（更简洁） |

---

## 五、架构对比总览

### 5.1 代码量对比

| 模块 | Claude Code (TypeScript) | IronClaw (Go) |
|------|-------------------------|---------------|
| Agent 执行入口 | `runAgent.ts` — 1,397 行 | `agent_tool.go` — 423 行 |
| Fork 相关 | `forkSubagent.ts` — 211 行 + `forkedAgent.ts` — 600 行 | `fork.go` — 47 行 + `subagent_context.go` — 97 行 |
| 后端抽象 | `InProcess.ts` + `TmuxBackend.ts` + `ITerm.ts` — ~1,200 行 | `backend.go` — 130 行 |
| 调度 | `teamHelpers.ts` — 21KB | `orchestrator.go` — 153 行 |
| 权限 | `permissionSync.ts` — 26KB | `permission.go` — 115 行 |

Go 的实现显著更紧凑——这是 Go 语言"少即是多"哲学和 goroutine/channel 原语简洁性的体现。

### 5.2 测试对比

| 指标 | IronClaw |
|------|---------|
| 新增测试文件 | 14 个 |
| 新增测试用例 | 69 个（Phase 1: 11, Phase 2: 20, Phase 3: 38） |
| 总测试用例 | 93 个（含原有 24 个） |
| 全部通过 | ✅ |

### 5.3 能力矩阵

```
                    Claude Code    IronClaw(优化前)    IronClaw(优化后)
Fork Agent              ✅              ❌                  ✅
Spawn Agent             ✅              ✅                  ✅
Background Agent        ✅              ❌                  ✅
并行调度                 ✅              ❌                  ✅
DAG 依赖调度             ❌              ❌                  ✅  ← 独有
上下文隔离               ✅              ⚠️(基础)           ✅
Prompt Cache            ✅(API级)       ❌                  ✅(应用级)
Sidechain 记录          ✅              ❌                  ✅
双存储(File+SQLite)     ❌              ❌                  ✅  ← 独有
多执行后端               ✅              ❌                  ✅
Docker 后端             ❌              ❌                  ✅  ← 独有
权限分级                 ✅              ⚠️(仅approval)     ✅
危险操作黑名单           ❌              ❌                  ✅  ← 独有
生命周期钩子             ⚠️(1个)        ❌                  ✅(5个)
Per-Agent MCP           ✅              ❌                  ✅
链式查询(GetByChain)    ❌              ❌                  ✅  ← 独有
```

---

## 六、总结

### 学到了什么

1. **Fork 优先于 Spawn** — 大多数子任务需要理解当前上下文，从零开始是浪费
2. **隔离是精确的，不是全有或全无** — 每个资源的共享/隔离策略都应该是有意识的选择
3. **权限要分级** — bypass/accept_edits/bubble 覆盖了从"完全信任"到"每次审批"的完整光谱
4. **执行历史要独立** — 子 agent 的中间过程不应该污染主对话
5. **后端要可插拔** — 今天用 goroutine，明天可能需要容器隔离
6. **钩子要非阻塞** — 可观测性不应该影响可靠性

### 超越了什么

1. **DAG 依赖调度** — Claude Code 没有显式的任务依赖图
2. **双存储 Sidechain** — File + SQLite，适应不同场景
3. **Docker 后端** — 面向服务器部署的容器隔离
4. **危险操作黑名单** — 显式的安全边界
5. **5 个生命周期钩子** — 比 Claude Code 更完整的可观测性
6. **链式执行查询** — 可以追踪整个调用链的时间线

### 一句话总结

> Claude Code 教会了 IronClaw **"如何设计一个生产级的多代理协作系统"**，
> 而 Go 的并发原语让 IronClaw 用**更少的代码**实现了**更灵活的架构**。
