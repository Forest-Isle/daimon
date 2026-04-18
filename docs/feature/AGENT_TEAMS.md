# Agent Teams（多 Agent 协作）

**日期**: 2026-04-18
**范围**: TeamCoordinator 协调器 + LLM 任务规划 + Worker 池 + 依赖调度 + Gateway 集成

## 概述

Agent Teams 是一个对等协作式多 Agent 系统，参考 Claude Code 的 Agent Teams 设计。用户通过 `/team <goal>` 命令提交一个高层目标，系统自动：

1. **规划**: LLM 将目标分解为可并行的原子任务，带依赖关系
2. **调度**: Worker 池从 Task Ledger 中认领任务，自动处理依赖阻塞
3. **执行**: 每个 Worker 独立执行认领的任务
4. **汇总**: 收集所有结果，报告完成/失败统计

与传统的 Orchestrator 模式不同，Agent Teams 不依赖中心控制器逐个分配任务，而是通过共享的 Task Ledger 实现去中心化的任务认领，最大化并行度。

## 架构

```
/team "重构用户模块并添加测试"
│
▼
Gateway.handleTeamCommand()
│
├── [1] LLM 规划
│   ├── 发送 TeamPlanPrompt + goal 给 LLM
│   └── 返回 JSON 任务数组
│
├── [2] ParseTaskPlan() → 验证 + 转换
│   ├── 检查: 无空 ID、无重复 ID、依赖指向已知 ID
│   └── 生成 []Task (kind=team_task, state=pending)
│
├── [3] 注册根任务 + 子任务到 Task Ledger
│   ├── 根任务: team_xxxxx (running)
│   └── 子任务: t1, t2, t3... (pending)
│
├── [4] TeamCoordinator.RunWithExecutor()
│   ├── 启动 N 个 Worker goroutine
│   ├── 每个 Worker 循环: ClaimNext → blockedByDeps? → execute → complete/fail
│   └── 所有 Worker 退出后汇总统计
│
└── [5] 更新根任务状态 → 返回结果给用户
```

## TeamCoordinator

```go
type TeamCoordinator struct {
    ledger     TaskLedger
    maxWorkers int
    executor   TaskExecutor     // func(ctx, task) (string, error)
    notifyCh   chan Notification // 轻量级通知通道
    mu         sync.Mutex
}
```

### Worker 循环

每个 Worker 独立运行以下循环：

```
worker-N:
  for {
      task := ledger.ClaimNext(ctx, TaskKindTeamTask, "worker-N")
      if task == nil { return }  // 无更多任务

      blocked, depFailed := blockedByDeps(ctx, task)
      if depFailed {
          cancelTask(task, "dependency failed")  // 级联取消
          continue
      }
      if blocked {
          putBack(task)           // 放回 pending 状态
          sleep(50ms)             // 等待依赖完成
          continue
      }

      result, err := executor(ctx, *task)
      if err {
          failTask(task, err)
          notify("failed")
      } else {
          completeTask(task, result)
          notify("completed")
      }
  }
```

### 依赖处理

`blockedByDeps` 检查任务的所有 `DependsOn` 依赖：

| 依赖状态 | 行为 |
|----------|------|
| `completed` | 继续检查下一个依赖 |
| `failed` / `cancelled` | 返回 `depFailed=true` → 当前任务被取消 |
| `pending` / `running` | 返回 `blocked=true` → 当前任务放回队列 |
| 查询错误 | 视为 blocked，稍后重试 |

**依赖失败传播**: 当一个依赖失败时，依赖它的任务会被级联取消（通过 `ledger.Cancel` 的递归 CTE），确保不会在已知失败的基础上继续执行。

### 通知通道

`Notification` 是一个轻量级的非阻塞通道（容量 64），Worker 在任务完成/失败时发送通知：

```go
type Notification struct {
    FromTaskID string
    Type       string // "completed", "failed", "info"
    Message    string
}
```

通道满时静默丢弃（`select` + `default`），避免阻塞 Worker。

### TeamResult

```go
type TeamResult struct {
    RootTaskID     string
    TasksCompleted int
    TasksFailed    int
    TasksCancelled int
    Summary        string        // "3 completed, 1 failed, 0 cancelled"
    Duration       time.Duration
}
```

## LLM 任务规划

### TeamPlanPrompt

```
You are a task planner. Break the following goal into independent, parallelizable tasks.

Output a JSON array where each task has:
- "id": unique short identifier (e.g., "t1", "t2")
- "title": concise task title
- "description": detailed instructions for an agent to execute this task
- "depends_on": array of task IDs that must complete before this task can start

Rules:
- Maximize parallelism: only add dependencies when truly necessary
- Each task should be independently executable by an agent
- Keep tasks focused and atomic

Goal: <用户目标>
```

### ParseTaskPlan 验证

LLM 返回的 JSON 经过严格验证：

| 检查 | 失败条件 |
|------|---------|
| JSON 格式 | 无法解析 |
| 非空 | 任务列表为空 |
| ID 非空 | 任何任务 ID 为空字符串 |
| ID 唯一 | 存在重复 ID |
| 依赖有效 | depends_on 引用了不存在的 ID |

验证通过后，每个 plan 条目被转换为 `Task{Kind: TaskKindTeamTask, State: TaskStatePending, ParentID: rootID}`。

### 示例

**输入**: `/team 重构用户认证模块并添加单元测试`

**LLM 输出**:
```json
[
  {"id": "t1", "title": "分析现有认证代码", "description": "...", "depends_on": []},
  {"id": "t2", "title": "设计新接口", "description": "...", "depends_on": ["t1"]},
  {"id": "t3", "title": "实现 JWT 验证", "description": "...", "depends_on": ["t2"]},
  {"id": "t4", "title": "实现 Session 管理", "description": "...", "depends_on": ["t2"]},
  {"id": "t5", "title": "编写单元测试", "description": "...", "depends_on": ["t3", "t4"]}
]
```

**执行图**:
```
t1 ──> t2 ──> t3 ──┐
                    ├──> t5
              t4 ──┘
```

t3 和 t4 可并行执行（共同依赖 t2），t5 等待两者完成。

## Gateway 集成

### 初始化

```go
if cfg.Agent.Team.Enabled {
    tc := taskledger.NewTeamCoordinator(gw.taskLedger, maxWorkers)
    tc.SetExecutor(func(ctx context.Context, task taskledger.Task) (string, error) {
        return gw.executeTeamTask(ctx, task)
    })
    gw.teamCoordinator = tc
}
```

### executeTeamTask

每个 Team 任务通过一次独立的 LLM 调用执行：

```go
func (gw *Gateway) executeTeamTask(ctx context.Context, task taskledger.Task) (string, error) {
    req := agent.CompletionRequest{
        Model:   gw.cfg.LLM.Model,
        System:  "You are an agent executing a specific task. Be concise and focused.",
        Messages: []agent.CompletionMessage{{Role: "user", Content: task.Description}},
    }
    resp, err := gw.provider.Complete(ctx, req)
    if err != nil { return "", err }
    return resp.Text, nil
}
```

### handleTeamCommand 完整流程

```go
func (gw *Gateway) handleTeamCommand(ctx context.Context, goal string) string {
    // 1. LLM 生成计划
    prompt := fmt.Sprintf(taskledger.TeamPlanPrompt, goal)
    resp, _ := gw.provider.Complete(ctx, req)

    // 2. 注册根任务
    rootTask := Task{ID: "team_...", Kind: TaskKindTeamTask, State: TaskStateRunning}
    gw.taskLedger.Register(ctx, rootTask)

    // 3. 解析并注册子任务
    tasks, _ := taskledger.ParseTaskPlan(resp.Text, rootID)
    for _, t := range tasks {
        gw.teamCoordinator.AddTask(ctx, t)
    }

    // 4. 执行
    result, _ := gw.teamCoordinator.RunWithExecutor(ctx)

    // 5. 更新根任务
    rootTask.State = TaskStateCompleted
    rootTask.Result = result.Summary
    gw.taskLedger.Update(ctx, rootTask)

    return "Team completed: X tasks done, Y failed"
}
```

## 配置

```yaml
agent:
  team:
    enabled: true      # 是否启用 Agent Teams
    max_workers: 3     # Worker 池大小（并行执行数）
    model: ""          # 可选：Team 任务使用的模型（空则使用默认模型）
```

## 斜杠命令

### `/team <goal>` — 启动 Agent Team

将 `<goal>` 分解为并行任务并执行。

```
用户: /team 重构日志系统，支持结构化输出和日志轮转
Agent: Team completed: 4 tasks done, 0 failed
```

执行期间可通过 `/tasks` 查看实时进度。

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/taskledger/team.go` | 新增 | TeamCoordinator、Worker 循环、依赖调度、通知通道 |
| `internal/taskledger/team_test.go` | 新增 | Worker 认领/完成、依赖阻塞、依赖失败传播 |
| `internal/taskledger/team_planner.go` | 新增 | TeamPlanPrompt + ParseTaskPlan（JSON 解析 + 验证） |
| `internal/config/config.go` | 修改 | TeamConfig 结构 |
| `internal/gateway/gateway.go` | 修改 | handleTeamCommand + executeTeamTask + TeamCoordinator 初始化 |
| `internal/channel/tui/commands.go` | 修改 | 注册 /team 斜杠命令 |

## 测试

### team_test.go（3 个用例）

- `TestTeamCoordinator_WorkerClaimsAndCompletes` — Worker 认领任务并成功完成
- `TestTeamCoordinator_Dependencies` — 有依赖的任务在依赖完成前被阻塞，完成后可执行
- `TestTeamCoordinator_FailedDependency` — 依赖失败时，下游任务被取消

### team_planner 测试（在 store_test.go 中）

- `TestParseTaskPlan_Valid` — 合法 JSON 正确解析
- `TestParseTaskPlan_InvalidJSON` — 非法 JSON 报错
- `TestParseTaskPlan_InvalidDependency` — 引用不存在的依赖 ID 报错
- `TestParseTaskPlan_DuplicateIDs` — 重复 ID 报错

## 与现有 Multi-Agent 系统的关系

IronClaw 已有的 Multi-Agent 基础设施（Orchestrator、Backend、BackgroundManager）面向的是**子 Agent 派生**场景——父 Agent 将子任务委托给独立的 Agent 实例（可能运行在不同进程/容器中）。

Agent Teams 面向的是**对等协作**场景——多个 Worker 共享同一个任务队列，通过 Task Ledger 协调，无需层级化的 Orchestrator。两者可以共存：

| 维度 | Multi-Agent (Orchestrator) | Agent Teams (TeamCoordinator) |
|------|---------------------------|-------------------------------|
| 协调模式 | 中心化（父 Agent 分配） | 去中心化（Worker 自行认领） |
| 隔离级别 | 进程/容器（Backend） | 共享进程（goroutine） |
| 任务来源 | 父 Agent 动态创建 | LLM 一次性规划 |
| 通信方式 | IPC/gRPC | Task Ledger + Notification channel |
| 适用场景 | 长时间、高隔离的子任务 | 短周期、高并行的批量任务 |
