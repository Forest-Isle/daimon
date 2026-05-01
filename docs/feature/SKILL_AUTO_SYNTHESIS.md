# 自驱技能合成闭环（Auto-triggered Skill Synthesis）

**日期**: 2026-05-01  
**范围**: `internal/evolution/optimizer.go` + `internal/evolution/brain.go` + `internal/evolution/synthesizer.go` + `internal/evolution/engine.go` + `internal/gateway/init_cognitive.go`

## 概述

IronClaw 已有完整的 evolution 引擎（`StrategyOptimizer`）和技能管理系统（`SkillSynthesizer`），但两者之间缺少自动触发的桥梁：当 agent 在某类任务上持续失败时，需要人工介入才能触发技能合成。

本次改动实现了 Hermes Agent 所称的"使用过程中自我改进"：当 evolution 引擎检测到持续失败模式时，自动触发 `SkillSynthesizer` 生成新技能草稿，无需人工介入。

## 架构设计

### 信号流

```
EpisodeEvent
  └── StrategyOptimizer.OnEpisodeComplete()
        └── optimizeLocked()
              └── maybeTriggerSynthesisLocked()  ← 新增
                    ├── 条件：success_rate < 0.7 且 episodes >= 10 且 冷却期已过
                    ├── 提取最近 5 条失败 episode 的 UserMessage/TaskType
                    └── synthesisRequests <- synthesisRequest{FailurePattern}

Brain.OnEpisodeComplete()
  ├── optimizer.OnEpisodeComplete()
  └── 消费 synthesisRequests（非阻塞 select）
        └── go synthesizer.SynthesizeFromFailures(ctx, failurePattern)  ← 异步
```

### 触发条件

```go
const (
    synthesisSuccessThreshold = 0.7   // 成功率低于此值触发
    synthesisMinEpisodes      = 10    // 至少分析 10 条 episode
    synthesisCooldown         = 24 * time.Hour  // 冷却期，防止频繁触发
)
```

三个条件同时满足才触发：
1. `OverallSuccessRate < 0.7`（滑动窗口内成功率低于 70%）
2. `EpisodesAnalyzed >= 10`（样本量足够）
3. 距上次合成超过 24 小时

### 失败模式提取

从最近 5 条失败 episode 中提取 `UserMessage`（前 200 字）和 `TaskType`，拼接成 `failurePattern` 字符串传给 synthesizer。这给 LLM 足够的上下文来理解失败的任务类型，同时避免传入过长的原始输出。

### Brain 协调层

`Brain` 是 optimizer 和 synthesizer 之间的���调者，实现 `evolution.Hook` 接口，在 `OnEpisodeComplete` 里串联两者：

```go
func (b *Brain) OnEpisodeComplete(ctx context.Context, event EpisodeEvent) {
    b.optimizer.OnEpisodeComplete(ctx, event)
    // 非阻塞消费合成信号
    select {
    case req := <-b.optimizer.SynthesisRequests():
        go func() {
            if err := b.synthesizer.SynthesizeFromFailures(ctx, req.FailurePattern); err != nil {
                slog.Warn("skill synthesis failed", "err", err)
            }
        }()
    default:
    }
}
```

合成在独立 goroutine 里执行，失败只打 warn 日志，不阻塞主流程，不返回 error。

### SynthesizeFromFailures

`SkillSynthesizer` 新增方法：

```go
func (s *SkillSynthesizer) SynthesizeFromFailures(ctx context.Context, failurePattern string) error
```

内部构造 `SkillProposeInput`，描述失败模式，调用现有 `SkillProposer` 生成技能草稿，写入 `~/.IronClaw/skills/<drafts_dir>/SKILL_<stem>.md`。

### Gateway 装配

`init_cognitive.go` 的 `registerEvolutionHooks()` 把 `Brain` 注册为 evolution engine 的 hook，替代之前 `Brain` 只构造但未接线的状态：

```go
engine.RegisterHook(brain)
```

## 与现有系统的关系

| 组件 | 角色 | 改动 |
|------|------|------|
| `StrategyOptimizer` | 检测失败模式，发出合成信号 | 新增 `synthesisRequests` channel、`lastSynthesisAt` 冷却字段、`maybeTriggerSynthesisLocked` |
| `Brain` | 协调 optimizer 和 synthesizer | 新增，实现 `evolution.Hook`，消费信号并异步触发合成 |
| `SkillSynthesizer` | 生成技能草稿 | 新增 `SynthesizeFromFailures` 方法 |
| `evolution.Engine` | 分发 episode 事件 | 注册 Brain 为 hook |
| `init_cognitive.go` | 装配 | 把 Brain 注册进 Engine |

## 效果

- **自驱**：agent 在某类任务上连续失败后，24h 内自动生成一个技能草稿，无需人工触发
- **非侵入**：合成异步执行，不影响主 agent 循环的延迟
- **可控**：24h 冷却期防止频繁触发；成功率阈值和最小样本量可通过配置调整
- **可审计**：生成的草稿写入文件系统，人工可审查后决定是否激活

## 文件清单

| 文件 | 改动 |
|------|------|
| `internal/evolution/optimizer.go` | 新增 `synthesisRequests` channel、`lastSynthesisAt`、`maybeTriggerSynthesisLocked`；`episodeRecord` 新增 `UserMessage`/`TaskType` 字段 |
| `internal/evolution/brain.go` | 新增 `Brain` 结构体，实现 `evolution.Hook`，协调 optimizer 信号和 synthesizer 调用 |
| `internal/evolution/brain_test.go` | Brain 协调逻辑的单元测试 |
| `internal/evolution/synthesizer.go` | 新增 `SynthesizeFromFailures` 方法 |
| `internal/evolution/engine.go` | 支持 Brain 作为 hook 注册 |
| `internal/evolution/optimizer_test.go` | 合成触发条件的单元测试 |
| `internal/gateway/init_cognitive.go` | 把 Brain 注册进 evolution engine |
