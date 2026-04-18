# 认知健康仪表盘

**日期**: 2026-04-18
**范围**: 新增 `internal/cogmetrics/` 包 + `ironclaw insights health` CLI 命令，暴露认知 Agent 的可观测指标

## 概述

此前 IronClaw 的自进化系统在后台运行，但没有任何方式观察其效果。用户无法回答"Agent 变好了吗？"这个基本问题。

本次改动新增认知健康指标收集器，它作为 `evolution.Hook` 接入事件流，实时累积断言通过率、重规划效率、工具可靠性等核心指标，并通过 CLI 和 JSON 两种方式暴露。

## 核心架构

### Collector（指标收集器）

`Collector` 实现 `evolution.Hook` 接口，监听三类事件：

```
┌──────────────────┐
│  evolution.Engine │
│                  │
│  OnReflection ───┼──► avgConfidence, complexitySuccess
│  OnEpisode ──────┼──► replanRate, replanEfficiency, totalEpisodes
│  OnToolExecuted ─┼──► toolReliability
│                  │
│  (外部调用)      │
│  RecordAssertion ┼──► assertionPassRate
│  SetStrategy ────┼──► strategyVersion
└──────────────────┘
```

所有指标使用 `RollingAvg`（滑动窗口平均值，默认窗口 100），确保指标反映近期趋势而非历史全量。

### RollingAvg（滑动窗口）

```go
type RollingAvg struct {
    values []float64
    pos    int
    count  int
    sum    float64
    cap    int
}
```

- 固定大小的环形缓冲区
- `Add(v)` 时间复杂度 O(1)
- `Avg()` 时间复杂度 O(1)
- 窗口满时自动淘汰最旧值

### HealthReport（健康快照）

`Snapshot()` 返回一个不可变的健康报告：

```go
type HealthReport struct {
    Timestamp        time.Time
    Uptime           time.Duration
    TotalEpisodes    int64
    TotalReflections int64
    StrategyVersion  int
    AssertionPassRate MetricValue
    ReplanRate        MetricValue
    ReplanEfficiency  ReplanEfficiency    // 对比：有重规划 vs 无重规划的成功率
    AvgConfidence     MetricValue
    ToolReliability   map[string]MetricValue
    ComplexitySuccess map[string]MetricValue
}
```

每个 `MetricValue` 包含 `Value`（当前均值）和 `Samples`（样本数），方便判断统计显著性。

## 指标清单

| 指标 | 来源事件 | 含义 | 健康方向 |
|------|---------|------|---------|
| 断言通过率 | `RecordAssertionRate` | OBSERVE 阶段断言的通过比例 | 高=好 |
| 重规划率 | `OnEpisodeComplete` | 需要重规划的 episode 比例 | 低=好 |
| 重规划后成功率 | `OnEpisodeComplete` | 有重规划的 episode 中成功的比例 | 高=好 |
| 无重规划成功率 | `OnEpisodeComplete` | 无重规划的 episode 中成功的比例 | 高=好 |
| 平均置信度 | `OnReflectionComplete` | REFLECT 阶段的平均 confidence | 稳定=好 |
| 工具可靠性 | `OnToolExecuted` | 每种工具的成功率 | 高=好 |
| 复杂度成功率 | `OnReflectionComplete` | 每个复杂度等级的成功率 | 高=好 |
| 策略版本 | `SetStrategyVersion` | StrategyOptimizer 的当前版本号 | 增长=活跃进化 |

### 重规划效率（核心差异化指标）

`ReplanEfficiency` 是最具洞察力的指标——它回答"重规划到底有没有用？"：

```
ReplanEfficiency:
  WithReplan:    { Value: 0.65, Samples: 20 }   ← 重规划后的成功率
  WithoutReplan: { Value: 0.85, Samples: 80 }   ← 首次就成功的比例
```

如果 `WithReplan` 远低于 `WithoutReplan`，说明重规划策略需要优化。

## 输出格式

### Markdown（CLI 默认）

```markdown
# Cognitive Health Report

**Uptime**: 2h30m | **Episodes**: 47 | **Reflections**: 52 | **Strategy v3**

## Core Metrics
| Metric | Value | Samples |
|--------|-------|---------|
| Assertion Pass Rate | 87.3% | 52 |
| Replan Rate | 23.4% | 47 |
| Avg Confidence | 0.742 | 52 |

## Replan Efficiency
| Condition | Success Rate | Samples |
|-----------|-------------|---------|
| With Replan | 63.6% | 11 |
| Without Replan | 86.1% | 36 |

## Tool Reliability
| Tool | Success Rate | Samples |
|------|-------------|---------|
| bash | 91.2% | 34 |
| file_write | 100.0% | 12 |
| http | 75.0% | 8 |
```

### JSON

`FormatJSON()` 输出完整的结构化 JSON，适合外部监控系统或仪表盘集成。

## CLI 命令

### `ironclaw insights health`

```bash
ironclaw insights health --days 7 [--json]
```

从磁盘上的轨迹 JSONL 文件重建健康指标（离线模式）。工作原理：

1. 读取 `~/.IronClaw/evolution/trajectories/` 下的轨迹文件
2. 将每条 `TrajectoryRecord` 回放为 `ReflectionEvent`、`EpisodeEvent`、`ToolExecEvent`
3. 通过 `Collector` 累积指标
4. 输出 `HealthReport`

这使得即使 IronClaw 不在运行中，也能分析历史数据。

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/cogmetrics/collector.go` | 新增 | Collector 实现 evolution.Hook，累积全部指标 |
| `internal/cogmetrics/rolling_avg.go` | 新增 | RollingAvg 滑动窗口数据结构 |
| `internal/cogmetrics/snapshot.go` | 新增 | HealthReport 快照和 MetricValue 类型 |
| `internal/cogmetrics/reporter.go` | 新增 | FormatMarkdown 和 FormatJSON 输出格式 |
| `internal/cogmetrics/collector_test.go` | 新增 | 9 个测试覆盖全部指标收集和输出路径 |
| `cmd/ironclaw/insights.go` | 修改 | 新增 `insights health` 子命令 + `buildHealthFromTrajectories` |

## 测试

9 个测试用例：

- `TestRollingAvg_Basic` — 基础均值计算
- `TestRollingAvg_WindowOverflow` — 窗口满后淘汰最旧值
- `TestRollingAvg_Empty` — 空窗口返回 0
- `TestCollector_OnReflectionComplete` — 置信度和复杂度成功率累积
- `TestCollector_OnEpisodeComplete` — 重规划率和效率分桶
- `TestCollector_OnToolExecuted` — 每工具可靠性追踪
- `TestCollector_RecordAssertionRate` — 断言通过率累积
- `TestCollector_FormatMarkdown` — Markdown 输出非空
- `TestCollector_FormatJSON` — JSON 输出可序列化
