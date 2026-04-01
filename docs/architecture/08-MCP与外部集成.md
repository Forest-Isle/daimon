# 08 - MCP 与外部集成

## 文件结构

```
internal/mcp/
├── adapter.go    # MCPToolAdapter — MCP 工具→Tool 接口
└── manager.go    # Manager — MCP 服务器生命周期管理
```

## 一、MCP 协议概述

MCP (Model Context Protocol) 是一个标准化的 AI 工具通信协议。IronClaw 作为 **MCP Client** 连接外部 MCP Server。

```
┌──────────────┐     stdio      ┌──────────────┐
│  IronClaw    │◄──────────────▶│  MCP Server  │
│  (Client)    │  JSON-RPC 2.0  │  (子进程)    │
└──────────────┘                └──────────────┘
```

## 二、Manager（服务器管理）

```go
type Manager struct {
    clients map[string]client.MCPClient  // 服务器名 → 客户端
    mu      sync.RWMutex
}
```

### 启动流程

```
StartServers(ctx, servers, registry)
    │
    └── 对每个服务器配置:
        │
        ├── 1. client.NewStdioMCPClient(command, env, args)
        │       启动子进程，建立 stdio 通信
        │
        ├── 2. client.Initialize()
        │       MCP 握手协议
        │
        ├── 3. client.ListTools()
        │       发现服务器提供的工具
        │
        └── 4. 对每个工具:
            └── registry.Register(MCPToolAdapter{...})
                命名: mcp_{server}_{tool}
```

### 热重载（SyncServers）

```
SyncServers(ctx, desired, registry)
    │
    ├── 对比 desired vs current:
    │
    ├── 新增的服务器:
    │   └── startServer() → 注册工具
    │
    ├── 不变的服务器:
    │   └── 跳过
    │
    └── 移除的服务器:
        ├── client.Close()
        └── registry.UnregisterByPrefix("mcp_{server}_")
```

热重载来源：
1. **配置文件**：`tools.mcp.servers` 中定义
2. **用户目录**：`~/.IronClaw/mcp/` 中的 YAML 文件
3. **轮询**：Gateway 每 30 秒扫描用户目录

## 三、Adapter（工具适配）

```go
type MCPToolAdapter struct {
    client       client.MCPClient
    serverName   string
    tool         mcp.Tool
    approval     bool
}

// 适配为 Tool 接口
func (a *MCPToolAdapter) Name() string {
    return "mcp_" + a.serverName + "_" + a.tool.Name
}
func (a *MCPToolAdapter) Description() string { return a.tool.Description }
func (a *MCPToolAdapter) InputSchema() map[string]any { return a.tool.InputSchema }
func (a *MCPToolAdapter) Execute(ctx, input) (Result, error) {
    // 调用 MCP server 执行工具
    return a.client.CallTool(ctx, a.tool.Name, input)
}
func (a *MCPToolAdapter) RequiresApproval() bool { return a.approval }
```

## 四、配置

```yaml
tools:
  mcp:
    servers:
      filesystem:
        command: "npx"
        args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
        env:
          NODE_ENV: "production"
        requires_approval: true

      github:
        command: "npx"
        args: ["-y", "@modelcontextprotocol/server-github"]
        env:
          GITHUB_TOKEN: "${GITHUB_TOKEN}"
```

用户目录配置（`~/.IronClaw/mcp/my-server.yaml`）：
```yaml
command: "my-mcp-server"
args: ["--port", "3000"]
requires_approval: false
```

## 五、关键设计

1. **stdio 通信**：通过子进程 stdin/stdout 通信，无需网络
2. **工具发现**：自动发现 MCP server 提供的工具
3. **统一注册**：MCP 工具注册到同一个 Registry，对 Agent 透明
4. **前缀命名**：`mcp_{server}_{tool}` 避免命名冲突
5. **批量管理**：`UnregisterByPrefix` 支持服务器级别的工具移除
6. **热重载**：运行时添加/移除 MCP 服务器无需重启
7. **配置优先级**：项目配置 > 用户目录配置
