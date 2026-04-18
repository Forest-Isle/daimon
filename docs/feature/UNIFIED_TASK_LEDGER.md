# 统一任务控制面（Task Ledger）

**日期**: 2026-04-18
**范围**: SQLite 任务注册表 + 原子任务认领 + 递归 CTE 树操作 + 心跳超时检测 + 执行路径集成

## 概述

Task Ledger 是一个统一的任务注册与追踪系统，为 IronClaw 的所有执行路径（用户请求、认知子任务、子 Agent、定时任务、Team 任务）提供一致的生命周期管理。参考 OpenClaw 的 Task Brain 设计，所有任务共享相同的状态机和存储模型，支持父子层级、依赖关系和心跳监控。

**设计动机**: 在引入 Task Ledger 之前，不同执行路径的任务状态散落在各处——Runtime 的局部变量、CognitiveAgent 的循环状态、调度器的数据库行。这使得无法回答「当前系统有哪些任务在运行？哪些卡住了？」这类问题。Task Ledger 将所有这些统一到一张 SQLite 表中。

## 数据模型

### 任务状态机

```
                ┌─────────┐
                │ pending │
                └────┬────┘
                     │ ClaimNext / Register(running)
                     ▼
                ┌─────────┐
          ┌────>│ running │<────────────┐
          │     └────┬────┘             │
          │          │                  │
  putBack │    ┌─────┴──────┐     StaleDetector
  (retry) │    │            │           │
          │    ▼            ▼           │
     ┌─────────┐    ┌───────────┐  ┌────────┐
     │completed│    │ cancelled │  │ failed │
     └─────────┘    └───────────┘  └────────┘
```

### 任务类型

| TaskKind | 来源 | 注册时机 |
|----------|------|---------|
| `user_request` | Runtime / CognitiveAgent | 用户消息进入 HandleMessage |
| `cognitive_subtask` | CognitiveAgent | 每个认知阶段（PERCEIVE / PLAN / ACT / REFLECT） |
| `sub_agent` | Multi-Agent Orchestrator | 子 Agent 被 fork/spawn |
| `scheduled` | Scheduler | cron 触发 |
| `team_task` | TeamCoordinator | Agent Teams 任务分解 |

### Task 结构

```go
type Task struct {
    ID          string            // 唯一标识
    ParentID    string            // 父任务 ID（空表示根任务）
    Kind        TaskKind          // 任务类型
    State       TaskState         // 当前状态
    Title       string            // 简短标题
    Description string            // 详细描述
    Assignee    string            // 分配给哪个 Agent/Worker
    DependsOn   []string          // 依赖的任务 ID 列表
    CreatedAt   time.Time
    UpdatedAt   time.Time
    StartedAt   *time.Time
    CompletedAt *time.Time
    Heartbeat   *time.Time        // 最近一次心跳时间
    Result      string            // 完成摘要或错误消息
    Metadata    map[string]string // 可扩展的键值对
}
```

### 数据库 Schema（Migration 019）

```sql
CREATE TABLE IF NOT EXISTS task_ledger (
    id TEXT PRIMARY KEY,
    parent_id TEXT DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'user_request',
    state TEXT NOT NULL DEFAULT 'pending',
    title TEXT NOT NULL DEFAULT '',
    description TEXT DEFAULT '',
    assignee TEXT DEFAULT '',
    depends_on TEXT DEFAULT '',        -- 逗号分隔的 ID 列表
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    heartbeat DATETIME,
    result TEXT DEFAULT '',
    metadata TEXT DEFAULT ''           -- JSON 格式
);

CREATE INDEX IF NOT EXISTS idx_task_ledger_state ON task_ledger(state);
CREATE INDEX IF NOT EXISTS idx_task_ledger_parent ON task_ledger(parent_id);
CREATE INDEX IF NOT EXISTS idx_task_ledger_kind ON task_ledger(kind);
```

## TaskLedger 接口

```go
type TaskLedger interface {
    Register(ctx context.Context, task Task) error
    Get(ctx context.Context, id string) (*Task, error)
    Update(ctx context.Context, task Task) error
    List(ctx context.Context, filter TaskFilter) ([]Task, error)
    Cancel(ctx context.Context, id string, reason string) error
    ClaimNext(ctx context.Context, kind TaskKind, assignee string) (*Task, error)
    Heartbeat(ctx context.Context, id string) error
    GetTree(ctx context.Context, rootID string) ([]Task, error)
    DetectStale(ctx context.Context, timeout time.Duration) ([]Task, error)
}
```

### 关键操作详解

#### ClaimNext — 原子任务认领

使用 SQLite 事务确保多个 Worker 不会认领同一个任务：

```
BEGIN TX
  SELECT id FROM task_ledger WHERE state = 'pending' AND kind = ? ORDER BY created_at ASC LIMIT 1
  UPDATE task_ledger SET state = 'running', assignee = ?, started_at = now, heartbeat = now WHERE id = ?
  SELECT * FROM task_ledger WHERE id = ?
COMMIT
```

如果没有待认领的任务，返回 `(nil, nil)`，调用方据此退出工作循环。

#### Cancel — 递归级联取消

使用 SQLite 递归 CTE 一次性取消任务及其所有后代：

```sql
WITH RECURSIVE descendants(id) AS (
    SELECT id FROM task_ledger WHERE id = ?
    UNION ALL
    SELECT tl.id FROM task_ledger tl JOIN descendants d ON tl.parent_id = d.id
)
UPDATE task_ledger SET state = 'cancelled', result = ?, updated_at = ?
WHERE id IN (SELECT id FROM descendants) AND state NOT IN ('completed', 'failed')
```

已完成或已失败的任务不会被取消，防止覆盖有效的终态。

#### GetTree — 递归查询任务树

同样使用递归 CTE，从根任务开始递归查询所有后代，按 `created_at` 排序返回完整任务树。

#### DetectStale — 超时检测

```sql
SELECT * FROM task_ledger
WHERE state = 'running' AND COALESCE(heartbeat, started_at) < ?
```

使用 `COALESCE` 确保即使从未发送过心跳的任务（只有 `started_at`）也能被检测到。

## StaleDetector 后台守护

`StaleDetector` 是一个后台 goroutine，定期扫描超时任务并标记为失败：

```go
type StaleDetector struct {
    ledger   TaskLedger
    timeout  time.Duration    // 心跳超时阈值（默认 2 分钟）
    interval time.Duration    // 扫描间隔（默认 30 秒）
    onStale  StaleCallback    // 可选回调
    stopCh   chan struct{}
    wg       sync.WaitGroup
}
```

**生命周期**:
- `Start()`: 启动后台 goroutine，每 `interval` 调用 `sweep()`
- `Stop()`: 关闭 `stopCh`，`wg.Wait()` 等待 goroutine 退出
- `sweep()`: 带 10 秒超时的 `DetectStale` 调用，将超时任务标记为 `failed`，结果设为 `"stale heartbeat"`

## 执行路径集成

### Runtime（简单模式）

```go
func (r *Runtime) HandleMessage(ctx, ch, msg) error {
    if r.taskLedger != nil {
        task := Task{ID: "req_...", Kind: TaskKindUserRequest, State: TaskStateRunning, Title: msg.Text}
        r.taskLedger.Register(ctx, task)
        defer func() {
            task.State = TaskStateCompleted
            task.CompletedAt = &now
            r.taskLedger.Update(ctx, task)
        }()
    }
    // ... agent loop ...
}
```

### CognitiveAgent（认知模式）

注册父任务和 4 个阶段子任务：

```go
func (ca *CognitiveAgent) HandleMessage(ctx, ch, msg) error {
    // 注册父任务
    parentTaskID := "cog_..."
    ca.taskLedger.Register(ctx, parentTask)
    defer func() { /* 标记父任务完成 */ }()

    // PERCEIVE
    donePerceive := ca.registerSubtask(ctx, parentTaskID, "PERCEIVE phase")
    state, err := ca.perceiver.Run(...)
    donePerceive()  // ← 标记子任务完成

    // PLAN
    donePlan := ca.registerSubtask(ctx, parentTaskID, "PLAN phase (attempt N)")
    plan, err = ca.planner.Run(...)
    donePlan()

    // ACT
    doneAct := ca.registerSubtask(ctx, parentTaskID, "ACT phase")
    observations, err := ca.executor.RunWithContext(...)
    doneAct()

    // REFLECT
    doneReflect := ca.registerSubtask(ctx, parentTaskID, "REFLECT phase")
    reflection, err = ca.reflector.Run(...)
    doneReflect()
}
```

`registerSubtask` 返回一个闭包，调用时将子任务状态从 `running` 更新为 `completed`：

```go
func (ca *CognitiveAgent) registerSubtask(ctx, parentID, title string) func() {
    if ca.taskLedger == nil || parentID == "" {
        return func() {}  // no-op
    }
    task := Task{
        ID: "sub_...", ParentID: parentID,
        Kind: TaskKindCognitiveSubtask, State: TaskStateRunning,
    }
    ca.taskLedger.Register(ctx, task)
    return func() {
        task.State = TaskStateCompleted
        task.CompletedAt = &now
        ca.taskLedger.Update(ctx, task)
    }
}
```

### Gateway

```
Gateway.New()
├── taskLedger = NewSQLiteTaskLedger(db)
├── runtime.SetTaskLedger(taskLedger)
└── cognitiveAgent.SetTaskLedger(taskLedger)

Gateway.Start()
├── staleDetector = NewStaleDetector(taskLedger, 2min, 30s, callback)
└── staleDetector.Start()

Gateway.Stop()
└── staleDetector.Stop()
```

## 斜杠命令

### `/tasks` — 查看活跃任务

列出当前所有 running 和 pending 状态的任务：

```
📋 Active Tasks
──────────────────
▶ [running] req_1713420000 - 帮我重构这个模块
  ├ [completed] sub_1713420001 - PERCEIVE phase
  ├ [running] sub_1713420002 - ACT phase
  └ [pending] sub_1713420003 - REFLECT phase

Pending: 1 | Running: 2
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/taskledger/ledger.go` | 新增 | TaskState/TaskKind 枚举 + Task 结构 + TaskLedger 接口 |
| `internal/taskledger/store.go` | 新增 | SQLiteTaskLedger 实现（9 个方法） |
| `internal/taskledger/store_test.go` | 新增 | Register/Get、ClaimNext 原子性、Cancel 级联、GetTree、DetectStale、List 过滤 |
| `internal/taskledger/stale.go` | 新增 | StaleDetector 后台守护 |
| `internal/taskledger/stale_test.go` | 新增 | 检测、回调、优雅停止 |
| `internal/store/migrations/019_task_ledger.sql` | 新增 | task_ledger 表 + 3 个索引 |
| `internal/agent/runtime.go` | 修改 | HandleMessage 注册/完成用户请求任务 |
| `internal/agent/cognitive.go` | 修改 | 父任务 + registerSubtask 模式 |
| `internal/gateway/gateway.go` | 修改 | TaskLedger 创建/注入 + StaleDetector 启停 + /tasks 命令 |
| `internal/channel/tui/commands.go` | 修改 | 注册 /tasks 斜杠命令 |

## 测试

### store_test.go（6 个用例）

- `TestSQLiteTaskLedger_Register_Get` — 基础 CRUD
- `TestSQLiteTaskLedger_ClaimNext_Atomic` — 并发认领只返回一个
- `TestSQLiteTaskLedger_Cancel_Cascades` — 递归取消父子链
- `TestSQLiteTaskLedger_GetTree` — 递归查询完整任务树
- `TestSQLiteTaskLedger_DetectStale` — 心跳超时检测
- `TestSQLiteTaskLedger_List_Filter` — 按状态/类型/父任务过滤

### stale_test.go（3 个用例）

- `TestStaleDetector_DetectsAndFails` — 超时任务被标记为 failed
- `TestStaleDetector_CallbackInvoked` — onStale 回调被触发
- `TestStaleDetector_StopsGracefully` — Stop 不会 panic 或泄漏

## 数据库迁移

Migration 019（`task_ledger`）在启动时自动应用，无需手动干预。包含 3 个索引覆盖高频查询路径：
- `idx_task_ledger_state` — ClaimNext / List 按状态过滤
- `idx_task_ledger_parent` — GetTree / List 按父任务过滤
- `idx_task_ledger_kind` — ClaimNext 按类型过滤
