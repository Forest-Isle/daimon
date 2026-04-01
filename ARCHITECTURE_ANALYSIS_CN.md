# IronClaw 系统架构综合分析

## 项目概述

IronClaw 是一个本地优先的 AI 代理运行时系统，采用 Go 语言实现。它将 Claude AI 与多种工具（bash、文件、HTTP、浏览器）相连接，通过多个通道（Telegram、TUI）暴露这些功能，所有数据持久化在 SQLite 数据库中。核心特点是支持两种智能体模式（simple 和 cognitive）以及完整的强化学习（RL）系统。

---

## 一、通道系统（Channel System）

### 架构概览

通道系统是 IronClaw 与外部通信平台的适配器层，负责：
1. **消息归一化** - 将不同平台的消息转换为统一的 InboundMessage 格式
2. **流式消息** - 支持实时流式输出（类似 ChatGPT 逐字输出）
3. **交互式决策** - 支持工具批准和重规划决策的用户交互

### 核心接口（channel/channel.go）

```
Channel 接口:
├─ Name() string                              // 通道名称标识
├─ Start(ctx, handler)                        // 启动通道监听
├─ Send(ctx, msg OutboundMessage)             // 发送消息
├─ SendStreaming(ctx, target) StreamUpdater  // 流式发送
└─ Stop(ctx)                                  // 停止通道

StreamUpdater 接口:
├─ Update(text)                               // 增量更新
└─ Finish(text)                               // 完成流式传输

可选接口:
├─ ApprovalSender                             // 工具执行批准
└─ ReflectionSender                           // 重规划决策
```

**消息模型**:
- `InboundMessage` - 用户输入，包含 Channel/ChannelID/UserID/Text/CallbackData
- `OutboundMessage` - 代理输出，支持 Markdown/HTML 格式和内联键盘
- `MessageTarget` - 消息目标标识 (channel + channelID)

### 1.1 Telegram 通道实现（channel/telegram/）

**适配器特点**:
1. **双向通信** - 基于 getUpdates 轮询和 inline keyboard 回调
2. **消息编辑** - 流式更新通过 Telegram 的 editMessage API 实现（rate-limited）
3. **批准工作流** - sync.Map 存储待批准的工具，callback 路由响应

**核心流程**:
```
收到更新 → handleUpdate()
├─ 如果是 CallbackQuery → handleCallback()
│  ├─ 解析 "action:key" 格式
│  ├─ 路由到 pendingApprovals 或 pendingReflections
│  └─ 写入响应通道（非阻塞）
├─ 如果是 Message → 异步转发
│  └─ go a.handler(ctx, InboundMessage{...})
└─ 关键：异步处理避免死锁（callback 不会被阻塞的 handler 卡住）
```

**Telegram 特有机制**:
- 流式消息通过 `streamUpdater` 管理 (Min 1s 更新间隔，最大 4096 字符)
- 批准请求返回 inline keyboard，用户选择后触发 callback
- 反射请求 (replan decision) 类似，支持 continue/adjust/abort 三选一

**代码亮点**:
```go
// 处理 callback 的无阻塞设计
select {
case ch <- decision:
default:  // 不阻塞，即使没有接收端
}

// 反射请求使用 Unix nano 时间戳作为 key 避免冲突
key := fmt.Sprintf("reflect_%s_%d", target.ChannelID, time.Now().UnixNano())
```

### 1.2 TUI 通道实现（channel/tui/）

**架构设计**:
基于 Bubble Tea（Charm 生态），实现完整的终端 UI：

**三层结构**:
1. **Adapter** - 处理与 gateway 的通信和模型交互
2. **Model** - Bubble Tea 的主 Model，管理 UI 状态和事件
3. **Messages** - tea.Msg 自定义消息类型（ agentResponseMsg, approvalRequestMsg等）

**事件驱动的交互**:
```
用户输入 (Enter) 
  ↓ modelWrapper 捕获
  ↓ 写入 userInputCh
  ↓ adapter 的 routeInput 读取
  ↓ 转换为 InboundMessage 并转发到 handler

Agent 响应
  ↓ adapter.Send() 发送 agentResponseMsg
  ↓ tea.Program.Send()
  ↓ Model.Update() 接收
  ↓ viewport 更新并重新渲染
```

**三种模式**:
1. `modeChat` - 正常聊天，键盘输入到 textarea
2. `modeApproval` - 工具批准对话，y/n/a 按键拦截
3. `modeReflection` - 重规划决策，1/2/3 或 c/a/x 按键拦截

**流式消息处理**:
- `tuiStreamUpdater` 通过 pump() 定期向 Bubble Tea loop 推送更新（50ms 节流）
- 使用 atomic.Value 存储最新文本，避免锁竞争

**样式系统** (styles.go):
- Lipgloss 定义全局样式（header、user/agent 标签、approval/reflection box）
- Glamour 集成进行 Markdown 渲染为 ANSI 彩色输出

**设计亮点**:
```go
// modelWrapper 扩展 Model，在 Update 时拦截 Enter 按键
type modelWrapper struct {
    *Model
    userInputCh chan string
}

// 这种方式避免修改原 Model 代码，保持关注点分离
func (w *modelWrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if keyMsg, ok := msg.(tea.KeyMsg); 
       ok && w.mode == modeChat && keyMsg.Type == tea.KeyEnter {
        // 捕获并转发用户输入
        w.userInputCh <- w.textarea.Value()
    }
    // 继续传递给原 Model
    return w.Model.Update(msg)
}
```

---

## 二、工具系统（Tool System）

### 2.1 工具接口与注册表（tool/tool.go）

**核心设计 - 工具如同 AI 的"手"**:
```
Tool 接口:
├─ Name() string                    // 工具标识（bash, file, http, browser）
├─ Description() string             // 用于 LLM 的自然语言描述
├─ InputSchema() map[string]any    // JSON Schema（LLM 遵循的参数约束）
├─ Execute(ctx, input) Result      // 执行工具，返回输出或错误
└─ RequiresApproval() bool         // 是否需要用户确认
```

**Registry 设计**:
- 线程安全的 sync.RWMutex 保护
- 支持 Register/Get/All/UnregisterByPrefix 操作
- 方便 MCP 工具动态注册和卸载

### 2.2 内置工具实现

#### Bash 工具 (tool/bash.go)
```
执行流程:
输入 JSON: {"command": "ls -la"}
    ↓ 解析
    ↓ 检查安全策略 (blockedCommands)
    ↓ 使用 exec.CommandContext 执行
    ↓ 采集 stdout + stderr
    ↓ 超时控制（默认 30s）
    ↓ 输出截断（最大 64KB）
    ↓ 返回 Result{Output: "...", Error: "..."}

特性:
- 最大输出 64KB（防止 token 爆炸）
- 超时处理（context.DeadlineExceeded）
- 策略检查拦截危险命令
```

#### 文件工具 (tool/file.go)
```
支持三种操作:
1. read   → os.ReadFile (输出截断 64KB)
2. write  → os.WriteFile (自动创建目录)
3. list   → os.ReadDir (列出目录内容)

特点:
- 操作前自动创建父目录
- 统一的错误处理
- 输出格式统一
```

#### HTTP 工具 (tool/http.go)
```
支持 GET/POST/PUT/DELETE/PATCH
输入 JSON: {
    "method": "POST",
    "url": "https://api.example.com",
    "headers": {"Content-Type": "application/json"},
    "body": "{...}"
}

特点:
- 超时控制（来自外部配置）
- 响应体限制 (64KB)
- 完整的 HTTP 状态码和头部支持
- 错误响应也作为 Output 返回
```

#### 浏览器工具 (tool/browser.go)
- 当前是 stub 实现，总是返回 "not yet implemented"
- 为未来扩展预留接口

### 2.3 特殊工具

#### 读技能工具 (tool/skill.go)
- **Progressive Disclosure 设计** - 系统提示中只显示技能名称，LLM 可通过该工具查看完整说明
- 支持 read（获取技能详情）和 list（列出所有可用技能）
- 避免系统提示过于冗长

#### 内存管理工具 (tool/memory_manage.go)
```
操作:
- forget   → 删除记忆（支持搜索或直接确认ID列表删除）
- list     → 搜索和查看记忆
- protect  → 设置记忆敏感度（public/private/secret）
- retention → 配置记忆保留策略

与内存系统深度集成:
- 通过 memory.Store 接口调用
- 审计日志跟踪（logAudit）
- 支持与 LLM 交互（搜索 → 确认 → 删除流程）
```

### 2.4 安全策略系统 (tool/policy.go)

```go
Policy 结构:
├─ blockedCommands []string  // 被禁止的命令关键词

CheckBashCommand(cmd) 函数:
- 遍历 blockedCommands 检查 substring match
- 返回错误信息或空字符串
```

**使用场景**:
- 阻止 `rm -rf /` 等危险操作
- 防止访问敏感文件 (`/etc/shadow`)
- 限制 shell 元字符 (`; rm`, `| nc`, 等)

---

## 三、知识库系统（Knowledge System）

### 3.1 核心架构 (knowledge/store.go)

**混合搜索引擎设计** - BM25 + 向量检索 + RRF 融合：

```
SQLiteKnowledgeBase:
├─ db: *store.DB              // SQLite 连接
├─ embedder: EmbeddingProvider // 向量化（可选）
├─ fts5Available: bool        // FTS5 全文索引可用性探测
├─ pipeline: *IngestPipeline  // 文档摄入管道
└─ searchCache: *KnowledgeSearchCache  // 搜索结果缓存

搜索流程 (Search):
输入: KnowledgeQuery{Text, Embedding, SourceType, Limit}
    ↓
检查缓存 (可选) → 命中返回
    ↓
向量化查询 (如需要) → 调用 embedder.Embed()
    ↓
并行执行:
  ├─ vectorSearch()     → 向量相似度 Top-K
  └─ fts5Search() 或 likeSearch()  → 全文 BM25/LIKE

RRF (Reciprocal Rank Fusion):
    score = bm25_score * 0.4 + vector_score * 0.6
    (可配置权重，默认 0.4/0.6)

结果去重 + 排序 → Top-K 返回
```

**数据库表结构**:
```sql
kb_chunks: 文档块存储
├─ id (PK)
├─ source_type (web/pdf/code/markdown/text)
├─ source_uri
├─ chunk_title
├─ content (实际文本)
└─ embedding (向量 blob)

kb_chunks_fts: FTS5 虚表（仅当 FTS5 可用时）
└─ 提供 BM25 全文索引
```

### 3.2 文本分块策略 (knowledge/chunk.go)

**智能分块算法** - 尽量在句子边界分割：

```go
ChunkText(text, ChunkStrategy{ChunkSize, ChunkOverlap}):
1. 目标块大小: 512 runes（可配置）
2. 重叠: 64 runes（可配置）
3. 分割策略:
   - 从 ChunkSize 位置开始
   - 在最后 20% 范围内寻找句子边界 (. ? ! \n)
   - 找到则在边界分割，否则在 ChunkSize 处强制分割
4. 跳过空块
```

**优势**:
- 保留上下文（重叠避免信息丢失）
- 尊重语义边界（在句子处分割）

### 3.3 重排序器 (knowledge/reranker.go)

**两种实现**:

1. **NoopReranker** - 直接返回原始顺序（默认快速路径）
2. **LLMReranker** - 使用 LLM 重新排序
   ```
   系统提示: "你是相关性重排器。给定查询和文档列表，..."
   用户输入: 
     QUERY: 用户问题
     DOCUMENTS:
       [0] ID: chunk_123
           内容摘要...
       [1] ID: chunk_456
           内容摘要...
   
   LLM 输出: ["chunk_456", "chunk_123"]  (JSON 数组)
   
   解析 JSON 数组并按顺序重排原结果
   ```

**应用场景**:
- 精排优化（在 top-k 的基础上进一步优化顺序）
- 可选配置（取决于是否配置 LLM completer）

### 3.4 搜索缓存 (knowledge/cache.go)

```go
KnowledgeSearchCache:
├─ 基于 SHA256(text + sourceType) 作为 key
├─ TTL 机制 (默认 5 分钟)
├─ LRU-like 驱逐 (size 限制后删除过期或最早项)
└─ 原子操作线程安全

使用场景:
- 相同查询频繁出现时命中缓存
- Invalidate() 在 Ingest 后清空缓存
```

### 3.5 摄入管道 (knowledge/ingest/)

**Ingester 接口设计** - 策略模式：
```go
Ingester 接口:
├─ CanHandle(sourceType) bool
└─ Extract(ctx, uri) (title, content, error)

内置实现:
├─ MarkdownIngester   → .md 文件
├─ CodeIngester       → .go/.py/.js 等代码文件
├─ WebIngester        → HTTP(S) URL（使用 colly 爬虫）
├─ PDFIngester        → .pdf（使用 pdfcpu）
└─ PlainTextIngester  → 通用文本文件

注册表模式:
registry.Register(ingest)
registry.Extract(ctx, uri, sourceType)
├─ 遍历已注册的 ingesters
├─ 使用首个支持该 sourceType 的 ingester
└─ 返回 (title, content, error)

源类型检测:
DetectSourceType(uri) 根据文件扩展名和 URL scheme 推断类型
```

### 3.6 知识图谱系统 (knowledge/graph/)

**图存储接口**:
```go
Graph 接口:
├─ UpsertNode()      // 创建或更新节点
├─ UpsertEdge()      // 创建或更新边
├─ Neighbors()       // 直接邻居（一跳）
├─ Traverse()        // 多跳遍历（DFS/BFS）
├─ FindNode()        // 按类型和名称查找
├─ FindByName()      // 模糊匹配
└─ AddProvenance()   // 链接信息源

数据模型:
Node: id, type, name, properties, createdAt, updatedAt
Edge: id, sourceID, targetID, type, weight, properties, validFrom, validTo
Triple: (Subject, Predicate, Object, Weight)
```

**SQLite 实现**:
- 递归 CTE (WITH RECURSIVE) 实现多跳遍历
- 支持时间有效性 (validFrom/validTo)
- 支持信息源追踪 (AddProvenance)

**关键特性**:
- **衰减机制** (graph_decay.go) - 权重随时间衰减
- **同步机制** (graph_sync.go) - 多数据源同步

---

## 四、MCP 系统（Model Context Protocol）

### 4.1 MCP 协议集成 (mcp/manager.go & adapter.go)

**架构设计** - 动态工具发现和适配：

```
Manager:
├─ clients: map[string]MCPClient  // 已连接的 MCP 服务器
└─ StartServers(ctx, config, registry)
   ├─ 遍历 config 中每个 MCP 服务器
   ├─ 对每个服务器:
   │  ├─ NewStdioMCPClient(command, env, args)
   │  ├─ Initialize() 握手
   │  ├─ ListTools() 发现可用工具
   │  └─ 为每个工具创建 ToolAdapter 并注册
   └─ 单个服务器失败不阻塞其他
```

**ToolAdapter 设计** - MCP 工具适配成 IronClaw Tool：

```go
ToolAdapter implements Tool interface:
├─ Name() 返回 "mcp_{serverName}_{toolName}"
├─ Description() 代理 MCPTool.Description
├─ InputSchema() 代理 MCPTool.InputSchema
├─ RequiresApproval() 返回配置值
└─ Execute(ctx, input)
   ├─ 解析 JSON input
   ├─ 构造 CallToolRequest
   ├─ client.CallTool(ctx, req)
   ├─ 提取响应内容
   └─ 返回 tool.Result{Output/Error}

关键特性:
- 无缝适配 - MCP 工具看起来像本地工具
- 错误处理 - 区分 IsError 错误和正常输出
- 文本提取 - 从 MCP Content 数组中提取纯文本
```

**MCP 服务器配置**:
```yaml
mcp:
  servers:
    filesystem:
      command: mcp-filesystem
      args: ["/home/user/docs"]
      env:
        SOME_VAR: value
      requires_approval: true
```

**生命周期**:
1. 启动时连接所有配置的服务器
2. 发现工具列表并注册
3. 运行时代理调用
4. 关闭时清理所有连接

---

## 五、技能系统（Skill System）

### 5.1 技能定义与加载 (skill/skill.go)

**技能文件格式** - YAML 前置 + Markdown 正文：

```markdown
---
name: 数据分析
description: 对 CSV 数据进行统计分析
version: 1.0.0
author: John Doe
tags: [analysis, data, csv]
metadata:
  openclaw:
    requires:
      env: [PYTHON_PATH]
      bins: [python3, pandas]
    primaryEnv: PYTHON_PATH
---

# 技能说明

使用 Python 分析 CSV 文件...

## 用法

```python
import pandas as pd
df = pd.read_csv('data.csv')
```
```

**加载流程**:
```
ParseSkill(path):
├─ 读取文件
├─ splitFrontmatter() 提取 YAML 前置和 Markdown 正文
├─ yaml.Unmarshal() 解析元数据到 Skill 结构
└─ 返回 Skill 指针（内容延迟加载）

Content() 方法:
├─ 首次调用触发加载（sync.Once）
├─ 缓存结果避免重复 I/O
└─ 返回错误或内容字符串
```

### 5.2 技能管理 (skill/manager.go)

**Manager 特点**:
```go
Manager:
├─ LoadBuiltin()      // 加载嵌入二进制的内置技能
├─ LoadDir(dir)       // 从目录加载技能（支持递归）
├─ All()              // 返回所有已加载技能
└─ Get(name)          // 查询单个技能

加载策略:
- SKILL.md 优先级高（放在子目录）
- 平面 .md 文件作为备选
- 重复名称时"先加载先得"原则
- 内置技能可被用户目录中的同名技能覆盖

存储:
- 嵌入 embed.FS（编译时内置）
- 用户技能从 ~/.IronClaw/skills/ 加载
```

### 5.3 系统提示中的技能集成

**Progressive Disclosure 模式**:
```
系统提示中:
  可用技能:
  - 数据分析 (v1.0)
  - 网页爬虫 (v2.1)
  
  使用 read_skill 工具获取完整说明

LLM 决策:
  1. 看到技能列表
  2. 判断是否需要用该技能
  3. 如需要，调用 read_skill 工具查看详情
  4. 理解后在后续步骤中使用
```

**优势**:
- 系统提示保持简洁（避免 token 爆炸）
- LLM 只在需要时获取完整信息
- 支持数百个技能而不影响响应速度

---

## 六、调度系统（Scheduler System）

### 6.1 基于 Cron 的定时任务 (scheduler/scheduler.go)

**架构**:
```go
Scheduler:
├─ db: *store.DB               // 任务存储
├─ cron: *cron.Cron            // Cron 引擎（支持秒级）
├─ handler: TaskHandler        // 任务执行回调
├─ pollInterval: time.Duration // 轮询数据库间隔
└─ entries: map[string]cron.EntryID  // 任务到 cron ID 的映射

生命周期:
Start(ctx):
├─ syncTasks()              // 初始同步
├─ cron.Start()             // 启动 cron 引擎
└─ go pollLoop()            // 后台轮询数据库

pollLoop():
├─ 每个 pollInterval 触发一次 syncTasks()
├─ 发现新任务 → 注册到 cron
├─ 移除已禁用任务
└─ 更新 last_run 时间戳
```

**任务定义** (task.go):
```go
Task:
├─ ID, Name                    // 任务标识
├─ CronExpr                    // Cron 表达式（支持秒）
├─ Prompt                      // 执行时的 LLM 提示
├─ Channel, ChannelID          // 目标通道
├─ Enabled                     // 启用状态
└─ CreatedAt, LastRun         // 时间戳
```

**任务执行流程**:
```
Cron 触发:
  ↓ 检查 Task.Enabled
  ↓ 调用 handler(ctx, task)
  ↓ Handler 通常将 Prompt 发送到代理
  ↓ 代理执行任务
  ↓ 更新 DB 中的 last_run
```

**设计亮点**:
- 可避免循环依赖的 SetHandler() 模式
- 数据库轮询支持动态任务管理（无需重启）
- 单次失败不影响后续轮次

---

## 七、会话系统（Session System）

### 7.1 会话生命周期 (session/manager.go)

**会话关键**:
```
Session = 一个独立的对话线程，按 channel:channelID 唯一标识

Manager 职责:
├─ Get(channel, channelID)
│  ├─ 内存缓存优先 (sync.Map)
│  ├─ 数据库查询 (存在则加载历史)
│  └─ 不存在则创建新会话
├─ Persist(session)
│  └─ 保存消息历史到数据库
└─ 支持多会话并发访问 (thread-safe)
```

**会话数据结构** (session/session.go):
```go
Session:
├─ ID                  // 唯一标识
├─ Channel, ChannelID  // 来源通道
├─ Messages []Message  // 对话历史
├─ Metadata            // 自定义元数据
├─ CreatedAt, UpdatedAt // 时间戳
└─ mu sync.Mutex       // 线程锁

Message:
├─ ID, Role            // user/assistant/system/tool_use/tool_result
├─ Content             // 文本内容
├─ ToolName, ToolInput // 工具调用元数据
└─ CreatedAt          // 时间戳
```

### 7.2 历史管理与审计 (session/history.go)

```go
LogToolExecution(ctx, db, sessionID, toolName, input, output, status, durationMs):
├─ 插入 tool_log 表
├─ 记录成功/错误/拒绝/超时
└─ 用于后续分析和 RL 训练

工具日志表:
tool_log:
├─ id (text pk)
├─ session_id (fk → sessions)
├─ tool_name (bash, file, http, ...)
├─ input/output (完整请求和响应)
├─ status (success/error/denied/timeout)
├─ duration_ms (执行时间)
└─ created_at (时间戳)
```

### 7.3 会话持久化流程

```
Agent 执行 → AddMessage() → Session.Messages 增长
    ↓
定期或显式调用 Persist()
    ↓
事务性写入:
  1. 更新 sessions.updated_at
  2. INSERT OR IGNORE messages (避免重复)
  3. 提交事务
```

**优势**:
- 内存快速，周期性持久化平衡性能和可靠性
- INSERT OR IGNORE 处理幂等性
- 事务保证一致性

---

## 八、存储系统（Store System）

### 8.1 SQLite 数据库封装 (store/sqlite.go)

**初始化流程**:
```go
Open(path):
├─ 创建数据目录
├─ sql.Open("sqlite3", dsn)
│  └─ DSN 包含:
│     ├─ _journal_mode=WAL (Write-Ahead Logging)
│     ├─ _busy_timeout=5000 (锁超时)
│     └─ _foreign_keys=on (外键约束)
├─ SetMaxOpenConns(1) // SQLite 单写入器并发模型
└─ migrate(db) // 应用所有待处理迁移

迁移系统:
├─ _migrations 表追踪已应用迁移
├─ 按文件名字母顺序应用（001_*.sql, 004_*.sql, ...）
├─ 幂等性：重复应用相同迁移被识别和跳过
└─ 错误检测：duplicate column 等被视为"已应用"
```

### 8.2 关键表结构

**核心表**:
```sql
sessions: 对话会话记录
├─ id (TEXT PK)
├─ channel (TEXT) + channel_id (TEXT) 【UNIQUE】
├─ created_at, updated_at (DATETIME)
└─ metadata (JSON TEXT)

messages: 对话消息
├─ id (TEXT PK)
├─ session_id (TEXT FK → sessions)
├─ role (CHECK: user/assistant/system/tool_use/tool_result)
├─ content, tool_name, tool_input (TEXT)
└─ created_at (DATETIME)
  【INDEX】 (session_id, created_at)

scheduled_tasks: 定时任务
├─ id, name, cron_expr, prompt
├─ channel, channel_id
├─ enabled (INTEGER)
├─ created_at, last_run (DATETIME)

tool_log: 工具执行审计
├─ id (TEXT PK)
├─ session_id (TEXT FK)
├─ tool_name, input, output (TEXT)
├─ status (CHECK: success/error/denied/timeout)
├─ duration_ms (INTEGER)
└─ created_at (DATETIME)
  【INDEX】 (session_id, created_at)
```

**知识库表**:
```sql
kb_chunks: 文档块
├─ id (TEXT PK)
├─ source_type, source_uri
├─ chunk_title, content (TEXT)
├─ embedding (BLOB - 向量)
└─ created_at (DATETIME)

kb_chunks_fts: FTS5 虚表
└─ 对 content 进行全文索引 (BM25)
```

**RL 系统表**:
```sql
rl_episodes: 训练集合
├─ id, session_id (FK)
├─ goal, complexity (simple/moderate/complex)
├─ total_reward, succeeded, subtask_count
├─ replan_count, duration_ms
└─ created_at (DATETIME)

rl_trajectories: 状态-动作-奖励轨迹
├─ id, episode_id (FK)
├─ step (INTEGER)
├─ level (bandit/ppo/dqn)
├─ state (BLOB), action (BLOB), reward (REAL)
├─ next_state (BLOB), done (INTEGER)
└─ created_at (DATETIME)

rl_rewards: 奖励分解
├─ id, episode_id (FK)
├─ reward_type (task_success/efficiency/safety/user_feedback)
├─ value, weight (REAL)
└─ created_at (DATETIME)

rl_model_checkpoints: 模型快照
├─ policy_name, version (TEXT, INTEGER)
├─ state_dim, action_dim (INTEGER)
├─ weights (BLOB)
└─ metrics (JSON TEXT)
  【UNIQUE】 (policy_name, version)

rl_bandit_arms: Bandit 统计
├─ context_hash, arm_name (TEXT)
├─ alpha, beta, pulls, total_reward (REAL)
└─ updated_at (DATETIME)
  【UNIQUE】 (context_hash, arm_name)
```

### 8.3 设计模式

**嵌入式迁移**:
```go
//go:embed migrations/*.sql
var migrationsFS embed.FS
```
- 编译时包含所有 SQL 文件
- 无需外部文件依赖
- 版本管理简化

**幂等迁移**:
- 使用 `CREATE TABLE IF NOT EXISTS`
- ALTER TABLE 失败时被捕获为"已应用"
- 升级路径安全可靠

---

## 九、用户目录系统（Userdir System）

### 9.1 ~/.IronClaw 目录管理 (userdir/userdir.go)

**目录结构**:
```
~/.IronClaw/
├─ Soul.md              → cfg.Agent.Personality (persona/style)
├─ Memory.md            → cfg.Agent.PersistentRules (长期规则)
├─ Agent.md             → cfg.Agent.SystemPrompt (代理指示)
├─ config.yaml          → 用户级配置覆盖
├─ mcp/                 → MCP 服务器配置文件 (*.yaml)
├─ skills/              → 用户自定义技能 (SKILL.md 或 *.md)
├─ agents/              → 自定义代理定义
├─ memory/              → 文件级内存存储
│  ├─ user/             → 用户作用域内存
│  ├─ session/          → 会话级内存
│  ├─ global/           → 全局内存
│  ├─ feedback/         → 反馈记录
│  └─ archived/         → 归档内存
└─ backups/             → 内存备份 (迁移时)
```

**Personality 三层系统**:
1. **Soul.md** → Personality
   - 代理的人设、语气、风格
   - 影响每次回复的表达方式

2. **Memory.md** → PersistentRules
   - 长期遵守的规则和原则
   - 所有认知阶段都必须遵循

3. **Agent.md** → 系统提示前缀
   - 特定于 IronClaw 的指示
   - 与 YAML 配置中的 system_prompt 拼接

**应用流程**:
```
userdir.Apply(cfg):
├─ 检查 ~/.IronClaw 存在
│  └─ 不存在则 initDir() 创建默认模板
├─ applyPersonality(cfg, base)
│  ├─ 读取 Soul.md → cfg.Agent.Personality
│  ├─ 读取 Memory.md → cfg.Agent.PersistentRules
│  └─ 读取 Agent.md → 前缀到 cfg.Agent.SystemPrompt
├─ applyMCP(cfg, base)
│  └─ 扫描 ~/.IronClaw/mcp/*.yaml 并加载配置
├─ ensureSkillsDir(base)
└─ ensureAgentsDir(base)
```

**MCP 服务器文件加载**:
```
~/.IronClaw/mcp/
├─ filesystem.yaml
│  ├─ name: filesystem
│  ├─ command: mcp-filesystem
│  ├─ args: [/home/user/docs]
│  ├─ env: {SOME_VAR: value}
│  └─ requires_approval: true
└─ web_search.yaml
   └─ ...
```

---

## 十、强化学习系统（RL System）

### 10.1 分层 RL 架构

IronClaw 采用三层强化学习系统，处理不同层级的决策：

```
┌─────────────────────────────────────────┐
│        认知循环 (Cognitive Loop)         │
│  PERCEIVE → PLAN → ACT → OBSERVE → REFLECT
└─────────────────────────────────────────┘
              ↓
┌─────────────────────────────────────────┐
│         三层 RL 系统                     │
├─────────────────────────────────────────┤
│ Level 1: Bandit (工具选择)              │
│   问题: 给定上下文，选择最优工具        │
│   算法: Thompson Sampling (Beta 分布)   │
│   输出: ToolSelectionAction             │
├─────────────────────────────────────────┤
│ Level 2: PPO (计划策略)                 │
│   问题: 调整计划参数（子任务数、并行度）│
│   算法: Proximal Policy Optimization    │
│   输出: PlanStrategyAction              │
├─────────────────────────────────────────┤
│ Level 3: DQN (重规划决策)               │
│   问题: 反思阶段决定是否重新规划        │
│   算法: Deep Q-Network + ε-greedy      │
│   输出: ReplanActionType (continue/adjust/abort)
└─────────────────────────────────────────┘
```

### 10.2 状态表示 (rl/state.go)

**RLState - 固定 21 维状态向量**:

```go
RLState 包含的特征:

任务特征 (3维):
├─ ComplexitySimple / Moderate / Complex    // One-hot encoding

上下文特征 (4维，归一化 0-1):
├─ MemoryCount      = retrieved_memories / 10
├─ KnowledgeCount   = knowledge_snippets / 10
├─ GraphCount       = graph_relations / 10
└─ HistoryLength    = conversation_messages / 20

计划特征 (2维):
├─ SubTaskCount     = planned_subtasks / 10
└─ PlanConfidence   = plan_confidence (0-1)

执行特征 (5维):
├─ SuccessCount     = successful_subtasks / 10
├─ FailureCount     = failed_subtasks / 10
├─ DeniedCount      = denied_subtasks / 10
├─ Progress         = overall_progress (0-1)
└─ ReplanCount      = num_replans / 5

反思特征 (1维):
└─ ReflectionConfidence = confidence (0-1)

二进制特征 (3维):
├─ HasSkills        = 1 if skills_available else 0
├─ HasAgents        = 1 if agents_available else 0
└─ HasPersonality   = 1 if personality_configured else 0

文本特征 (2维):
├─ WordCount        = user_message_words / 100
└─ ErrorPatternCnt  = num_error_patterns / 5
```

**ToVector() / FromVector()** - 序列化支持：
```go
ToVector() []float64:
    return []float64{
        s.ComplexitySimple,        // 0
        s.ComplexityModerate,      // 1
        s.ComplexityComplex,       // 2
        s.MemoryCount,             // 3
        ... (总计 21 个)
    }
```

### 10.3 动作表示 (rl/action.go)

**三层动作定义**:

1. **Bandit 动作** - ToolSelectionAction:
   ```go
   struct {
       ToolName   string
       ToolIndex  int
       Confidence float64  // Thompson 采样置信度
   }
   ```

2. **PPO 动作** - PlanStrategyAction:
   ```go
   struct {
       SubTaskCountBias float64  // [-1, 1] 调整子任务数
       ParallelBias     float64  // [-1, 1] 调整并行度
       ConfidenceAdj    float64  // [-0.2, 0.2] 调整置信阈值
   }
   ```

3. **DQN 动作** - ReplanActionType:
   ```go
   const (
       ReplanActionContinue = 0
       ReplanActionAdjust   = 1
       ReplanActionAbort    = 2
   )
   ```

**序列化格式** (EncodeAction):
```
[level_byte (1B)][action_data ...]
level_byte: 0=bandit, 1=ppo, 2=dqn
```

### 10.4 奖励系统 (rl/reward.go)

**分解的奖励结构**:

```go
RewardComponents:
├─ TaskSuccess      // 任务完成度 [-1, 1]
├─ Efficiency       // 执行效率 [-1, 1]
├─ Safety           // 安全性 [-1, 1]
└─ UserSatisfaction // 用户反馈 [-1, 1]

加权总奖励:
    Total = TaskSuccess * w1 + Efficiency * w2 + 
            Safety * w3 + UserSatisfaction * w4
```

**工具级奖励** (ComputeToolReward):
```go
ComputeToolReward(succeeded, denied, durationMs) float64:
├─ 拒绝: -0.5
├─ 失败: -1.0
├─ 成功: 1.0 + 效率奖励 (< 5s 加 0-0.1 额外分)
```

**剧集级奖励** (ComputeEpisodeReward):
```go
ComputeEpisodeReward(params, cfg) *RewardComponents:

TaskSuccess:
├─ 成功: +1.0
└─ 失败: -1.0

Efficiency:
├─ 基础: 1.0 - (duration / max_duration)
├─ 重规划惩罚: -0.2 * replan_count
└─ 夹持 [-1, 1]

Safety:
├─ 基础: 1.0 - (denied + failed) / total_actions
└─ 中立: 0.5 (无执行时)

UserSatisfaction:
└─ 从外部反馈设置
```

### 10.5 Bandit 系统 (rl/bandit.go)

**Thompson Sampling for Tool Selection**:

```
核心思想: 对每个 (context, tool) 对维护一个 Beta 分布

ContextualBandit:
├─ storage: *Storage           // 持久化 Beta 参数
├─ priorAlpha, priorBeta      // 先验 Beta(1, 1)
└─ SelectTool(ctx, state, toolNames) *ToolSelectionAction

SelectTool 流程:
1. 计算 context_hash = Hash(state)
2. 对每个 tool:
   ├─ 从 storage 获取 (alpha, beta, pulls, totalReward)
   ├─ 若首次见，使用先验 Beta(1, 1)
   ├─ 从 Beta(alpha, beta) 采样
   └─ 追踪最高样本
3. 返回最高样本对应的工具

Update 流程:
├─ 正规化奖励到 [0, 1]
├─ alpha += normalized_reward
├─ beta += (1 - normalized_reward)
├─ pulls++, totalReward += reward
└─ 存储更新的参数
```

**优势**:
- 平衡探索 vs 开发
- 自适应：好的工具被选中概率更高
- 上下文感知：同一工具在不同上下文有不同参数

### 10.6 PPO 系统 (rl/ppo.go)

**Policy Gradient Learning for Plan Strategy**:

```
PPO 初始化:
├─ policyNet: 21维输入 → 64 → 32 → 3维输出 (Tanh)
│  └─ 输出每个动作维度 [-1, 1]
├─ valueNet: 21维输入 → 64 → 32 → 1维输出 (Linear)
│  └─ 预测状态价值
└─ optimizer: Adam (默认学习率 0.0003)

SelectAction(state) *PlanStrategyAction:
├─ stateVec = state.ToVector()
├─ actionVec = policyNet.Forward(stateVec)
├─ 添加探索噪声: N(0, 0.1)
└─ 返回 PlanStrategyFromVector(actionVec)

Update(experiences) 使用 GAE:
├─ computeGAE() 计算优势 (Generalized Advantage Estimation)
├─ normalizeSlice() 对优势进行归一化
├─ 对 Epoch 次迭代:
│  ├─ 计算新的 log_prob
│  ├─ PPO 目标:
│  │  min(ratio * advantage, 
│  │      clip(ratio, 1-ε, 1+ε) * advantage)
│  ├─ 价值损失: (return - predicted_value)^2
│  └─ 总损失 = policy_loss + value_loss
└─ 优化参数
```

### 10.7 DQN 系统 (rl/dqn.go)

**Value-Based Learning for Replan Decisions**:

```
DQN 初始化:
├─ qNet: 21维输入 → 64 → 32 → 3维输出 (Linear)
│  └─ 对应 3 个重规划动作的 Q-值
├─ targetNet: qNet.Clone()
│  └─ 用于目标计算（缓慢更新）
├─ optimizer: Adam (默认学习率 0.001)
└─ epsilon: 0.9 (初始探索率)

SelectAction(state) ReplanActionType:
├─ 若 rand() < epsilon:
│  └─ 随机选择 [0, 1, 2]
├─ 否则:
│  ├─ qValues = qNet.Forward(state.ToVector())
│  ├─ 选择 max Q-value 对应的动作
│  └─ 返回 ReplanActionType
└─ epsilon 随时间衰减

Update(experiences):
├─ 对每个经验:
│  ├─ 目标 Q = reward + γ * max_a' Q_target(s', a')
│  ├─ 现在 Q = qNet(state)
│  ├─ 损失 = (target_Q - current_Q)^2
│  └─ 反向传播
├─ 定期更新 targetNet (硬复制)
└─ epsilon 衰减: epsilon *= decay_rate
```

### 10.8 神经网络实现 (rl/nn/)

**从零构建的轻量级 NN**:

```go
Network 结构:
├─ layers: []*Layer         // 层数组
├─ Add(layer) *Network      // Builder pattern
└─ Forward(input) []float64 // 前向传播

Layer 结构:
├─ Weights: [][]float64     // [outputDim][inputDim]
├─ Biases: []float64        // [outputDim]
├─ ActFn: ActivationFn      // ReLU / Tanh / Linear
├─ lastInput/Output/PreAct  // 缓存用于反向传播
└─ WeightGrads/BiasGrads    // 累积梯度

Forward(input):
├─ 保存输入用于反向传播
├─ 对每一层: output = activation(W @ input + b)
└─ 返回最终输出

Backward(gradOutput):
├─ 反向传播梯度
├─ 计算权重和偏置梯度
└─ 返回输入梯度

激活函数:
├─ ReLU: f(x) = max(0, x), f'(x) = x > 0 ? 1 : 0
├─ Tanh: f(x) = tanh(x), f'(x) = 1 - tanh²(x)
└─ Linear: f(x) = x, f'(x) = 1

Xavier 初始化:
    W ~ N(0, scale)，其中 scale = sqrt(2 / (in_dim + out_dim))

Optimizer (Adam):
├─ 维护一阶矩 (m) 和二阶矩 (v)
├─ m ← β1 * m + (1-β1) * grad
├─ v ← β2 * v + (1-β2) * grad²
└─ param ← param - lr * m / (sqrt(v) + ε)
```

### 10.9 RL 存储与持久化 (rl/storage.go, rl/experience.go)

**Experience 定义**:
```go
Experience:
├─ State: *RLState           // 初始状态
├─ Action: []float64         // 动作向量
├─ Reward: float64           // 奖励信号
├─ NextState: *RLState       // 后继状态
└─ Done: bool                // 剧集结束标志
```

**数据库存储**:
```
rl_episodes: 总体剧集统计
├─ id, session_id, goal, complexity
├─ total_reward, succeeded, subtask_count
├─ replan_count, duration_ms, created_at

rl_trajectories: 详细轨迹 (经验)
├─ id, episode_id, step, level
├─ state (BLOB), action (BLOB), reward
├─ next_state (BLOB), done, metadata

rl_rewards: 分解奖励
├─ episode_id, reward_type (task_success/efficiency/...)
├─ value, weight, created_at

rl_model_checkpoints: 模型快照
├─ policy_name, version
├─ state_dim, action_dim
├─ weights (BLOB - 序列化权重)
├─ metrics (JSON), created_at

rl_bandit_arms: Bandit 统计
├─ context_hash, arm_name
├─ alpha, beta, pulls, total_reward
└─ updated_at
```

**生命周期**:
1. 获取经验 → 存入 rl_trajectories
2. 剧集结束 → 计算总奖励 → 存入 rl_episodes
3. 定期（每 N 剧集） → 训练模型 → 保存 checkpoint

---

## 十一、系统整合流程

### 11.1 网关初始化顺序 (gateway.go)

```
初始化顺序（严格按序）:

1. DB 开启
   └─ SQLite 连接 + 迁移

2. Session Manager
   └─ 依赖 DB

3. Tool Registry
   └─ 独立初始化

4. LLM Provider
   └─ 依赖配置

5. Agent Runtime
   └─ 依赖 Registry + LLM Provider

6. Memory Store (可选)
   └─ 依赖 DB + 可选 LLM Provider

7. Cognitive Agent (mode=cognitive 时)
   └─ 依赖 Agent Runtime + Memory Store

8. Knowledge Base (可选)
   └─ 依赖 DB + 可选 Embedder

9. Skill Manager
   └─ 加载内置和用户技能

10. Scheduler
    └─ 依赖 DB + 设置 handler

11. Channels (Telegram/TUI)
    └─ 独立初始化但监听 Handler

12. MCP Manager
    └─ 发现和注册工具到 Registry

关键依赖关系:
Agent ← (Registry, LLM)
CognitiveAgent ← (Agent, Memory)
KnowledgeBase ← (DB, Embedder)
Scheduler ← DB
Channel ← Handler
MCP ← Registry
```

### 11.2 请求处理流程

```
用户输入 (Telegram/TUI)
  ↓ Channel adapter 接收
  ↓ 转换为 InboundMessage
  ↓ 传递给 Handler (通常在 Gateway)
  ↓ 获取或创建 Session
  ↓ 添加用户消息到 history
  ↓ 检索相关上下文:
     ├─ Memory Store 搜索
     ├─ Knowledge Base 搜索
     ├─ KG 遍历
     └─ 提取工具列表
  ↓ 构建系统提示 + 上下文
  ↓ 调用 Agent (simple 或 cognitive)
     ├─ Simple: 单轮 LLM 循环 + 工具执行
     └─ Cognitive: 5 阶段 PERCEIVE/PLAN/ACT/OBSERVE/REFLECT
  ↓ 工具执行时:
     ├─ 检查 approval 要求
     ├─ 若需要，调用 Channel.SendApprovalRequest()
     ├─ 等待用户响应
     └─ 执行 + 审计日志
  ↓ 反思阶段（cognitive 时）:
     ├─ 评估计划置信度
     ├─ 若低，调用 Channel.SendReflectionRequest()
     ├─ 根据决定重新规划或继续
     └─ 记录 RL 经验
  ↓ 流式或一次性发送响应
  ↓ 持久化会话消息
  ↓ 记录 RL 奖励
```

### 11.3 强化学习反馈循环

```
每个会话：
1. 初始化 RL Episode
   ├─ session_id, goal (用户问题), complexity
   └─ 记录开始时间

2. 每个工具执行：
   ├─ 创建 Trajectory
   ├─ 记录 (state, action, reward, next_state)
   └─ 更新 Bandit 统计

3. 反思阶段：
   ├─ 捕捉 (state, ppo_action, reward, next_state)
   ├─ DQN 观察到用户是否同意重规划
   └─ 记录决策轨迹

4. 剧集结束：
   ├─ 计算总奖励 (成功度 + 效率 + 安全性 + 用户反馈)
   ├─ 存储 rl_episodes 记录
   └─ 完整持久化

5. 训练（定期）：
   ├─ 从 rl_trajectories 采样 mini-batch
   ├─ 分别训练 Bandit/PPO/DQN
   ├─ 保存 checkpoint
   └─ 评估指标
```

---

## 十二、关键设计模式

### 12.1 适配器模式
- **Channel 适配器** - 统一不同通信平台
- **Tool 适配器** - MCP 工具适配为本地工具
- **Ingester 适配器** - 多种文档格式统一处理

### 12.2 策略模式
- **Ingester** - 按 source_type 选择策略
- **Reranker** - 可切换 NoopReranker 或 LLMReranker
- **RL 策略** - Bandit/PPO/DQN 三层独立

### 12.3 工厂模式
- **Tool Registry** - 集中创建和注册
- **Skill Manager** - 加载和管理技能集合

### 12.4 观察者模式
- **Event-Driven** - Channel 事件触发 Handler
- **Message Queue** - Bubble Tea tea.Msg 通信

### 12.5 装饰者模式
- **TUI modelWrapper** - 扩展 Model 而不修改
- **RL Experience** - 累积经验后异步处理

### 12.6 避免循环依赖
- **completerAdapter** - Agent.Provider → Memory.Completer 的桥接
- **SetHandler()** - Scheduler 回调注册而非直接依赖
- **Optional Interfaces** - ApprovalSender/ReflectionSender 可选

---

## 十三、特点和亮点

### 13.1 本地优先设计
- SQLite 嵌入式数据库，无需外部 DB
- 文件级内存系统，优先本地存储
- 离线可用，数据隐私保护

### 13.2 灵活的通道支持
- 开放式 Channel 接口
- 支持 Telegram、TUI，易于扩展
- 流式输出和交互式批准内置

### 13.3 完整的工具生态
- 内置 Bash/File/HTTP/Skill 工具
- MCP 动态集成
- 安全策略保护
- 工具审计日志

### 13.4 分层强化学习
- Bandit 用于工具选择
- PPO 用于计划参数
- DQN 用于重规划决策
- 完整的 state/reward/experience 管理

### 13.5 认知架构
- Simple 模式快速推理
- Cognitive 模式 5 阶段反思
- 用户交互式决策（批准/重规划）
- 内置记忆和知识集成

---

## 总结

IronClaw 是一个精心设计的 AI 代理系统，融合了：
- **模块化架构** - 清晰的职责分离
- **多层交互** - 通道、工具、知识、记忆
- **强化学习** - 分层 RL 系统
- **完整生命周期** - 从输入到持久化
- **可扩展性** - 易于添加新工具/通道/技能

核心哲学是**本地优先、隐私保护、用户控制**，同时保持高度的**灵活性和可定制性**。
