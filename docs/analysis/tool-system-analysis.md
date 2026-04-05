# IronClaw 工具系统 — 完整实现分析

## 目录

- [1. 架构总览](#1-架构总览)
- [2. 核心接口：Tool + Registry](#2-核心接口tool--registry)
- [3. 权限系统：Policy + PermissionEngine](#3-权限系统policy--permissionengine)
- [4. BashTool：Shell 命令执行](#4-bashtoolshell-命令执行)
- [5. FileTool：文件读写](#5-filetool文件读写)
- [6. HTTPTool：HTTP 请求](#6-httptoolhttp-请求)
- [7. BrowserTool：浏览器自动化（桩）](#7-browsertool浏览器自动化桩)
- [8. SkillTool：渐进式技能加载](#8-skilltool渐进式技能加载)
- [9. MemoryManageTool：记忆管理](#9-memorymanage-tool记忆管理)
- [10. ResultStore：大结果持久化](#10-resultstore大结果持久化)
- [11. 推荐阅读顺序](#11-推荐阅读顺序)

---

## 1. 架构总览

```
┌───────────────────────────────────────────┐
│              Tool Registry                │
│  (线程安全 map[string]Tool)                │
├───────────┬───────┬───────┬───────┬───────┤
│ BashTool  │ File  │ HTTP  │Skill  │Memory │
│           │ Tool  │ Tool  │ Tool  │Manage │
│           │       │       │       │ Tool  │
├───────────┴───────┴───────┴───────┴───────┤
│              MCP Tools                    │
│  mcp_{server}_{tool}（动态注册）           │
├───────────────────────────────────────────┤
│              Agent Tools                  │
│  agent_{name}（子 Agent 包装）             │
└───────────────────────────────────────────┘
         │                        │
         ▼                        ▼
┌─────────────────┐    ┌──────────────────┐
│PermissionEngine │    │   ResultStore    │
│ (规则匹配)       │    │ (大结果磁盘缓存)  │
└─────────────────┘    └──────────────────┘
```

---

## 2. 核心接口：Tool + Registry

📄 **文件**: `internal/tool/tool.go`

### 2.1 Tool 接口

```go
type Tool interface {
    Name() string                                           // 工具名称
    Description() string                                    // 描述（供 LLM 理解）
    InputSchema() map[string]any                            // JSON Schema 定义输入格式
    Execute(ctx context.Context, input []byte) (Result, error) // 执行
    RequiresApproval() bool                                 // 是否需要用户审批
}
```

所有工具（内置、MCP、Agent）必须实现此接口。

### 2.2 可选接口：能力声明

```go
// 旧接口（向后兼容）
type ReadOnlyTool interface {
    IsReadOnly() bool
}

// 新接口（推荐）
type CapableTool interface {
    Capabilities() ToolCapabilities
}

type ToolCapabilities struct {
    IsReadOnly      bool    // 只读，无副作用
    IsDestructive   bool    // 可能造成不可逆变更
    RequiresNetwork bool    // 需要网络访问
    ApprovalMode    string  // "never" | "always" | "auto"
}
```

`GetCapabilities(tool)` 辅助函数统一处理两种接口的兼容性。

### 2.3 Result 类型

```go
type Result struct {
    Output    string         `json:"output"`              // 主输出
    Error     string         `json:"error,omitempty"`     // 错误信息
    Type      ResultType     `json:"type,omitempty"`      // text | image | file | reference
    FilePath  string         `json:"file_path,omitempty"` // 关联文件路径
    IsPartial bool           `json:"is_partial,omitempty"`// 输出是否被截断
    Metadata  map[string]any `json:"metadata,omitempty"`  // 扩展元数据
}
```

### 2.4 Registry

```go
type Registry struct {
    mu    sync.RWMutex
    tools map[string]Tool
}

func NewRegistry() *Registry
func (r *Registry) Register(t Tool)                          // 注册工具
func (r *Registry) Get(name string) (Tool, error)            // 获取工具
func (r *Registry) All() []Tool                              // 列出所有工具
func (r *Registry) UnregisterByPrefix(prefix string) []string // 按前缀批量注销
```

`UnregisterByPrefix` 在 MCP 服务重新加载时使用，清理旧的 `mcp_{server}_*` 工具。

---

## 3. 权限系统：Policy + PermissionEngine

### 3.1 旧版 Policy（命令黑名单）

📄 **文件**: `internal/tool/policy.go`

```go
type Policy struct {
    blockedCommands []string  // 被禁止的命令列表
}

func NewPolicy(blockedCommands []string) *Policy
func (p *Policy) CheckBashCommand(cmd string) string  // 返回错误信息或 ""
```

简单的字符串匹配黑名单，作为向后兼容的 fallback。

### 3.2 新版 PermissionEngine（规则引擎）

📄 **文件**: `internal/tool/permissions.go`

```go
type PermissionRule struct {
    Tool        string `yaml:"tool"`         // 工具名模式："bash", "file*", "*"
    Pattern     string `yaml:"pattern"`      // 命令模式："git *", "rm -rf *"
    PathPattern string `yaml:"path_pattern"` // 文件路径模式："/etc/*"
    Action      string `yaml:"action"`       // "allow" | "deny" | "ask"
}

type PermissionEngine struct {
    rules      []PermissionRule
    defaultAct PermissionAction
    legacy     *Policy  // 兼容旧版
}
```

**评估流程**：

```
PermissionEngine.Evaluate(toolName, input, capabilities)
     │
     ├─ 如果无规则 → 回退到旧版 Policy
     │
     ├─ 从 input 提取 command（bash 工具）或 filePath（file 工具）
     │
     ├─ 从上到下遍历规则，第一个匹配的规则生效：
     │   ├─ matchToolPattern(rule.Tool, toolName)
     │   ├─ matchGlob(rule.Pattern, command)
     │   └─ matchGlob(rule.PathPattern, filePath)
     │
     ├─ 如果工具 IsDestructive 且无规则匹配 → 默认 "ask"
     │
     └─ 否则使用配置的默认动作
```

**规则合并**：`MergeRules(projectRules, globalRules)` — 项目级规则优先于全局规则。

---

## 4. BashTool：Shell 命令执行

📄 **文件**: `internal/tool/bash.go`

```go
const maxOutputSize = 64 * 1024  // 64KB 输出限制

type BashTool struct {
    timeout  time.Duration
    approval bool
    policy   *Policy
}
```

**输入 Schema**：
```json
{
  "type": "object",
  "properties": {
    "command": { "type": "string", "description": "The shell command to execute" }
  },
  "required": ["command"]
}
```

**执行流程**：
1. 解析 JSON 提取 `command`
2. 检查 Policy 黑名单
3. `exec.CommandContext("sh", "-c", command)` 带超时执行
4. 捕获 stdout + stderr
5. 输出超过 64KB → 截断
6. 超时 → 返回错误

**Capabilities**：`IsDestructive: true`, `ApprovalMode: "always"`

---

## 5. FileTool：文件读写

📄 **文件**: `internal/tool/file.go`

```go
type FileTool struct {
    approval bool
}

type fileInput struct {
    Action  string  // "read" | "write" | "list"
    Path    string
    Content string  // write 时使用
}
```

**支持操作**：

| Action | 说明 |
|--------|------|
| `read` | 读取文件内容 |
| `write` | 写入/创建文件（自动创建父目录） |
| `list` | 列出目录内容 |

**Capabilities**：`IsReadOnly: false`, `IsDestructive: false`, `ApprovalMode: "auto"`

---

## 6. HTTPTool：HTTP 请求

📄 **文件**: `internal/tool/http.go`

```go
type HTTPTool struct {
    client   *http.Client
    approval bool
}

type httpInput struct {
    Method  string
    URL     string
    Headers map[string]string
    Body    string
}
```

**支持方法**：GET, POST, PUT, DELETE, PATCH

**响应格式**：
```
Output: "HTTP 200 OK\n\n{response body}"
Metadata: {"status_code": 200, "content_type": "application/json"}
```

**Capabilities**：`RequiresNetwork: true`, `ApprovalMode: "auto"`

---

## 7. BrowserTool：浏览器自动化（桩）

📄 **文件**: `internal/tool/browser.go`

当前为桩实现，Execute 返回 "not yet implemented"。

**Capabilities**：`IsReadOnly: true`, `RequiresNetwork: true`

---

## 8. SkillTool：渐进式技能加载

📄 **文件**: `internal/tool/skill.go`

```go
type SkillContentProvider interface {
    GetContent(name string) (string, error)
    ListNames() []string
}

type SkillTool struct {
    provider SkillContentProvider
}

type skillInput struct {
    Action string  // "read" | "list"
    Name   string  // 技能名（read 时使用）
}
```

**渐进式披露（Progressive Disclosure）模式**：
1. 系统提示中只包含技能元数据（名称、描述、标签）
2. Agent 识别需要某技能时，调用 `read_skill` 工具
3. 工具返回完整的技能指令（Markdown 内容）
4. Agent 按技能指令执行

**Capabilities**：`IsReadOnly: true`, `ApprovalMode: "never"`

---

## 9. MemoryManageTool：记忆管理

📄 **文件**: `internal/tool/memory_manage.go`

```go
type MemoryManageTool struct {
    store   memory.Store
    db      *sql.DB
    baseDir string
}

type memoryManageInput struct {
    Action        string     // "forget" | "list" | "protect" | "retention"
    Query         string
    Sensitivity   string     // "public" | "private" | "secret"
    MemoryType    string     // "episodic" | "semantic" | "procedural"
    RetentionDays float64
    ConfirmIDs    []string   // 确认删除的 ID 列表
}
```

**支持操作**：

| Action | 说明 |
|--------|------|
| `forget` | 搜索并删除记忆（两步：搜索 → 确认 → 删除） |
| `list` | 显示最近记忆及元数据 |
| `protect` | 设置记忆的隐私级别 |
| `retention` | 配置按类型的保留策略 |

所有 delete 和 protect 操作都会写入 `memory_audit_log` 审计日志。

**Capabilities**：`RequiresApproval: true`

---

## 10. ResultStore：大结果持久化

📄 **文件**: `internal/tool/resultstore.go`

```go
type ResultStore struct {
    cacheDir       string  // 缓存目录
    thresholdBytes int     // 触发持久化的阈值
    previewChars   int     // 预览截断大小
    ttlHours       int     // 缓存 TTL
}

type StoredResult struct {
    Preview  string  // 截断预览（保留在上下文中）
    DiskPath string  // 完整输出的磁盘路径
    FullSize int     // 完整输出大小
}
```

**工作原理**：
1. 工具输出超过 `thresholdBytes` → 触发持久化
2. 完整输出写入 `{cacheDir}/{sessionID}/{toolUseID}.txt`
3. 上下文中只保留截断预览（在行边界截断，保持可读性）
4. TTL 过期自动清理

**目的**：防止大工具输出撑爆 LLM 上下文窗口。

---

## 11. 推荐阅读顺序

### 第一层：核心抽象

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 1 | `tool.go` | `Tool` 接口, `Result`, `Registry`, `ToolCapabilities` |
| 2 | `policy.go` | 旧版安全策略 |
| 3 | `permissions.go` | `PermissionEngine`, 规则匹配逻辑 |

### 第二层：内置工具

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 4 | `bash.go` | Shell 执行 + 输出截断 |
| 5 | `file.go` | 文件 CRUD |
| 6 | `http.go` | HTTP 请求 |
| 7 | `skill.go` | 渐进式披露模式 |
| 8 | `memory_manage.go` | 记忆管理操作 |

### 第三层：辅助系统

| 顺序 | 文件 | 重点关注 |
|------|------|---------|
| 9 | `resultstore.go` | 大结果缓存策略 |
| 10 | `browser.go` | 桩实现（了解接口即可） |
