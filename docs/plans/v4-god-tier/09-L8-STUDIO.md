# L8 — Agent Studio + Multi-Tenant (可视化工作室 + 多租户)

> 优先级: P8 | 工作量: 4-6 周 | 依赖: L1 (WASM 插件市场需要前端展示)  
> 从只读 Dashboard 到完整的 Agent 可视化开发环境。支持多租户隔离。

---

## 一、Studio 功能矩阵

```
┌──────────────────────────────────────────────────────────────────┐
│                      Agent Studio                                │
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌──────────┐  ┌───────────┐ │
│  │ Flow Editor │  │ Prompt IDE  │  │ Memory   │  │ Evolution │ │
│  │ 流水线编辑器 │  │ 提示词工作室 │  │ Explorer │  │ Monitor   │ │
│  │ 节点拖拽    │  │ Monaco编辑  │  │ 记忆浏览 │  │ 进化监控  │ │
│  │ 实时预览    │  │ 版本管理    │  │ 图谱可视化│  │ 策略对比  │ │
│  └─────────────┘  └─────────────┘  └──────────┘  └───────────┘ │
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌──────────┐  ┌───────────┐ │
│  │ Tool        │  │ Eval        │  │ Agent     │  │ Settings  │ │
│  │ Marketplace │  │ Dashboard   │  │ Swarm     │  │ & Admin   │ │
│  │ 工具市场    │  │ 评测面板    │  │ 群体面板  │  │ 设置管理  │ │
│  └─────────────┘  └─────────────┘  └──────────┘  └───────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

---

## 二、Flow Editor — 可视化流水线编辑器

这是 Studio 的核心功能。允许非程序员通过拖拽节点构建 Agent 流水线。

### 2.1 节点类型

```
┌─────────────────────────────────────────────────────────┐
│                    Node Types                           │
│                                                         │
│  TRIGGER:                                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │ OnMsg    │  │ Schedule │  │ Webhook  │             │
│  └──────────┘  └──────────┘  └──────────┘             │
│                                                         │
│  PROCESS:                                               │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │ Memory   │  │ Plan     │  │ Tool     │             │
│  │ Search   │  │ (LLM)    │  │ Execute  │             │
│  └──────────┘  └──────────┘  └──────────┘             │
│                                                         │
│  DECISION:                                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │ Branch   │  │ Loop     │  │ Parallel │             │
│  │ (if/else)│  │ (while)  │  │ (fanout) │             │
│  └──────────┘  └──────────┘  └──────────┘             │
│                                                         │
│  OUTPUT:                                                │
│  ┌──────────┐  ┌──────────┐                            │
│  │ Respond  │  │ Call     │                            │
│  │ to User  │  │ SubAgent │                            │
│  └──────────┘  └──────────┘                            │
└─────────────────────────────────────────────────────────┘
```

### 2.2 流水线序列化格式 (YAML)

导出/导入格式，既能可视化编辑也能用代码定义：

```yaml
# pipeline.yaml
name: customer-support-agent
version: 1.0.0
description: Multi-step customer support pipeline with escalation

triggers:
  - type: on_message
    channel: "*"
    filter:
      keywords: ["help", "support", "issue", "problem"]

nodes:
  - id: classify
    type: llm
    config:
      model: claude-sonnet-4-20250514
      system_prompt: |
        Classify the user's request into:
        - billing: payment, subscription, invoice issues
        - technical: bugs, errors, performance
        - account: login, password, settings
        - general: everything else
        Output ONLY the category name.
      temperature: 0.1
      max_tokens: 50

  - id: search_kb
    type: tool
    config:
      tool: knowledge_search
      input_template: "Find solutions for: {{user_message}}"

  - id: escalate_check
    type: branch
    config:
      condition: "{{classify.output}} == 'billing'"
      true_branch: escalate
      false_branch: respond

  - id: escalate
    type: sub_agent
    config:
      agent: billing-specialist
      task_template: |
        User needs billing help: {{user_message}}
        Knowledge base results: {{search_kb.output}}

  - id: respond
    type: llm
    config:
      system_prompt: |
        You are a helpful support agent.
        Use the knowledge base results to answer the user.
        KB Results: {{search_kb.output}}
        Agent response: {{escalate.output}}
      temperature: 0.7

  - id: judge_quality
    type: guardian
    config:
      action: judge
      criteria: [accuracy, helpfulness]

edges:
  - from: trigger
    to: classify
  - from: classify
    to: search_kb
  - from: search_kb
    to: escalate_check
  - from: escalate_check
    to: escalate (true)
  - from: escalate_check
    to: respond (false)
  - from: escalate
    to: respond
  - from: respond
    to: judge_quality
```

### 2.3 前后端接口

```go
// 后端 API

// GET /api/pipelines — 列出所有流水线
// POST /api/pipelines — 创建流水线
// PUT /api/pipelines/:id — 更新流水线
// DELETE /api/pipelines/:id — 删除流水线
// POST /api/pipelines/:id/run — 手动运行
// GET /api/pipelines/:id/runs — 运行历史
// POST /api/pipelines/:id/validate — 验证流水线定义
```

前端技术栈:
- **Vue 3** + **TypeScript** + **Vite**
- **Vue Flow** (节点编辑器，基于 React Flow 的 Vue 版本)  
- **Monaco Editor** (提示词编辑器，VS Code 同款)
- **D3.js** (知识图谱可视化、进化曲线)

---

## 三、多租户架构

```go
// internal/gateway/multitenant.go

// TenantManager 管理多个租户
type TenantManager struct {
    tenants   map[string]*Tenant
    mu        sync.RWMutex
    baseDir   string  // ~/.IronClaw/tenants/
}

// Tenant 是一个完全隔离的用户/项目空间
type Tenant struct {
    ID          string
    Name        string
    // 每个租户有独立的:
    Config      *config.Config     // 独立配置
    Cortex      *cortex.Store      // 独立记忆库
    Sessions    *session.Manager   // 独立会话
    Tools       *tool.Registry     // 独立工具集（可共享基础工具）
    Agents      []*agent.AgentSpec // 独立代理
    Pipelines   []*Pipeline       // 独立流水线
    Evolution   *evolution.Engine // 独立进化状态
    // 可共享的:
    SharedPlugins []*wasm.Plugin  // 工具插件可跨租户共享
}

// CreateTenant 创建一个新租户
func (tm *TenantManager) CreateTenant(ctx context.Context, name string) (*Tenant, error) {
    id := uuid.New().String()
    tenantDir := filepath.Join(tm.baseDir, id)

    tenant := &Tenant{
        ID:   id,
        Name: name,
    }

    // 初始化独立的存储
    db, _ := store.Open(filepath.Join(tenantDir, "data.db"))
    tenant.Cortex = cortex.NewStore(db)
    tenant.Sessions = session.NewManager(db)

    tm.tenants[id] = tenant
    return tenant, nil
}

// RouteMessage 将消息路由到正确的租户
func (tm *TenantManager) RouteMessage(ctx context.Context, tenantID string, msg channel.InboundMessage) error {
    tenant, ok := tm.tenants[tenantID]
    if !ok {
        return fmt.Errorf("tenant not found: %s", tenantID)
    }
    return tenant.HandleMessage(ctx, msg)
}
```

---

## 四、Prompt IDE

一个完整功能的提示词编辑器:

```
┌─────────────────────────────────────────────────┐
│  Prompt Editor                [Save] [Version]  │
├─────────────────────────────────────────────────┤
│  ┌───────────────────────┐  ┌─────────────────┐ │
│  │ Editor (Monaco)       │  │ Preview          │ │
│  │                       │  │                  │ │
│  │ ## Personality        │  │ [实时渲染的      │ │
│  │ You are a {{role}}    │  │  预览效果]       │ │
│  │                       │  │                  │ │
│  │ ## Rules              │  │                  │ │
│  │ 1. ...                │  │                  │ │
│  │                       │  │                  │ │
│  │ {{#each memories}}    │  │                  │ │
│  │ - {{this}}            │  │                  │ │
│  │ {{/each}}             │  │                  │ │
│  │                       │  │                  │ │
│  └───────────────────────┘  └─────────────────┘ │
├─────────────────────────────────────────────────┤
│  Variables: {{role}} {{memories}} [+]           │
│  Test Input: [________________] [Test Run ▶]   │
│  Version: v3 (current) v2 v1                    │
├─────────────────────────────────────────────────┤
│  Test Results:                                  │
│  ✅ Success: 85%  ⚡ Avg Latency: 2.3s          │
│  A/B Test: v3 vs v2: +7% success rate (p<0.05) │
└─────────────────────────────────────────────────┘
```

---

## 五、验收标准

1. **Flow Editor**: 可以通过拖拽节点构建完整流水线，导出 YAML，导入另一实例运行
2. **Prompt IDE**: 支持变量/模板/Monaco 编辑/A-B 对比
3. **Memory Explorer**: 可视化浏览记忆、图谱、按时间/类型/关联过滤
4. **多租户**: 创建 3 个租户，各自独立记忆和配置，互不干扰
5. **响应式**: 桌面端 Full HD 可用，移动端有基本 Dashboard
