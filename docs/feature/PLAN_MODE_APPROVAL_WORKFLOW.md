# Plan Mode — Plan → Approve → Execute Workflow

**日期**: 2026-05-15
**范围**: 新增 `internal/agent/plan_mode.go`，写工具执行前强制走计划→审批→执行流程，对标 Claude Code Plan Mode 和 Devin 的结构化计划。

## 概述

顶级 coding agent 的核心安全机制：agent 在执行任何写操作前，必须先向用户提出结构化计划、获得批准。这防止了 agent 自主做出的破坏性修改，同时保持了对只读操作的无摩擦访问。

Plan Mode 拦截认知循环 ACT 阶段中所有写工具的调用，强制经过 `GeneratePlan → RequestApproval → InterceptTool` 三步校验。

## 架构

### PlanMode 核心结构

```go
type PlanMode struct {
    activePlan    *Plan          // 当前活跃计划（同时只允许一个）
    planProvider  Provider       // LLM 接口，用于生成计划
    approvalFunc  ApprovalFunc   // 用户审批回调（channel 适配）
    autoApprove   bool           // 信任模式下跳过审批
    approvedTools map[string]bool // 当前计划批准的工具白名单
}

type Plan struct {
    ID          string
    Goal        string
    Steps       []PlanStep
    ToolsNeeded []string
    Approved    bool
    CreatedAt   time.Time
}

type PlanStep struct {
    Description string  // 人类可读的步骤描述
    ToolName    string  // 该步骤使用的工具
    Reason      string  // 为什么需要这一步
    IsWrite     bool    // 是否涉及写操作
}
```

### 三步工作流

```
Agent 准备执行 file_write("config.go", ...)
     │
     ▼
[1] GeneratePlan(goal, context)
     ├── LLM 调用 (PlanGenerationPrompt)
     ├── 解析 JSON 计划结构
     └── 设置 activePlan + 清空 approvedTools
     │
     ▼
[2] RequestApproval(plan, channel, target)
     ├── 格式化计划为人类可读文本
     ├── Telegram → 内联键盘 (Approve/Deny)
     ├── TUI → 交互式对话框
     ├── 其他 channel → 回退到 approvalFunc / 自动批准
     └── 批准 → approvedTools = plan.ToolsNeeded
     │
     ▼
[3] InterceptTool(toolName, input)
     ├── 只读工具? → 直接放行
     ├── autoApprove? → 直接放行
     ├── 无 activePlan? → 拒绝: "先生成计划"
     ├── 有计划但未批准? → 拒绝: "先获取审批"
     ├── 工具有批准? → 放行
     └── 工具不在白名单? → 拒绝: "重新计划并审批"
```

### LLM 计划生成 Prompt

```
You are generating an execution plan before any write tool is used.

1. Analyze the user's goal.
2. Break the work into discrete, ordered steps.
3. For each step, identify the tool and whether it is write-capable.
4. Return JSON only.

Output shape:
{
  "goal": "string",
  "steps": [
    {"description": "string", "tool_name": "string",
     "reason": "string", "is_write": bool}
  ],
  "tools_needed": ["tool_a", "tool_b"]
}
```

### 写工具判定

`isPlanModeWriteTool` 函数定义哪些工具需要 Plan Mode 管控：

| 工具 | 类型 | 原因 |
|------|------|------|
| `file_write` | 写 | 直接修改文件 |
| `file_edit` | 写 | 直接修改文件 |
| `bash` | 条件写 | 仅当输入含 `>`、`>>`、`tee`、`dd` 等写入操作符时 |
| `worktree_create` | 写 | 创建新分支 |
| `worktree_merge` | 写 | 合并变更 |

### Gateway 接线

```go
// init_cognitive.go
if gw.provider != nil {
    gw.planMode = agent.NewPlanMode(gw.provider, gw.handleApproval, false)
    gw.cognitiveAgent.SetPlanMode(gw.planMode)
}
```

`CognitiveAgent.SetPlanMode` → `Executor.planMode`，在 ACT 阶段工具执行前调用 `InterceptTool`。

## 配置

Plan Mode 目前始终启用（当 cognitive agent 和 provider 存在时）。未来可通过 `agent.plan_mode.enabled` 配置项控制，`autoApprove` 可通过 `agent.plan_mode.auto_approve: true` 设为信任模式。

## 文件

| 文件 | 说明 |
|------|------|
| `internal/agent/plan_mode.go` | PlanMode 核心 + LLM 计划生成 + 审批流 + 工具拦截 |
| `internal/agent/plan_mode_test.go` | 计划生成/审批/拦截/完成/重置 全覆盖 |
| `internal/agent/act.go` | Executor +planMode 字段 + SetPlanMode 方法 |
| `internal/agent/cognitive.go` | CognitiveAgent +planMode 字段 + SetPlanMode 方法 |
| `internal/gateway/init_cognitive.go` | 创建并注入 PlanMode |
