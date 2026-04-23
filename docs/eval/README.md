# IronClaw Agent 评测系统深度解析

> 基于源码精确分析，版本日期：2026-04-22

---

## 目录

1. [总体架构](#1-总体架构)
2. [核心概念速览](#2-核心概念速览)
3. [完整评测流水线](#3-完整评测流水线)
4. [模块地图](#4-模块地图)
5. [两种运行模式](#5-两种运行模式)
6. [进化系统与评测的双向耦合](#6-进化系统与评测的双向耦合)
7. [评测维度分类体系](#7-评测维度分类体系)
8. [数据流与持久化](#8-数据流与持久化)
9. [配置参数速查](#9-配置参数速查)
10. [子模块文档索引](#10-子模块文档索引)

---

## 1. 总体架构

IronClaw 的评测系统是一个**多层次、自闭环**的 Agent 能力度量框架，覆盖从单次任务执行到跨轮次纵向学习分析的完整链路。

```
┌─────────────────────────────────────────────────────────────────────┐
│                        评测入口（CLI / CI）                          │
│   eval run │ eval longitudinal │ eval diagnose │ eval adaptive       │
└──────────────────────────┬──────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     任务套件加载（loadSuite）                         │
│   命名套件（builtin/evolution/full/...）  │  外部文件（.yaml/.json）  │
└──────────────────────────┬──────────────────────────────────────────┘
                           │  []TaskCase
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      RunSuite / RunSuiteWithOptions                  │
│                         （harness.go）                               │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  per TaskCase:                                               │   │
│  │  SetupFunc → AgentRunner.RunTask → CleanupFunc → SuccessFunc │   │
│  └──────────────────────────────────────────────────────────────┘   │
└──────────┬──────────────────────────────┬───────────────────────────┘
           │                              │
     DryRunner                   CognitiveAgentRunner
  （合成结果，CI 默认）          （真实 LLM + CognitiveAgent）
           │                              │
           │                   ┌──────────┴──────────────┐
           │                   │     EvalChannel          │
           │                   │  （自动审批工具调用）      │
           │                   └──────────┬──────────────┘
           │                              │
           │                   ┌──────────┴──────────────┐
           │                   │     EvalHook             │
           │                   │ （挂载进化引擎，捕获指标）  │
           │                   └──────────┬──────────────┘
           │                              │
           └──────────────┬───────────────┘
                          │  *EvalResult
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         评分流水线                                    │
│   VerifyReference（规则检查） ─→ LLMJudge（Rubric评分） ─→ FinalScore │
└──────────────────────────┬──────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      SuiteResult 汇总                                │
│  Results[] │ EvoBefore/EvoAfter │ CogHealth │ FeatureState           │
└──────┬─────────────┬────────────────────┬───────────────────────────┘
       │             │                    │
       ▼             ▼                    ▼
  JSON 持久化   Compare（回归检测）   Diagnosis（弱点分析）
       │             │                    │
       ▼             ▼                    ▼
  Longitudinal   CI 失败判定       WeaknessReport + HTML
  Report + HTML                         │
       │                                ▼
       ▼                         AdaptiveLoop（自适应补强）
 LearningMetrics
 （学习曲线分析）
```

**设计哲学**：

- **解耦隔离**：评测专用 Gateway（`initEvalGateway`）强制 cognitive 模式 + 临时内存目录，保证结果与生产会话完全隔离
- **干湿分离**：DryRunner（无 LLM）用于 CI 快速验证，CognitiveAgentRunner（真实 LLM）用于深度评测
- **进化感知**：评测本身是自进化系统的驱动器——每次 live 运行后进化子系统的状态变化被 `EvolutionSnapshot` 精确量化

---

## 2. 核心概念速览

| 概念 | 类型 | 位置 | 说明 |
|------|------|------|------|
| `TaskCase` | struct | `harness.go` | 单条评测任务，含目标、复杂度、验证方式、维度等 |
| `EvalResult` | struct | `harness.go` | 单任务执行结果，含成功/失败、工具调用、Assertion 率、奖励值等 |
| `SuiteResult` | struct | `harness.go` | 一次 suite 运行的完整结果，含进化快照和认知健康报告 |
| `EvolutionSnapshot` | struct | `harness.go` | 进化子系统在某时刻的状态快照（偏好数/策略版本/技能草稿/轨迹数） |
| `AgentRunner` | interface | `harness.go` | 统一执行接口：`RunTask(ctx, TaskCase) (*EvalResult, error)` |
| `DryRunner` | struct | `dry_runner.go` | 无 LLM 的合成结果 runner，CI 默认使用 |
| `CognitiveAgentRunner` | struct | `cognitive_runner.go` | 驱动真实 CognitiveAgent 的 runner |
| `EvalChannel` | struct | `eval_channel.go` | 无头 channel，自动审批所有工具调用，采集输出 |
| `EvalHook` | struct | `eval_hook.go` | `evolution.Hook` 实现，per-session 缓冲进化事件 |
| `Dimension` | type | `dimension.go` | 评测维度枚举（planning/tool_use/memory 等） |
| `VerifyMethod` | type | `dimension.go` | 验证方式（none/reference/rubric/judge_only） |
| `Reference` | struct | `harness.go` | 规则验证配置（必包含词、文件检查、退出码） |
| `Rubric` | struct | `harness.go` | LLM Judge 评判标准（多维度加权评分） |
| `ComparisonReport` | struct | `compare.go` | 两次 suite 运行的差异报告 |
| `WeaknessReport` | struct | `diagnosis.go` | 弱点分析报告，包含雷达图和建议 |
| `LongitudinalReport` | struct | `harness.go` | 跨轮次纵向学习曲线报告 |

---

## 3. 完整评测流水线

### 3.1 阶段一：任务加载

```
命名套件名     ──→  fixtures.go / fixtures_*.go  ──→  []TaskCase
外部 .yaml    ──→  taskset.go:LoadTaskSetYAML    ──→  []TaskCase
外部 .json    ──→  taskset.go:LoadTaskSetJSON    ──→  []TaskCase
```

内置套件通过 `AllSuites()` 注册，key 为套件名（`builtin`、`evolution`、`full`、`self_learning` 等）。外部文件自动按扩展名选择解析器。

### 3.2 阶段二：任务执行

```
RunSuiteWithOptions(ctx, runner, tasks, opts)
    ├── CaptureSnapshot(runner)          // 记录进化前快照 EvoBefore
    ├── for each TaskCase:
    │   ├── SetupFunc / SetupWithRunner  // 注入内存 Fixture 等
    │   ├── runner.RunTask(ctx, task)    // 核心执行
    │   │   ├── EvalChannel.HandleMessage(goal)
    │   │   ├── CognitiveAgent 5 阶段循环
    │   │   ├── evo.WaitPending()        // 等待异步进化事件完成
    │   │   ├── ComputeReward()          // 计算 RL 奖励值
    │   │   └── cogCollector.RecordAssertionRate()
    │   ├── VerifyReference(result)      // 规则验证（可选）
    │   ├── LLMJudge.Score(result)       // LLM 评分（可选）
    │   ├── ComputeFinalScore()          // 综合最终分
    │   ├── FailureClassifier.Classify() // 失败原因分类（可选）
    │   └── CleanupFunc / CleanupWithRunner
    ├── CaptureSnapshot(runner)          // 记录进化后快照 EvoAfter
    └── CaptureCogHealth(runner)         // 认知健康指标
```

### 3.3 阶段三：结果汇总与输出

```
SuiteResult
    ├── Summary()             // 生成聚合统计（成功率/平均分/维度分布等）
    ├── SaveJSON(path)        // 持久化为 JSON，自动创建父目录
    ├── Compare(before, after) ──→ ComparisonReport ──→ FormatMarkdown()
    └── (可选) WeaknessReport ──→ radar HTML
```

---

## 4. 模块地图

```
internal/eval/
├── 核心框架
│   ├── harness.go              ← TaskCase / EvalResult / SuiteResult / RunSuite*
│   ├── taskset.go              ← YAML/JSON 任务套件文件加载
│   ├── compare.go              ← 两次运行 diff，ComparisonReport
│   └── dimension.go            ← Dimension 枚举 / VerifyMethod / AggregateDimensions
│
├── 执行引擎
│   ├── dry_runner.go           ← 无 LLM 合成结果，CI 快速验证
│   ├── cognitive_runner.go     ← 真实 CognitiveAgent 驱动，进化/认知指标采集
│   ├── eval_channel.go         ← 无头 Channel，自动审批工具，采集输出流
│   └── eval_hook.go            ← evolution.Hook 实现，per-session 进化事件捕获
│
├── 评分与验证
│   ├── verifier.go             ← VerifyReference：规则检查（含/不含词/文件/退出码）
│   └── judge.go                ← LLMJudge：Rubric 多维度 LLM 打分
│
├── 诊断与适应
│   ├── classifier.go           ← FailureClassifier：失败原因分类（8 类）
│   ├── diagnosis.go            ← Diagnose：套件诊断 + WeaknessReport + 雷达图
│   └── adaptive.go             ← RunAdaptiveLoop：多轮自适应补强
│
├── 纵向分析
│   ├── longitudinal_runner.go  ← RunLongitudinal：多轮运行 + 进化洞见触发
│   └── learning_metrics.go     ← 学习曲线 / 策略收敛 / 复合分 / HTML 可视化
│
├── 标准基准
│   ├── benchmark.go            ← BenchmarkAdapter 接口 + AllBenchmarkAdapters
│   ├── bench_swe.go            ← SWE-bench 适配器
│   ├── bench_humaneval.go      ← HumanEval 适配器
│   └── bench_gaia.go           ← GAIA 适配器
│
└── 任务 Fixture 库
    ├── fixtures.go             ← BuiltinSuite / EvolutionSuite / AllSuites()
    ├── fixtures_planning.go    ← 规划维度任务
    ├── fixtures_tool.go        ← 工具调用维度任务
    ├── fixtures_memory.go      ← 记忆维度任务
    ├── fixtures_knowledge.go   ← 知识库维度任务
    ├── fixtures_conv.go        ← 对话维度任务
    ├── fixtures_error.go       ← 错误处理维度任务
    ├── fixtures_team.go        ← 团队协作维度任务
    ├── fixtures_evolution.go   ← 进化维度任务
    ├── fixtures_preference.go  ← 偏好学习维度任务
    └── fixtures_self_learning.go ← 自学习维度任务
```

---

## 5. 两种运行模式

### 5.1 Dry 模式（默认，CI 使用）

- 无需 LLM API Key，毫秒级完成
- `DryRunner` 生成合成的 `EvalResult`（success=true，固定 duration）
- 用途：CI 回归检测框架合法性 / PR 合并门控 / 快速冒烟

```bash
ironclaw eval run --suite builtin -o eval_output/ci_results.json
```

### 5.2 Live 模式（真实评测）

- 需要 LLM API Key（Claude 或 OpenAI compatible）
- `CognitiveAgentRunner` 驱动完整 5 阶段认知循环
- `initEvalGateway` 强制以下隔离设置：
  - `agent.mode = cognitive`
  - `evolution.enabled = true`
  - 临时内存目录（避免污染生产数据）
  - `SkipPersistedFeatureState = true`
  - Dashboard 关闭
- 用途：能力基线测量 / 进化前后对比 / 弱点发现

```bash
ironclaw eval run --suite builtin --live -c configs/ironclaw.yaml \
  --judge -o eval_output/live_results.json
```

---

## 6. 进化系统与评测的双向耦合

评测系统与进化系统是**双向绑定**关系：

```
评测系统 ──触发──→ 进化子系统
    ← 被度量 ←── 进化子系统
```

**评测驱动进化**：
- `CognitiveAgentRunner.RunTask` 中，每次任务执行都触发 `EvalHook` 上的进化事件
- `RunLongitudinal` 的 `--force-insights` 选项在轮次间调用 `evolution.RunInsightsCycle`，将轨迹数据转化为策略改进

**评测度量进化**：
- `EvolutionSnapshot` 在 suite 执行前后分别采集，量化进化状态变化
- `EvoSnapshotDiff` 计算 4 个 delta 字段，在 `ComparisonReport` 中展示进化增益

详见 [EVOLUTION_INTEGRATION.md](./EVOLUTION_INTEGRATION.md)。

---

## 7. 评测维度分类体系

系统定义了 **11 个评测维度**，对应 `Dimension` 枚举：

| 维度 | 标识 | 典型任务特征 |
|------|------|------------|
| 规划 | `planning` | 多步骤任务分解、目标追踪 |
| 工具调用 | `tool_use` | 工具选择准确性、参数正确率 |
| 记忆 | `memory` | 跨会话事实检索、记忆注入 |
| 知识 | `knowledge` | 知识库检索、实体关系查询 |
| 对话 | `conversation` | 多轮上下文理解、意图识别 |
| 错误处理 | `error_handling` | 异常恢复、重试策略 |
| 团队协作 | `team` | 子 Agent 调度、任务委派 |
| 进化 | `evolution` | 偏好学习速度、策略自适应 |
| 偏好学习 | `preference` | 用户偏好识别与应用 |
| 自学习 | `self_learning` | 技能迁移、知识泛化 |
| 综合 | `general` | 跨维度综合能力 |

`AggregateDimensions(results)` 按维度分组计算成功率和平均分，用于雷达图渲染。

---

## 8. 数据流与持久化

### 8.1 输入文件

```
eval/example_tasks.yaml          ← 示例任务套件（YAML 格式）
configs/ironclaw.example.yaml    ← 评测 Gateway 配置模板
```

### 8.2 输出文件

```
eval_output/
├── ci_results.json              ← CI dry run 结果
├── baseline.json                ← 基线快照（用于回归检测）
├── live_results.json            ← Live 模式运行结果
├── longitudinal/
│   ├── iteration_*.json         ← 每轮结果
│   └── report.json              ← 纵向报告
├── learning_curve.html          ← 学习曲线可视化
├── radar.html                   ← 维度雷达图
└── weakness_report.md           ← 弱点分析文本报告
```

### 8.3 SuiteResult JSON 结构概览

```json
{
  "run_id": "uuid",
  "started_at": "2026-04-22T10:00:00Z",
  "finished_at": "2026-04-22T10:05:00Z",
  "results": [/* []EvalResult */],
  "evo_before": {/* EvolutionSnapshot */},
  "evo_after":  {/* EvolutionSnapshot */},
  "cog_health": {/* cogmetrics.HealthReport */},
  "feature_state": {/* map[string]bool */}
}
```

---

## 9. 配置参数速查

### `eval run` 关键标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `--suite` | `builtin` | 套件名或文件路径 |
| `--live` | `false` | 启用真实 LLM（否则 dry run） |
| `--judge` | `false` | 启用 LLM Judge 评分 |
| `-o` / `--output` | — | JSON 输出路径 |
| `--run-id` | 自动生成 | 运行 ID（用于追踪） |
| `-c` / `--config` | — | 配置文件路径（live 模式必需） |

### `eval longitudinal` 关键标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `-n` / `--iterations` | `5` | 迭代轮次数 |
| `--with-workload` | `false` | 轮次间注入工作负载任务 |
| `--force-insights` | `true` | 轮次间强制触发进化洞见 |

### `eval compare` 关键标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `--before` | — | 基线 JSON 路径 |
| `--after` | — | 对比 JSON 路径 |
| `--fail-on-regression` | `false` | 出现回归时以 exit 1 退出（CI 用） |
| `--json` | `false` | 输出 JSON 格式（否则 Markdown） |

---

## 10. 子模块文档索引

| 文档 | 内容 |
|------|------|
| [HARNESS.md](./HARNESS.md) | 核心框架：TaskCase、EvalResult、SuiteResult、RunSuite 详解 |
| [RUNNERS.md](./RUNNERS.md) | 执行引擎：DryRunner、CognitiveAgentRunner、EvalChannel、EvalHook |
| [SCORING.md](./SCORING.md) | 评分流水线：Verifier、LLMJudge、Dimension、FinalScore |
| [DIAGNOSIS.md](./DIAGNOSIS.md) | 诊断与自适应：Classifier、WeaknessReport、AdaptiveLoop |
| [LONGITUDINAL.md](./LONGITUDINAL.md) | 纵向分析：多轮运行、学习曲线、可视化 |
| [EVOLUTION_INTEGRATION.md](./EVOLUTION_INTEGRATION.md) | 进化系统集成：EvalHook、EvolutionSnapshot、cogmetrics |
| [CLI_AND_CI.md](./CLI_AND_CI.md) | CLI 命令全览、Fixture 格式、CI/CD 工作流 |
