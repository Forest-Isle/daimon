# 06 - Tool 工具系统

## 文件结构

```
internal/tool/
├── tool.go            # Tool 接口 + Registry
├── bash.go            # Bash 命令执行工具
├── file.go            # 文件读写工具
├── http.go            # HTTP 请求工具
├── browser.go         # 浏览器工具（预留）
├── policy.go          # 安全策略（命令拦截）
├── skill.go           # 技能读取工具 (read_skill)
└── memory_manage.go   # 记忆管理工具 (memory_manage)
```

## 一、核心接口

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any      // JSON Schema
    Execute(ctx context.Context, input []byte) (Result, error)
    RequiresApproval() bool
}

type Result struct {
    Output string
    Error  string
}
```

## 二、Registry（注册表）

```go
type Registry struct {
    mu    sync.RWMutex
    tools map[string]Tool
}

func (r *Registry) Register(t Tool)
func (r *Registry) Get(name string) (Tool, error)
func (r *Registry) All() []Tool
func (r *Registry) UnregisterByPrefix(prefix string) []string  // MCP 热重载用
```

## 三、内置工具

### bash — 命令执行

```
名称: bash
输入: { "command": "ls -la" }
特性:
    ├── 超时控制 (cfg.Tools.Bash.Timeout, 默认 30s)
    ├── 安全策略检查 (policy.go)
    ├── 可配置审批 (requires_approval)
    └── 输出截断 (防止 token 爆炸)
```

安全策略（`policy.go`）：
```go
type Policy struct {
    blockedCommands []string  // 黑名单命令
}

// 检查命令是否被阻止
func (p *Policy) IsBlocked(command string) bool
```

默认黑名单示例：`rm -rf /`, `mkfs`, `dd`, `:(){ :|:& };:`

### file — 文件操作

```
名称: file
输入: { "action": "read|write|list", "path": "/tmp/test.txt", "content": "..." }
特性:
    ├── read  — 读取文件内容
    ├── write — 写入文件（创建/覆盖）
    ├── list  — 列出目录内容
    └── 可配置审批
```

### http — HTTP 请求

```
名称: http
输入: { "method": "GET", "url": "https://api.example.com", "headers": {...}, "body": "..." }
特性:
    ├── 支持 GET/POST/PUT/DELETE
    ├── 超时控制 (cfg.Tools.HTTP.Timeout, 默认 30s)
    ├── 响应截断
    └── 可配置审批
```

### read_skill — 技能内容读取

```
名称: read_skill
输入: { "name": "skill_name" }
用途: Agent 在系统提示中看到技能元数据后，
      通过此工具按需加载完整技能内容
      （渐进式披露模式 / Progressive Disclosure）
```

### memory_manage — 记忆管理

```
名称: memory_manage
输入: { "action": "search|save|update|delete|list", ... }
用途: Agent 主动管理记忆系统
      ├── search — 搜索记忆
      ├── save   — 保存新记忆
      ├── update — 更新现有记忆
      ├── delete — 删除记忆
      └── list   — 列出某作用域的记忆
```

## 四、MCP 工具集成

通过 MCP 协议动态注册的工具：

```
internal/mcp/
├── adapter.go    # MCP 工具 → Tool 接口适配
└── manager.go    # MCP 服务器管理

MCP 工具命名: mcp_{server}_{tool}
例如: mcp_filesystem_readFile
```

### MCP Manager

```
StartServers(ctx, servers, registry)
    │
    ├── 对每个配置的 MCP 服务器:
    │   ├── client.NewStdioMCPClient()   # 启动子进程
    │   ├── MCP 握手 (Initialize)
    │   ├── 发现工具 (ListTools)
    │   └── 适配并注册到 Registry
    │
    └── SyncServers(ctx, desired, registry)  # 热重载
        ├── 新增服务器 → 启动 + 注册
        ├── 已有服务器 → 跳过
        └── 已移除 → 关闭 + 反注册 (UnregisterByPrefix)
```

### MCP Adapter

```go
// 将 MCP 工具适配为 Tool 接口
type MCPToolAdapter struct {
    client       client.MCPClient
    serverName   string
    tool         mcp.Tool
    approval     bool
}

func (a *MCPToolAdapter) Name() string {
    return "mcp_" + a.serverName + "_" + a.tool.Name
}
```

## 五、Agent 子工具（agent_tool.go）

每个注册的子 Agent 也作为 Tool 注册到 Registry：

```
名称: agent_{agent_name}
输入: { "task": "要执行的任务描述" }
执行: 启动子 Agent 运行时处理任务
```

## 六、工具执行流程

```
Agent Loop 中工具调用
    │
    ├── 1. registry.Get(toolName)        # 查找工具
    │
    ├── 2. tool.RequiresApproval()?
    │   └── YES → approvalFunc(ctx, ch, target, toolName, input)
    │       ├── Channel 支持 ApprovalSender → 交互式审批
    │       └── Channel 不支持 → 自动批准
    │
    ├── 3. policy.IsBlocked(command)?     # (仅 bash)
    │   └── YES → 返回错误
    │
    ├── 4. tool.Execute(ctx, input)
    │   ├── 成功 → Result{Output: "..."}
    │   └── 失败 → Result{Error: "..."}
    │
    ├── 5. 压缩输出 (if compressor != nil)
    │
    └── 6. 记录日志 (session.LogToolExecution)
```

## 设计亮点

1. **统一接口**：所有工具（内置/MCP/Agent）实现相同接口
2. **安全层**：Policy 黑名单 + 审批机制 + 超时控制
3. **热重载**：MCP 工具支持运行时添加/移除
4. **渐进披露**：Skill 元数据在提示词中，内容按需加载
5. **前缀管理**：`UnregisterByPrefix` 支持批量反注册 MCP 工具
