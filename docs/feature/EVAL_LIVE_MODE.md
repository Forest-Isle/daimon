# 评估框架实时模式与纵向追踪

**日期**: 2026-04-19
**范围**: 闭合评估回路 — CognitiveAgentRunner 实现 + 进化专用任务集 + 纵向评估命令

## 概述

此前的评估框架仅提供 `DryRunner`（模拟运行器），无法驱动真实的认知 Agent 执行评估任务。`CognitiveAgentRunner`（文档中标注为"未来集成"）是自进化效果量化的关键缺失环节：没有它，"Agent 在第 1 次 vs 第 100 次使用时变好了" 这个核心叙事无法用数据支撑。

本次改动实现了三个关键组件：
1. **CognitiveAgentRunner** — 将 eval 框架与真实认知 Agent 连接
2. **EvolutionSuite** — 专门设计的重规划压力测试任务集
3. **`eval longitudinal`** — 纵向追踪命令，自动跑 N 轮并生成进化对比报告

## Part A: CognitiveAgentRunner

### 架构

```
                          CognitiveAgentRunner
                          ┌─────────────────────────┐
TaskCase ──► RunTask() ──►│  EvalChannel             │
                          │  ├─ auto-approve tools   │
                          │  ├─ auto-continue replan  │
                          │  └─ capture messages      │
                          │                           │
                          │  CognitiveAgent           │
                          │  └─ HandleMessage()       │
                          │     ├─ PERCEIVE           │
                          │     ├─ PLAN               │
                          │     ├─ ACT                │
                          │     ├─ OBSERVE ──callback──┼──► ObservationResult
                          │     └─ REFLECT            │       (assertions)
                          │                           │
                          │  EvalHook (evolution.Hook) │
                          │  ├─ OnReflection ─────────┼──► succeeded, confidence
                          │  ├─ OnEpisode ────────────┼──► replan_count, duration
                          │  └─ OnToolExecuted ───────┼──► tool reliability
                          └─────────────────────────┘
                                      │
                                      ▼
                                  EvalResult
```

### 核心组件

#### EvalChannel（`eval_channel.go`）

无头通道适配器，实现 `channel.Channel` + `ApprovalSender` + `ReflectionSender`：

| 接口方法 | 行为 |
|---------|------|
| `Send` | 捕获消息到内部缓冲区 |
| `SendStreaming` | 返回 no-op 的 `StreamUpdater`，Finish 时捕获最终消息 |
| `SendApprovalRequest` | 始终返回 `true`（自动批准所有工具调用） |
| `SendReflectionRequest` | 始终返回 `ReplanContinue`（自动继续重规划） |
| `Messages()` / `LastMessage()` | 读取捕获的消息 |
| `Reset()` | 清空缓冲区（任务间重置） |

#### EvalHook（`eval_hook.go`）

实现 `evolution.Hook` 接口，按 session ID 隔离捕获的指标：

```go
type EvalHook struct {
    reflections map[string]*evolution.ReflectionEvent  // sessionID → 最近反思
    episodes    map[string]*evolution.EpisodeEvent      // sessionID → 最近 episode
    toolExecs   map[string][]evolution.ToolExecEvent    // sessionID → 工具执行记录
}
```

Runner 在每个任务前 `ClearSession()`，任务后通过 `GetReflection()` / `GetEpisode()` 读取指标。

#### CognitiveAgent 扩展

为支持评估框架，`CognitiveAgent` 新增三个导出接口：

```go
func (ca *CognitiveAgent) SetObservationCallback(fn func(result *ObservationResult))
func (ca *CognitiveAgent) EvolutionEngine() *evolution.Engine
func (ca *CognitiveAgent) Sessions() *session.Manager
```

`observationCallback` 在 OBSERVE 阶段完成后立即触发（`cognitive.go` 第 450 行），EvalRunner 通过它获取断言统计数据——这是唯一无法通过 evolution hooks 获取的信息。

#### 数据收集流

RunTask 执行后，EvalResult 的填充分两个来源：

| 数据 | 来源 | 方法 |
|------|------|------|
| AssertionTotal / Passed / PassRate | ObservationResult (callback) | `populateFromObservation` |
| ToolsUsed | ObservationResult.Observations | `populateFromObservation` |
| Success / Confidence / ReplanCount | ReflectionEvent (EvalHook) | `populateFromEvolution` |
| Duration | time.Since(start) | 直接测量 |

### Gateway 集成

`Gateway` 新增 `NewEvalRunner()` 方法，返回 `*eval.CognitiveAgentRunner`（认知模式未启用时返回 `nil`）。CLI 通过 `initEvalGateway()` 启动完整的 Gateway 栈（DB、LLM、工具、认知 Agent），然后获取 runner。

### CLI: `--live` 模式

```bash
# 模拟运行（默认，不需要 LLM 凭证）
ironclaw eval run --suite builtin

# 实时运行（驱动真实认知 Agent）
ironclaw eval run --suite builtin --live --config configs/ironclaw.yaml --output baseline.json
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--live` | `false` | 启用实时评估模式 |
| `--config` / `-c` | `configs/ironclaw.yaml` | 配置文件路径（仅 `--live` 时使用） |

## Part B: 进化专用任务集

### EvolutionSuite

6 个专门设计的任务，特点是：
- 包含歧义性指令（触发规划不确定性）
- 包含故意的错误条件（触发重规划）
- 多步依赖链（测试级联失败恢复）

| 任务 ID | 复杂度 | 特性 | 说明 |
|---------|--------|------|------|
| `evo-ambiguous-path` | moderate | 歧义路径 | 在多个可能位置查找配置文件 |
| `evo-wrong-tool-recovery` | moderate | 条件分支 | 根据磁盘使用率决定输出内容 |
| `evo-multi-attempt-fix` | complex | 代码验证 | 写 Python 脚本并验证输出精确值 |
| `evo-cascading-deps` | complex | 级联依赖 | 三个文件的依赖链（a→b→c） |
| `evo-permission-boundary` | moderate | 权限边界 | 遇到权限拒绝后的恢复 |
| `evo-iterative-refinement` | complex | 精确匹配 | 输出必须精确匹配（有 SuccessFunc 验证） |

### AllSuites 注册表

```go
func AllSuites() map[string]func() []TaskCase {
    return map[string]func() []TaskCase{
        "builtin":   BuiltinSuite,
        "evolution": EvolutionSuite,
    }
}
```

CLI 通过 `loadSuite(name)` 先查注册表，再回退到 JSON 文件。

## Part C: 纵向追踪

### `ironclaw eval longitudinal`

```bash
# 模拟纵向追踪（验证框架）
ironclaw eval longitudinal --suite evolution -n 5

# 真实纵向追踪（Agent 每处理 50 个真实任务后跑一次）
ironclaw eval longitudinal --suite evolution -n 1 --live --output-dir eval_data/
```

执行流程：

```
for i := 1..N:
    RunSuite(runID=iter-001, tasks, runner)
    → 保存 output_dir/iter-001.json
    → 打印单轮摘要

Compare(results[0], results[N-1])
→ 打印 first-vs-last Markdown 报告
→ 保存 output_dir/comparison.md
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--suite` | `evolution` | 任务集（默认使用进化专用集） |
| `--output-dir` | 自动生成 | 结果输出目录 |
| `-n` / `--iterations` | `3` | 评估迭代次数 |
| `--live` | `false` | 实时模式 |
| `--config` / `-c` | `configs/ironclaw.yaml` | 配置文件 |

### 使用场景

**场景 1：基线 → 进化后对比**
```bash
# 1. 跑基线
ironclaw eval run --suite evolution --live -o baseline.json

# 2. 让 Agent 处理真实任务 1 周...

# 3. 跑进化后评估
ironclaw eval run --suite evolution --live -o after.json

# 4. 对比
ironclaw eval compare --before baseline.json --after after.json
```

**场景 2：连续纵向追踪**
```bash
# 每天跑一次，追踪策略版本演进
ironclaw eval longitudinal --suite evolution -n 1 --live --output-dir eval_daily/
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/eval/eval_channel.go` | 新增 | EvalChannel — 无头通道适配器 |
| `internal/eval/eval_hook.go` | 新增 | EvalHook — 进化事件捕获器 |
| `internal/eval/cognitive_runner.go` | 新增 | CognitiveAgentRunner — 真实 Agent 运行器 |
| `internal/eval/cognitive_runner_test.go` | 新增 | 7 个测试覆盖 EvalChannel / EvalHook |
| `internal/eval/fixtures.go` | 修改 | 新增 EvolutionSuite (6 任务) + AllSuites 注册表 |
| `internal/agent/cognitive.go` | 修改 | 新增 SetObservationCallback / EvolutionEngine / Sessions |
| `internal/gateway/gateway.go` | 修改 | 新增 NewEvalRunner + 导入 eval 包 |
| `cmd/ironclaw/eval.go` | 修改 | --live / --config 支持 + longitudinal 命令 + loadSuite 重构 |

## 测试

19 个测试全部通过（7 个新增 + 12 个已有）：

**新增**：
- `TestEvalChannel_AutoApproves` — 工具自动批准
- `TestEvalChannel_CapturesMessages` — 消息捕获
- `TestEvalChannel_Reset` — 任务间重置
- `TestEvalChannel_StreamUpdater` — 流式消息捕获
- `TestEvalHook_CapturesEvents` — 反思/Episode/工具事件捕获
- `TestEvalHook_ClearSession` — Session 数据清理
- `TestEvalHook_IsolatesSessions` — 跨 Session 隔离
