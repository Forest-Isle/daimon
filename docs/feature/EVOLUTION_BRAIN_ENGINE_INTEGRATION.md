# Evolution Brain → Engine Integration

**日期**: 2026-05-15
**范围**: 将已构建但未使用的 Brain 协调器接入 Engine 的派发和 insights 循环，实现真正的跨回路反馈。

## 概述

`EvolutionBrain` 在 `internal/evolution/brain.go` 中已完整实现——统一协调三个进化回路（Preference Learner、Strategy Optimizer、Skill Synthesizer），具备跨回路反馈管道和统一指标——但在 `init_cognitive.go` 中被静默丢弃：`_ = brain // Brain is available for future Engine integration`。本次改动将 Brain 作为一等公民接入 Engine，让每一次 episode 派发和 insights 循环都经由 Brain 协调。

## 问题

此前的 Engine 分发模式是扁平化的：

```
DispatchEpisode → Hook.OnEpisodeComplete (每个 hook 独立)
RunInsightsCycle → StrategyOptimizer.ApplyInsights
                  → PreferenceLearner.ApplyInsights (独立调用)
```

这意味着：
- **没有跨回路反馈**：Skill 激活从不通知 Preference；Strategy 变化从不通知 Skill
- **没有统一健康评分**：BrainMetrics（`HealthScore`、`TotalEpisodes` 等）被完全忽视
- **双轨制 insights 应用**：同一个 insights report 被手动拆分到两个 hook，各自独立应用

## 架构

### Engine 变更 (`engine.go`)

```
Engine
├── brain *Brain          ← 新增字段
├── SetBrain(*Brain)      ← 新增方法
├── Brain() *Brain        ← 新增访问器
│
├── DispatchEpisode(...)  ← 注入: brain.OnEpisodeComplete(event)
│   └── 各 hook.OnEpisodeComplete (保持向后兼容)
│
└── RunInsightsCycle()    ← 注入: brain.ApplyInsights(report)
    │                      ← 注入: brain.DrainFeedback()
    └── 无 Brain 时回退到旧的直接调用
```

**关键代码路径**：

```go
// DispatchEpisode 现在先路由到 Brain，再分发给各 hook
func (e *Engine) DispatchEpisode(event EpisodeEvent) {
    // 1. Brain 统一协调
    if e.brain != nil {
        e.brain.OnEpisodeComplete(e.ctx, event)
    }
    // 2. 独立 hook（向后兼容）
    for _, h := range e.hooks {
        go h.OnEpisodeComplete(ctx, event)
    }
}

// RunInsightsCycle 在 Brain 接入时走统一路径
func (e *Engine) RunInsightsCycle() {
    report := GenerateInsights(records, "auto-7d")
    if e.brain != nil {
        brain.ApplyInsights(report)  // 同时应用到 pref + strategy + skill
        brain.DrainFeedback()         // 处理积压的跨回路消息
        return
    }
    // 回退: 直接应用到各 hook
}
```

### Gateway 接线 (`init_cognitive.go`)

改动前：
```go
brain := evolution.NewBrain(prefLearner, stratOptimizer, skillSynth)
_ = brain // 死代码
```

改动后：
```go
brain := evolution.NewBrain(prefLearner, stratOptimizer, skillSynth)
gw.evoEngine.SetBrain(brain) // 接入 Engine
```

### 跨回路反馈流

```
 Episode 完成
     │
     ▼
 Brain.OnEpisodeComplete
     ├── PreferenceLearner.OnEpisodeComplete
     ├── StrategyOptimizer.OnEpisodeComplete
     ├── SkillSynthesizer.OnEpisodeComplete
     │
     └── Cross-feedback push:
         strategyToSkill ← {ToolPriorities, ReplanThreshold}

 Insights Cycle (每 6h)
     │
     ▼
 Brain.ApplyInsights(report)
     └── 统一应用到 preference + strategy + skill

 Brain.DrainFeedback()
     ├── skillToPreference → PreferenceLearner.BoostTool()
     └── strategyToSkill → SkillSynthesizer.SetToolPriorities()
```

## 文件

| 文件 | 改动 |
|------|------|
| `internal/evolution/engine.go` | +brain 字段，+SetBrain/Brain 方法，DispatchEpisode 注入 Brain，RunInsightsCycle 走 Brain 路径 |
| `internal/gateway/init_cognitive.go` | `_ = brain` → `gw.evoEngine.SetBrain(brain)` |

## 效果

- **统一学习**：三个进化回路现在通过 Brain 协调，不再各自为政
- **跨回路反馈激活**：Skill→Preference 和 Strategy→Skill 反馈管道开始实际运行
- **健康评分可见**：`BrainMetrics.HealthScore` 随 episode 积累实时更新
- **完整向后兼容**：Brain 为 nil 时，所有派发路径回退到原有行为
