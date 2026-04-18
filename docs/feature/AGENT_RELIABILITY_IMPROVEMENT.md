# Agent 可靠性改进

**日期**: 2026-04-18
**范围**: 三条并行改进轨道，缩小与前沿 Agent 项目的差距

## 概述

本次发布跨三条独立改进轨道引入 9 项新能力，分 4 个阶段实施，共 20 次提交（+3,400 行代码，涉及 33 个文件）。所有改动集中在认知 Agent 路径和工具层，零新增外部依赖。

## Track A: 长任务可靠性

### A1. 任务检查点（Checkpoint/Resume）

中途中断的任务现在可以从上一个已完成的子任务恢复执行。

- 新增 SQLite 表 `task_checkpoints`（migration 018）存储执行状态
- `CognitiveAgent` 在每个 OBSERVE 阶段后写入检查点，序列化完整的 `ObservationResult`（包含断言和失败上下文）及 `TaskPlan`
- `/resume [session_id]` 斜杠命令从最近的检查点恢复执行
- 任务成功完成后自动清除检查点

**涉及文件**: `internal/agent/checkpoint.go`, `internal/store/migrations/018_task_checkpoints.sql`, `internal/channel/tui/commands.go`

### A2. 结构化验证（断言循环）

OBSERVE 阶段现在根据工具类型自动生成断言，捕获静默失败。

| 工具 | 断言内容 |
|------|---------|
| `bash` | `exit_code == 0`，`stderr 无错误关键词` |
| `http` | `status_code` 在 100–399 范围内 |
| `file_write` / `file_edit` | 操作成功（无错误） |

断言失败会生成结构化的 `FailureContext` 条目，包含类型化的错误分类（`FailureAssertionFailed`、`FailureToolError`、`FailureDenied`），直接注入 REFLECT 阶段驱动精准重规划。

**涉及文件**: `internal/agent/assertion.go`, `internal/agent/observe.go`, `internal/agent/cognitive_types.go`

### A3. 上下文感知的智能重试

REFLECT 阶段现在接收结构化的失败上下文，而非原始观察结果的文本转储。

- `FailureContext` 携带子任务 ID、工具名称、错误类型、尝试次数和逐断言详情
- REFLECT 提示词中的 `{{FAILURE_CONTEXT}}` 模板明确告知 LLM 什么失败了、为什么失败、失败了多少次
- 同类型失败达 3 次后，触发降级警告建议使用更保守的工具选择
- `replanAttempt` 计数器贯穿整个 OBSERVE → REFLECT → 重规划循环

**涉及文件**: `internal/agent/failure_context.go`, `internal/agent/reflect.go`, `internal/agent/cognitive_prompts.go`

## Track B: 工具质量

### B1. 结构化 Bash 输出

Bash 工具现在返回结构化 JSON 而非原始文本：

```json
{"stdout":"...","stderr":"...","exit_code":0,"status":"ok","duration_ms":42,"truncated":false}
```

- `status` 为 `"ok"` 或 `"failed"` —— 为 Observer 断言提供明确信号
- 输出超过 8KB 时写入临时文件，返回文件路径而非内联内容（节省上下文 Token）
- `Result.Metadata` 携带 `exit_code`、`status`、`duration_ms` 供下游消费者使用

**涉及文件**: `internal/tool/bash.go`

### B2. 浏览器搜索与提取工具

两个新的浏览器工具，用于结构化的 Web 交互：

- **`browser_search`** — 查询 → `[{title, url, snippet}]` 结构化结果（DuckDuckGo HTML 端点，基于正则的解析，含降级回退）
- **`browser_extract`** — URL → Readability 风格的 Markdown（去除导航/广告/样板内容，按 4KB/页分页输出）

两者均实现 `IsReadOnly` 和 `Capabilities` 接口，在 `tools.browser.enabled` 下注册。

**涉及文件**: `internal/tool/browser_search.go`, `internal/tool/browser_extract.go`, `internal/gateway/init_tools.go`

### B3. 工具结果缓存

按任务粒度的只读工具结果缓存，消除冗余的 Token 消耗。

- 缓存键: `tool_name:sha256(input)`，生命周期: 单个任务
- 缓存工具: 所有实现 `IsReadOnly() == true` 的工具（`file_read`、`file_list`、`http` GET、浏览器工具）
- 自动失效: 实现 `PathScopedTool` 接口的写工具会驱逐受影响路径的缓存读结果
- 线程安全: `sync.RWMutex` 支持并发子任务执行

**涉及文件**: `internal/agent/tool_cache.go`, `internal/agent/act.go`

## Track C: 上下文智能

### C1. 项目上下文自动注入

PERCEIVE 阶段现在自动检测工作目录的项目类型，并将上下文注入 PLAN 提示词。

| 检测文件 | 语言 | 提取信息 |
|---------|------|---------|
| `go.mod` | Go | 模块名、`go build/test` 命令 |
| `package.json` | JavaScript | 包名、npm scripts |
| `Cargo.toml` | Rust | crate 名、cargo 命令 |
| `pyproject.toml` | Python | 项目名、pytest |
| `Makefile` | — | build/test/lint/run 目标 |

同时扫描 `README.md` 和关键目录（`cmd/`、`src/`、`internal/`、`pkg/` 等）。结果按目录缓存。

**涉及文件**: `internal/agent/project_scanner.go`, `internal/agent/perceive.go`, `internal/agent/plan.go`

### C2. Git 状态感知

认知 Agent 现在了解当前的 Git 上下文，防止在错误分支上执行代码任务。

通过 `{{GIT_STATE}}` 模板注入 PLAN 提示词：
- 当前分支
- 未提交文件（来自 `git status --short`）
- 最近 5 次提交（来自 `git log --oneline -5`）

为认知路径构建专用的 `GitContextProvider`（已有的 `hook/injector_git.go` 仅服务于简单 Agent 模式）。

**涉及文件**: `internal/agent/git_context.go`, `internal/agent/perceive.go`, `internal/agent/cognitive_prompts.go`

### C3. 动态上下文预算

上下文注入现在具备复杂度感知能力，防止简单任务浪费 Token 以及复杂任务上下文不足。

| 复杂度 | 记忆 | 知识库分块 | 图谱 | 项目上下文 | Git |
|-------|------|-----------|------|-----------|-----|
| 简单 | top-3 | 无 | 否 | 是 | 否 |
| 中等 | top-5 | top-3 | 否 | 是 | 否 |
| 复杂 | top-10 | top-5 | 是 | 是 | 是 |

分配器读取 Perceiver 现有的复杂度分类，在 `CognitiveState` 传递给 Planner 之前进行裁剪。

**涉及文件**: `internal/agent/context_budget.go`, `internal/agent/perceive.go`

## 实施阶段

| 阶段 | 内容 | 理由 |
|------|------|------|
| 1 | A2（断言）+ B1（结构化 Bash） | 影响最大、风险最低 —— 后续阶段的基础 |
| 2 | A3（智能重试）+ C1（项目上下文） | 构建于 A2 之上；C1 独立 |
| 3 | B2（浏览器）+ B3（缓存）+ C2（Git） | 可并行；B3 需要 B1 先完成 |
| 4 | A1（检查点）+ C3（预算） | 最复杂；受益于前期所有工作 |

## 集成验证

全部 44 个集成检查点通过：

- **端到端链路**: ACT → OBSERVE（断言）→ REFLECT（失败上下文）→ 重规划循环
- **模板管线**: PERCEIVE（扫描 + Git + 预算）→ `{{PROJECT_CONTEXT}}` + `{{GIT_STATE}}` + `{{FAILURE_CONTEXT}}` → PLAN/REFLECT
- **横切关注点**: `SetMemoryStore` 在 Perceiver 重建时保留全部 6 个注入组件（searcher、graph、scanner、gitProvider、budgetAlloc、rlPolicy）
- **检查点生命周期**: 保存（完整 ObservationResult）→ `/resume`（Load + 重新执行）→ 删除（成功时）

## 新增斜杠命令

| 命令 | 说明 |
|------|------|
| `/resume [session_id]` | 从上一个检查点恢复任务 |

## 数据库迁移

Migration 018（`task_checkpoints`）在启动时自动应用，无需手动干预。

## 测试

所有新代码遵循 TDD，具有全面的测试覆盖：
- `internal/agent/`: 断言、观察器、失败上下文、项目扫描器、Git 上下文、上下文预算、检查点、工具缓存
- `internal/tool/`: Bash 结构化输出、浏览器搜索、浏览器提取
