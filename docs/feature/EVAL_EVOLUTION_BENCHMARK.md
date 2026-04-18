# 进化基准测试闭环

**日期**: 2026-04-19
**范围**: 修复评估管道 7 个缺陷 + 工作负载注入进化压力 + 时间序列可视化 — 实现 "第 1 次 vs 第 100 次" 量化叙事

## 概述

此前的评估框架虽然 `CognitiveAgentRunner` 代码已就位，但存在多个阻塞性缺陷导致 `--live` 评估无法产出正确数据；`eval longitudinal` 命令在迭代之间没有进化压力，测量的是框架一致性而非进化效果；且缺乏时间序列数据结构和可视化能力。

本次改动分三个 Phase 完成闭环：

1. **Phase 1（缺陷修复）** — 修复 7 个阻塞 `--live` 评估的 Bug，新增 5 个集成测试
2. **Phase 2（进化压力注入）** — 新增 WorkloadSuite（22 个任务）+ `--with-workload` 工作负载注入 + 强制 insights 学习周期
3. **Phase 3（时间序列可视化）** — `LongitudinalReport` 结构化数据 + `eval visualize` 生成 Chart.js 图表

## 背景与动机

IronClaw 的三回路自进化（偏好学习 / 技能合成 / 策略优化）在架构完整度上是同类项目最高的。但 "自进化真的让 Agent 变好了" 这个核心叙事需要数据支撑。在本次改动之前：

- `eval run --live` 在无进化引擎时所有任务 Success 永远为 false（逻辑 Bug）
- `eval longitudinal` 连跑 N 次但迭代间无学习——测不到进化
- 结果只有 first-vs-last Markdown 对比，没有时间序列数据或图表
- `insights health` 的策略版本和断言通过率字段为空

改动后的完整使用流程：

```bash
# 1. 带进化压力的纵向测试（10 轮 benchmark + 工作负载注入 + 强制学习）
ironclaw eval longitudinal \
  --suite evolution \
  --with-workload workload \
  --live -n 10 \
  --output-dir eval_demo/

# 2. 生成可视化
ironclaw eval visualize \
  -i eval_demo/longitudinal_report.json \
  -o eval_demo/evolution_chart.html

# 3. 浏览器打开图表
open eval_demo/evolution_chart.html
```

---

## Phase 1：缺陷修复（7 项）

### 缺陷 1：无进化引擎时 Success 永远为 false

**文件**: `internal/eval/cognitive_runner.go`

**问题**: `populateFromEvolution` 是设置 `Success` 和 `Confidence` 的唯一路径。当 `EvalHook` 未注册（无进化引擎配置）时，该方法直接 return，导致所有任务的 `Success` 保持 Go 零值 `false`。

**修复**: 新增 `populateSuccessFallback` 方法，在 `populateFromEvolution` 之后调用：

```go
func (r *CognitiveAgentRunner) populateSuccessFallback(result *EvalResult) {
    if r.hook != nil {
        return // 进化 hook 已设置 Success
    }
    if result.Error != "" {
        return // 硬错误 — 保持 Success=false
    }
    if obs != nil && result.AssertionTotal > 0 {
        result.Success = result.AssertionPassRate >= 0.8
        result.Confidence = result.AssertionPassRate
        return
    }
    // 无断言且无错误 — 视为成功
    result.Success = true
    result.Confidence = 0.5
}
```

RunTask 调用顺序变为：
```
populateFromObservation → populateFromEvolution → populateSuccessFallback
```

### 缺陷 2：EvolutionSnapshot 不完整

**文件**: `internal/eval/cognitive_runner.go` + `internal/evolution/engine.go` + `synthesizer.go` + `trajectory.go`

**问题**: `CaptureSnapshot()` 中 `SkillDraftCount` 和 `TrajectoryCount` 始终为 0，从未被填充。`EvoBefore` / `EvoAfter` 快照数据残缺。

**修复**:

1. Engine 新增两个 accessor（匹配已有的 `PreferenceLearnerHook` / `StrategyOptimizerHook` 模式）：
   - `SkillSynthesizerHook() *SkillSynthesizer`
   - `TrajectoryRecorderHook() *TrajectoryRecorder`
   - `TrajectoryDir() string`

2. SkillSynthesizer 新增 `DraftCount() int`（返回已生成的草稿数量）

3. TrajectoryRecorder 新增 `Dir() string`（返回轨迹目录路径）

4. `CaptureSnapshot` 补全：

```go
if ss := evo.SkillSynthesizerHook(); ss != nil {
    snap.SkillDraftCount = ss.DraftCount()
}
if tr := evo.TrajectoryRecorderHook(); tr != nil {
    dir := tr.Dir()
    if dir != "" {
        since := time.Now().Add(-24 * time.Hour)
        if records, err := evolution.ReadTrajectories(dir, since, time.Now()); err == nil {
            snap.TrajectoryCount = len(records)
        }
    }
}
```

### 缺陷 3：轨迹分发时序竞态

**文件**: `internal/agent/cognitive.go`

**问题**: `dispatchEvolutionEvents` 中调用顺序为 `DispatchReflection`（异步）→ `DispatchEpisode`（异步）→ `DispatchToolExec`（同步）。`TrajectoryRecorder` 的 `OnToolExecuted` 负责缓冲 per-tool 数据，`OnEpisodeComplete` 负责刷写。但 Episode 先于 ToolExec 分发，导致刷写时 buffer 为空，回退到粗粒度 `ToolSequence`（所有工具标记为 `Succeeded: true`）。

Engine 注释声称 `DispatchToolExec` 同步是为了 "在 episode 分发前填充 buffer"，但调用方代码与此意图矛盾。

**修复**: 重排分发顺序为：

```
1. DispatchToolExec   (同步 — 阻塞直到所有 tool buffer 填充完成)
2. DispatchReflection (异步)
3. DispatchEpisode    (异步 — 此时 tool buffer 已就绪)
```

### 缺陷 4：CognitiveAgentRunner 无集成测试

**文件**: `internal/eval/cognitive_runner_test.go`

**问题**: 测试文件仅覆盖 `EvalChannel` 和 `EvalHook` 的独立功能，没有验证数据填充管道（`populateFromObservation` / `populateFromEvolution`）的端到端正确性。

**修复**: 新增 5 个测试：

| 测试 | 验证内容 |
|------|---------|
| `TestPopulateFromObservation` | 断言计数、通过率、工具去重 |
| `TestPopulateFromObservation_NilObservation` | nil observation 的优雅处理 |
| `TestPopulateFromEvolution` | 反思事件 → Success/Confidence/ReplanCount 填充 |
| `TestPopulateFromEvolution_NoHook` | nil hook 时不修改 result |
| `TestPopulateFromEvolution_EpisodeFallback` | 无反思事件时 Episode 回退路径 |

### 缺陷 5：insights health 指标不完整

**文件**: `cmd/ironclaw/insights.go`

**问题**:
- `buildHealthFromTrajectories` 从不调用 `c.SetStrategyVersion()` — 报告中策略版本永远为 0
- 从不调用 `c.RecordAssertionRate()` — 断言通过率 0 样本

**修复**:
1. 每个 episode 的工具成功率派生为断言通过率：`RecordAssertionRate(passed/total)`
2. 新增 `loadStrategyVersion()` 从 `~/.IronClaw/evolution/strategy.yaml` 读取当前版本

---

## Phase 2：工作负载注入与进化压力

### 核心设计思路

**问题**: 原 `eval longitudinal` 只是把同一批 benchmark 连跑 N 次。迭代之间没有新任务流入，进化引擎收不到 episode，策略/偏好/技能都不会更新。这测量的是框架一致性，不是进化效果。

**解决方案**: 在每轮 benchmark 评估之间注入工作负载任务（workload），产生真实的轨迹记录，然后强制触发 insights 学习周期。

```
for i := 1..N:
    ┌───────────────────────────────┐
    │  RunSuite(benchmark_tasks)    │ ← 评估（记录指标）
    │  → 保存 iter-{i}.json         │
    │  → 记录 IterationPoint        │
    └───────────┬───────────────────┘
                │ (非最后一轮)
    ┌───────────▼───────────────────┐
    │  RunSuite(workload_tasks)     │ ← 进化压力（产生轨迹）
    │  → 不保存结果，只产生轨迹      │
    └───────────┬───────────────────┘
                │
    ┌───────────▼───────────────────┐
    │  evo.WaitPending()            │ ← 等待异步 hook 完成
    │  evo.RunInsightsCycle()       │ ← 强制学习（更新策略/偏好）
    └───────────────────────────────┘
```

### WorkloadSuite（22 个任务）

新增 `WorkloadSuite()` 返回 22 个多样化任务，专为触发进化信号设计。分为 5 个类别：

| 类别 | 数量 | 设计目标 | 进化信号 |
|------|------|---------|---------|
| **Bash 热身** | 5 | 高成功率的简单命令 | 偏好学习：建立 bash 工具置信度 |
| **文件操作链** | 5 | write→read→modify→verify 模式 | 技能合成：重复模式检测 → 草稿生成 |
| **故意失败+恢复** | 5 | 权限拒绝、路径不存在、语法错误 | 策略优化：重规划阈值调优 |
| **多工具组合** | 4 | 3+ 工具协作完成复杂任务 | 全三回路：丰富轨迹数据 |
| **歧义/模糊指令** | 3 | 不确定性高的开放式任务 | 策略优化：规划能力评估 |

任务 ID 统一以 `wl-` 前缀标识。所有任务都有 `workload` tag。

### Engine.RunInsightsCycle 导出

**文件**: `internal/evolution/engine.go`

将 `runInsightsCycle` 重命名为 `RunInsightsCycle`（导出），允许外部代码（如 `eval longitudinal`）在需要时手动触发学习周期，而非等待 6 小时定时器。`insightsLoop` 内部调用同步更新。

### Gateway.EvolutionEngine 访问器

**文件**: `internal/gateway/gateway.go`

新增 `EvolutionEngine() *evolution.Engine` 方法，暴露进化引擎给 CLI 使用。配合 `RunInsightsCycle` 实现 CLI 端的强制学习触发。

### eval longitudinal 增强

**文件**: `cmd/ironclaw/eval.go`

`newEvalLongitudinalCmd` 重写，新增两个标志：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--with-workload` | (空) | 工作负载套件名称或 JSON 文件路径，在 benchmark 迭代之间注入 |
| `--force-insights` | `true` | 每轮工作负载注入后强制触发 insights 学习周期 |

新增行为：
1. 每轮评估后构建 `IterationPoint`，记录 `SuiteSummary` + `EvolutionSnapshot`
2. 非最后一轮时运行工作负载任务（失败不中断流程）
3. 工作负载完成后调用 `evo.WaitPending()` + `evo.RunInsightsCycle()`
4. 所有迭代完成后生成 `LongitudinalReport` 并保存为 `longitudinal_report.json`

---

## Phase 3：时间序列可视化

### LongitudinalReport 类型

**文件**: `internal/eval/harness.go`

新增两个结构体和配套方法：

```go
type IterationPoint struct {
    Iteration       int          `json:"iteration"`
    RunID           string       `json:"run_id"`
    Timestamp       time.Time    `json:"timestamp"`
    Summary         SuiteSummary `json:"summary"`
    StrategyVersion int          `json:"strategy_version"`
    PreferenceCount int          `json:"preference_count"`
    SkillDraftCount int          `json:"skill_draft_count"`
    TrajectoryCount int          `json:"trajectory_count"`
}

type LongitudinalReport struct {
    Iterations  []IterationPoint `json:"iterations"`
    First       SuiteSummary     `json:"first"`
    Last        SuiteSummary     `json:"last"`
    Deltas      ComparisonDelta  `json:"deltas"`
    GeneratedAt time.Time        `json:"generated_at"`
}
```

`NewLongitudinalReport(points)` 自动计算 first-vs-last delta。`SaveJSON` / `LoadLongitudinalReport` 提供 JSON 持久化。

### eval visualize 命令

**文件**: `cmd/ironclaw/eval_visualize.go`（新增）

读取 `longitudinal_report.json`，生成自包含 HTML 页面（内嵌 Chart.js 4.4.0）：

```bash
ironclaw eval visualize -i longitudinal_report.json -o evolution_chart.html
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--input` / `-i` | `longitudinal_report.json` | 纵向报告 JSON 文件 |
| `--output` / `-o` | `evolution_chart.html` | 输出 HTML 文件 |

#### 图表设计

页面包含两个并排图表，深色主题：

**左侧图表（Performance）— 双 Y 轴**:
- 左 Y 轴 (0-100%)：成功率（绿色线）+ 断言通过率（蓝色线）
- 右 Y 轴（整数）：策略版本（橙色阶梯虚线）

**右侧图表（Behavior）— 双 Y 轴**:
- 左 Y 轴 (0-1)：平均置信度（紫色线）
- 右 Y 轴（数值）：平均重规划次数（红色线）

图表下方为 Delta 摘要框，显示 first-vs-last 的变化总结。

---

## 完整数据流

```
ironclaw eval longitudinal --live --with-workload workload -n 10

  ┌──────────┐     ┌──────────┐     ┌──────────┐
  │ iter-001 │     │ iter-002 │     │ iter-010 │
  │ benchmark│     │ benchmark│     │ benchmark│
  │ (6 tasks)│     │ (6 tasks)│     │ (6 tasks)│
  └────┬─────┘     └────┬─────┘     └──────────┘
       │                 │
  ┌────▼─────┐     ┌────▼─────┐
  │ workload │     │ workload │           (最后一轮无 workload)
  │(22 tasks)│     │(22 tasks)│
  └────┬─────┘     └────┬─────┘
       │                 │
  ┌────▼─────┐     ┌────▼─────┐
  │ insights │     │ insights │
  │  cycle   │     │  cycle   │
  └──────────┘     └──────────┘

输出:
  eval_demo/
  ├── iter-001.json              ← 每轮 benchmark 的 SuiteResult
  ├── iter-002.json
  ├── ...
  ├── iter-010.json
  ├── longitudinal_report.json   ← 时间序列 (IterationPoint[])
  ├── comparison.md              ← first-vs-last Markdown 对比
  └── evolution_chart.html       ← Chart.js 可视化 (eval visualize)
```

## CLI 命令族更新

```
ironclaw eval
├── run            # 单次评估（dry / --live）
├── compare        # 两次评估对比（Markdown / --json）
├── list           # 列出任务集（builtin / evolution / workload / all）
├── longitudinal   # 纵向追踪（支持 --with-workload / --force-insights）
└── visualize      # 生成 HTML 可视化图表
```

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/eval/cognitive_runner.go` | 修改 | Phase 1: Success 回退 + EvolutionSnapshot 补全 |
| `internal/eval/cognitive_runner_test.go` | 修改 | Phase 1: 新增 5 个集成测试 |
| `internal/eval/fixtures.go` | 修改 | Phase 2: WorkloadSuite (22 任务) + AllSuites 注册 |
| `internal/eval/harness.go` | 修改 | Phase 3: IterationPoint + LongitudinalReport + SaveJSON/Load |
| `internal/agent/cognitive.go` | 修改 | Phase 1: 轨迹分发时序重排 |
| `internal/evolution/engine.go` | 修改 | Phase 1: accessor 新增; Phase 2: RunInsightsCycle 导出 |
| `internal/evolution/synthesizer.go` | 修改 | Phase 1: DraftCount() 方法 |
| `internal/evolution/trajectory.go` | 修改 | Phase 1: Dir() 方法 |
| `internal/gateway/gateway.go` | 修改 | Phase 2: EvolutionEngine() 访问器 |
| `cmd/ironclaw/eval.go` | 修改 | Phase 2: longitudinal 重写 + visualize 注册 |
| `cmd/ironclaw/eval_visualize.go` | 新增 | Phase 3: eval visualize 命令 + Chart.js HTML 模板 |
| `cmd/ironclaw/insights.go` | 修改 | Phase 1: StrategyVersion + AssertionRate 补全 |

## 测试

全部通过（`go build` + `go test`）：

| 包 | 测试数 | 结果 |
|----|--------|------|
| `internal/eval/` | 18 (12 已有 + 5 新增 + 1 框架) | PASS |
| `internal/evolution/` | 全量 (82+) | PASS |
| `internal/agent/` | 全量 | PASS |
| 全项目编译 | `go build ./cmd/ironclaw/` | PASS |
| E2E dry-run | `eval longitudinal --with-workload` | PASS |
| E2E visualize | `eval visualize -i report.json` | PASS |

## 与前序文档关系

- [EVAL_HARNESS.md](EVAL_HARNESS.md) — 评估框架基础（Phase 0）
- [EVAL_LIVE_MODE.md](EVAL_LIVE_MODE.md) — CognitiveAgentRunner + EvolutionSuite + longitudinal 初版
- [EVOLUTION_LOOP_CLOSURE.md](EVOLUTION_LOOP_CLOSURE.md) — 三回路自进化 10 项断裂修复
- **本文档** — 评估管道缺陷修复 + 工作负载进化压力 + 时间序列可视化
