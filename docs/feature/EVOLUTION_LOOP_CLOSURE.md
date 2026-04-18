# 三回路自进化闭环修复

**日期**: 2026-04-19
**范围**: 修复三回路自进化（偏好学习 / 技能合成 / 策略优化）+ RL 桥接 + 轨迹记录 + 洞见生成 + Eval Harness 集成链路中的 10 个断裂点

## 概述

对自进化全链路进行审计后，发现 10 个问题导致闭环不完整。本次修复覆盖 6 个包、15 个文件，涉及 P0 级闭环断裂、P1 级数据质量/竞态问题、P2 级覆盖缺口。

修复后，从认知循环 → 事件分发 → 三个学习回路 → 回注 Agent 行为的完整链路全部可用。

## 修复前闭环状态

```
Loop 1 偏好学习:  ⚠ UserFeedback 被忽略；Simple 任务不触发
Loop 2 技能合成:  ❌ 草稿写入成功但永远不会被加载（P0 断裂）
Loop 3 策略优化:  ✅ 基本可用，洞见只反馈策略不反馈偏好
RL 桥接:         ⚠ 在线/离线 reward 公式不一致
轨迹记录:        ⚠ Confidence 字段存的是 Reward；时区过滤 Bug
Eval Harness:    ⚠ 异步读取竞态；无进化状态快照
```

## 修复后闭环状态

```
Loop 1 偏好学习:  ✅ 反思 + UserFeedback + 洞见 → 偏好更新 → {{PREFERENCES}}
Loop 2 技能合成:  ✅ Episode → 模式检测 → 草稿写入 → SkillManager 加载
Loop 3 策略优化:  ✅ Episode → 滚动优化 + 6h 洞见 → {{STRATEGY}} + HardControl
RL 桥接:         ✅ 在线/离线 reward 公式一致
轨迹记录:        ✅ Reward/Confidence 字段语义正确 + UTC 时区安全
Eval Harness:    ✅ 同步读取 + 进化状态快照 + Simple 任务覆盖
```

---

## Fix #1 (P0): 技能草稿加载闭环断裂

### 问题

`SkillSynthesizer` 将草稿写为 `~/.IronClaw/skills/drafts/SKILL_bash+file_write.md`（扁平 `.md` 文件），但 `SkillManager.LoadDir` 扫描 `~/.IronClaw/skills/` 时，对 `drafts/` 子目录只查找 `drafts/SKILL.md`。草稿文件名是 `SKILL_*.md` 不是 `SKILL.md`，所以永远找不到。

```
写入路径: ~/.IronClaw/skills/drafts/SKILL_bash+file_write.md  ← synthesizer 写入
扫描逻辑: ~/.IronClaw/skills/drafts/SKILL.md                  ← manager 只找这个
                                              ↑ 不匹配 → 闭环断裂
```

### 修复

`skill/manager.go` `LoadDir` 对子目录采用两级策略：

1. **有 `SKILL.md`** → 只加载这个文件（向后兼容，行为不变）
2. **无 `SKILL.md`** → 扫描该子目录下所有扁平 `*.md` 文件（一层深度，非递归）

```go
if e.IsDir() {
    candidate := filepath.Join(dir, e.Name(), "SKILL.md")
    if _, err := os.Stat(candidate); err == nil {
        paths = append(paths, candidate)
    } else {
        subDir := filepath.Join(dir, e.Name())
        subEntries, subErr := os.ReadDir(subDir)
        if subErr == nil {
            for _, se := range subEntries {
                if !se.IsDir() && strings.HasSuffix(se.Name(), ".md") {
                    paths = append(paths, filepath.Join(subDir, se.Name()))
                }
            }
        }
    }
}
```

**测试**: 6 个新增（`TestLoadDir_SubdirFlatMDFiles` 直接验证 bug 场景）

---

## Fix #2 (P1): Engine 异步 dispatch 竞态

### 问题

`Engine` 的三种 Dispatch 方法都用 goroutine 异步调用 hook。`cognitive.go` 的 dispatch 顺序是 `DispatchToolExec` × N → `DispatchEpisode`。由于 `DispatchToolExec` 只启动 goroutine 不等待，`TrajectoryRecorder.OnEpisodeComplete` 可能在 `OnToolExecuted` 之前执行，导致 tool buffer 为空、轨迹丢失 per-tool 精度。

### 修复

`DispatchToolExec` 改为**同步调用**（保留 timeout + panic recovery）：

```go
// 修复前（异步）:
go e.safeDispatch(hook.Name(), func(ctx context.Context) {
    hook.OnToolExecuted(ctx, event)
})

// 修复后（同步）:
e.safeDispatch(hook.Name(), func(ctx context.Context) {
    hook.OnToolExecuted(ctx, event)
})
```

`DispatchReflection` 和 `DispatchEpisode` 保持异步不变。

新增 `WaitPending()` 方法供 Eval Runner 使用：

```go
func (e *Engine) WaitPending() {
    e.wg.Wait()
}
```

---

## Fix #3 (P1): 在线 reward 公式不完整

### 问题

`computeSimpleEpisodeReward` 只传 `Succeeded` + `Progress` 给 `ComputeReward`，而离线路径传全部 5 个字段（含 `DurationMs`、`ReplanCount`、`UserFeedback`）。同一 Episode 在线/离线产生的 reward 不一致。

### 修复

扩展函数签名，传入完整 `RewardInput`：

```go
func computeSimpleEpisodeReward(
    reflection *Reflection, obs *ObservationResult,
    durationMs int64, replanCount int, userFeedback float64,
) float64 {
    return evolution.ComputeReward(evolution.RewardInput{
        Succeeded:    reflection.Succeeded,
        Progress:     progress,
        DurationMs:   durationMs,
        ReplanCount:  replanCount,
        UserFeedback: userFeedback,
    })
}
```

`cognitive.go` `dispatchEvolutionEvents` 中的调用点同步更新，传入实际的 `durationMs`、`replanCount`、`userFeedback`。

---

## Fix #4 (P1): Trajectory Confidence 字段语义错误

### 问题

`TrajectoryRecorder.OnEpisodeComplete` 将 `event.TotalReward` 存入 `ReflectionBrief.Confidence`。下游 `rl_bridge.go` 将 `Confidence` 映射为 RL state 的 `PlanConfidence` 和 `ReflectionConf` 特征——"置信度"实际上是 reward 的重复编码。

### 修复

`ReflectionBrief` 新增 `Reward` 字段：

```go
type ReflectionBrief struct {
    Confidence float64  `json:"confidence"`
    Reward     float64  `json:"reward"`      // ← 新增
    Succeeded  bool     `json:"succeeded"`
    Lessons    []string `json:"lessons,omitempty"`
}
```

`OnEpisodeComplete` 写 `Reward` 而非 `Confidence`：

```go
Reflection: ReflectionBrief{
    Reward:    event.TotalReward,   // 正确语义
    Succeeded: event.Succeeded,
    // Confidence 保持零值，直到 EpisodeEvent 携带真实反思置信度
},
```

`computeTrajectoryReward` 优先使用显式 `Reward` 字段，零值时回退到 `ComputeReward` 公式（向后兼容旧 JSONL）。

---

## Fix #5 (P1): Eval Runner 异步读取竞态

### 问题

`CognitiveAgentRunner.RunTask` 在 `HandleMessage` 返回后立即读取 `EvalHook` 数据，但 hook 的异步 goroutine 可能还没执行完。

### 修复

在 `HandleMessage` 和 `populateFromEvolution` 之间插入同步点：

```go
handleErr := r.agent.HandleMessage(ctx, r.channel, msg)

if evo := r.agent.EvolutionEngine(); evo != nil {
    evo.WaitPending()  // 等待所有 hook goroutine 完成
}

r.populateFromObservation(result)
r.populateFromEvolution(result, sess.ID)
```

---

## Fix #6 (P1): UserFeedback 未接入偏好学习

### 问题

`PreferenceLearner.OnReflectionComplete` 接收 `ReflectionEvent.UserFeedback` 但完全忽略。声称的 "用户反馈 → 偏好更新" 链路不存在。

### 修复

新增 `applyUserFeedback` 私有方法：

- **正反馈** (`feedback > 0`)：该轮使用的工具偏好 Confidence 提升最多 +0.15
- **负反馈** (`feedback < 0`)：偏好 Confidence 降低最多 -0.15
- 幅度与 `|feedback|` 成比例

在 `OnReflectionComplete` 末尾调用：

```go
if event.UserFeedback != 0 {
    p.applyUserFeedback(event.ToolsUsed, event.UserFeedback)
}
```

新增 `EntryCount() int` getter（供 Eval 快照使用）。

---

## Fix #7 (P2): Simple 任务跳过进化事件

### 问题

`ComplexitySimple` 的任务走 `runtime.HandleMessage` 快速路径，直接 return，不经过 `dispatchEvolutionEvents`。偏好/技能/策略永远看不到简单任务数据。

### 修复

Simple delegation 完成后补发轻量 `EpisodeEvent`：

```go
if state.Goal.Complexity == ComplexitySimple {
    simpleStart := time.Now()
    err := ca.runtime.HandleMessage(ctx, ch, msg)

    if ca.evoEngine != nil && ca.evoEngine.IsEnabled() {
        ca.evoEngine.DispatchEpisode(evolution.EpisodeEvent{
            SessionID:  sess.ID,
            Goal:       msg.Text,
            Complexity: string(ComplexitySimple),
            Succeeded:  err == nil,
            DurationMs: time.Since(simpleStart).Milliseconds(),
            Timestamp:  time.Now(),
        })
    }
    return err
}
```

---

## Fix #8 (P2): 洞见只反馈策略优化器

### 问题

`insightsLoop` 每 6 小时生成 `InsightsReport`，但只调用 `StrategyOptimizer.ApplyInsights`，不反馈 `PreferenceLearner`。

### 修复

`runInsightsCycle` 新增偏好反馈：

```go
if pl := e.PreferenceLearnerHook(); pl != nil {
    plApplied := pl.ApplyInsights(report)
    if plApplied > 0 {
        slog.Info("evolution: insights → preferences updated", "adjustments", plApplied)
    }
}
```

`PreferenceLearner.ApplyInsights` 根据 `InsightsReport.TopTools` 的 per-tool 成功率调整偏好：
- 成功率 > 50%：偏好 Confidence 提升
- 成功率 < 50%：偏好 Confidence 降低
- 中性点 50%：不变
- 使用次数 < 3 的工具跳过（数据不充分）

---

## Fix #9 (P2): Eval 无进化状态快照

### 问题

`EvalResult` / `ComparisonReport` 只有 task-level 指标，无法量化 "进化效果"（偏好数量、策略版本、草稿数量变化）。

### 修复

新增 `EvolutionSnapshot` 结构和 `SnapshotCaptor` 接口：

```go
type EvolutionSnapshot struct {
    PreferenceCount int `json:"preference_count"`
    StrategyVersion int `json:"strategy_version"`
    SkillDraftCount int `json:"skill_draft_count"`
    TrajectoryCount int `json:"trajectory_count"`
}

type SnapshotCaptor interface {
    CaptureSnapshot() *EvolutionSnapshot
}
```

`SuiteResult` 新增 `EvoBefore` / `EvoAfter` 字段。`RunSuite` 在任务执行前后自动捕获快照（当 runner 实现 `SnapshotCaptor` 时）。

`CognitiveAgentRunner.CaptureSnapshot` 从 `PreferenceLearner.EntryCount()` 和 `StrategyOptimizer.GetStrategy().Version` 读取实时数据。

---

## Fix #10 (P2): ReadTrajectories 时区过滤

### 问题

JSONL 文件名按本地日期命名（`2006-01-02.jsonl`），`time.Parse` 解析为 UTC midnight。在 UTC+8 等正偏移时区凌晨前后，`since`/`until`（local time）与文件日期（UTC）比较可能跳过当天文件。

### 修复

`ReadTrajectories` 入口处将 `since`/`until` 归一化为 UTC，日期级预过滤增加 ±1 天缓冲：

```go
since = since.UTC()
until = until.UTC()

dayLo := since.Truncate(24 * time.Hour).Add(-24 * time.Hour)
dayHi := until.Truncate(24 * time.Hour).Add(24 * time.Hour)
```

逐条 record 的 Timestamp 精确校验确保最终正确性。

---

## 完整闭环数据流图

```
User Input ──► PERCEIVE ──► PLAN ──► ACT ──► OBSERVE ──► REFLECT
                 │            ▲                              │
                 │  {{PREFERENCES}}                          │
                 │  {{STRATEGY}}                             │
                 │  {{SKILLS}}                               │
                 │            │                              ▼
                 │            │                      dispatchEvolutionEvents
                 │            │                       │       │        │
                 │            │               Reflection  Episode  ToolExec(sync)
                 │            │                  │         │           │
                 │            │           ┌─ Loop 1 ──┐  ┌── Loop 2 ──┐  ┌── Loop 3 ──┐
                 │            │           │ Preference │  │ Skill      │  │ Strategy   │
                 │            │           │ + Feedback │  │ Synthesizer│  │ Optimizer  │
                 │            │           └─────┬──────┘  └─────┬──────┘  └──────┬─────┘
                 │            │                 │               │                │
                 │    {{PREFERENCES}} ◄─────────┘  LoadDir(drafts) │      {{STRATEGY}}
                 │    {{STRATEGY}} ◄──────────────── scans *.md ──┘      HardControl
                 │    {{SKILLS}} ◄─────────────────────┘                     │
                 │                                                           │
                 │     ┌─── Trajectory ──┐    ┌── insightsLoop ──┐           │
                 │     │ Reward (correct) │───►│ 6h cycle         │───► ApplyInsights
                 │     │ UTC-safe filter  │    │ → Strategy       │    (strategy+preference)
                 │     └──────────┬──────┘    └──────────────────┘
                 │                │
                 │     ┌── RL Bridge ──┐
                 │     │ Consistent    │──► RL Trainer → DQN replan
                 │     │ reward formula│
                 │     └──────────────┘

Simple Tasks ──► runtime.HandleMessage ──► lightweight DispatchEpisode ──► hooks
```

## 涉及文件

| 文件 | 变更类型 | 修复项 |
|------|---------|--------|
| `internal/skill/manager.go` | 修改 | #1: LoadDir 子目录扁平 md 扫描 |
| `internal/skill/manager_test.go` | 新增测试 | #1: 6 个新测试 |
| `internal/evolution/engine.go` | 修改 | #2: DispatchToolExec 同步化 + WaitPending；#8: insightsLoop 反馈偏好 |
| `internal/evolution/preference.go` | 修改 | #6: applyUserFeedback + ApplyInsights + EntryCount |
| `internal/evolution/preference_test.go` | 修改 | #6: UserFeedback 测试 |
| `internal/evolution/trajectory.go` | 修改 | #4: ReflectionBrief.Reward 字段；#10: UTC 归一化 + 日期缓冲 |
| `internal/evolution/trajectory_test.go` | 修改 | #4: Reward 断言；#10: UTCNormalization 测试 |
| `internal/evolution/rl_bridge.go` | 修改 | #4: computeTrajectoryReward 优先使用 Reward 字段 |
| `internal/evolution/rl_bridge_test.go` | 修改 | #4: 测试数据更新 |
| `internal/agent/cognitive.go` | 修改 | #3: 完整 RewardInput；#7: Simple 任务补发 EpisodeEvent |
| `internal/agent/rl_helpers.go` | 修改 | #3: computeSimpleEpisodeReward 扩展签名 |
| `internal/agent/rl_helpers_test.go` | 修改 | #3: 测试参数更新 |
| `internal/eval/harness.go` | 修改 | #9: EvolutionSnapshot + SnapshotCaptor + RunSuite 快照 |
| `internal/eval/cognitive_runner.go` | 修改 | #5: WaitPending 同步；#9: CaptureSnapshot |

## 测试

全部通过（`go build` + `go test` + `go vet`）：

| 包 | 测试数 | 结果 |
|-----|--------|------|
| `internal/evolution/` | 全量 | PASS (2.7s) |
| `internal/eval/` | 12 | PASS (2.2s) |
| `internal/skill/` | 6 新 + 已有 | PASS (1.3s) |
| `internal/agent/` | 全量 (short) | PASS (3.1s) |
