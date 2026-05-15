# Worktree Manager — Git Worktree-Based Code Isolation

**日期**: 2026-05-15
**范围**: 新增 `internal/worktree/` 包，为 agent 提供安全的 git worktree 隔离代码修改能力，对标 Claude Code/Codex 的 worktree 工作流。

## 概述

顶级 coding agent（Claude Code、Codex、Devin、Cursor）都在使用 git worktree 隔离代码修改：创建一个独立分支的临时工作区，agent 在其中自由修改文件，完成后 diff 审查、merge 回主分支。IronClaw 此前虽有 Docker 沙箱隔离 bash 执行，但文件修改直接发生在当前仓库，缺少安全网。

`WorktreeManager` 新增完整 worktree 生命周期管理 + 4 个 agent 工具 + Feature Registry 集成。

## 架构

### WorktreeManager

```go
type WorktreeManager struct {
    repoPath    string         // git 仓库根路径
    stagingRoot string         // .codex-staging/
}

type WorktreeInfo struct {
    Path      string    `json:"path"`
    Branch    string    `json:"branch"`
    HEAD      string    `json:"head"`
    IsBare    bool      `json:"is_bare"`
    IsLocked  bool      `json:"is_locked"`
    CreatedAt time.Time `json:"created_at"`
}
```

### API

| 方法 | 说明 |
|------|------|
| `NewWorktreeManager(repoPath)` | 验证 git 仓库，解析 toplevel |
| `Create(ctx, name)` | `git worktree add .codex-staging/<name> -b feature/<name>` |
| `GetDiff(ctx, wtPath)` | `git diff main..HEAD` 获取变更 |
| `MergeAndCleanup(ctx, wtPath, branch)` | checkout main → merge branch → git worktree remove |
| `List(ctx)` | `git worktree list --porcelain` 解析 |
| `CleanupOrphans(ctx)` | 清理无对应分支的孤立 worktree |
| `ValidatePath(path)` | 验证路径是否属于已知 worktree |

### 错误语义

- `ErrNotGitRepo` — 仓库路径不是 git 仓库
- `ErrWorktreeExists` — 同名 worktree 或分支已存在
- `ErrWorktreeNotFound` — 路径未匹配任何已知 worktree

### Agent 工具

| 工具 | 读写 | 需审批 | 输入 |
|------|------|--------|------|
| `worktree_create` | 写 | 是 | `{"name": "...", "base_branch": "main"}` |
| `worktree_diff` | 只读 | 否 | `{"path": "..."}` |
| `worktree_merge` | 写 | 是 | `{"path": "..."}` |
| `worktree_list` | 只读 | 否 | `{}` |

所有工具实现 `tool.Tool` 接口，支持 `ToolCapabilities` 声明（包括 `IsReadOnly`、`ParallelSafety`、`ApprovalMode`）。

### 工作流

```
Agent 收到 "修改 X 功能" 请求
     │
     ▼
[1] worktree_create("fix-x")  →  .codex-staging/fix-x/ @ feature/fix-x
     │
     ▼
[2] Agent 在 worktree 中自由执行 file_write / file_edit / bash
     │
     ▼
[3] worktree_diff(".codex-staging/fix-x")  → 审查变更
     │
     ▼
[4] User 审批变更
     │
     ▼
[5] worktree_merge(".codex-staging/fix-x")
     ├── git checkout main
     ├── git merge feature/fix-x
     └── git worktree remove
```

## Feature 集成

```go
r.Register(feature.Feature{
    Name:        "worktree",
    Default:     true,
    Phase:       feature.PhaseConstruct,
    AutoDetect:  func(ctx) { return worktree.Available() }, // 检测 git 是否在 PATH
})
```

工具在 `init_tools.go` 中注册：
```go
if gw.featureEnabled("worktree") {
    worktree.RegisterTools(gw.tools, ".")
}
```

## 文件

| 文件 | 说明 |
|------|------|
| `internal/worktree/manager.go` | WorktreeManager 核心 + git 命令封装 + porcelain 解析 |
| `internal/worktree/tool.go` | 4 个 agent 工具 + RegisterTools helper |
| `internal/worktree/tool_test.go` | 覆盖创建/diff/merge/list/错误路径 |
| `internal/gateway/features.go` | worktree feature 注册 + AutoDetect |
| `internal/gateway/init_tools.go` | 条件性工具注册 |

## 与 Sandbox 的关系

Worktree 隔离与 Docker 沙箱互补：
- **Worktree**：git 级别隔离——分支独立，merge 前不污染主分支
- **Docker Sandbox**：进程级别隔离——bash 命令在容器中执行
- 两者可叠加使用：agent 在 worktree 中修改文件，bash 在 Docker 容器中运行测试
