# IronClaw 自进化机制深度解析

> 基于源码精确分析，版本日期：2026-04-14

---

## 目录

1. [总体架构](#1-总体架构)
2. [进化引擎（Evolution Engine）](#2-进化引擎evolution-engine)
3. [认知循环与自进化的衔接](#3-认知循环与自进化的衔接)
4. [三条反馈回路](#4-三条反馈回路)
   - [Loop 1 · 偏好学习器（PreferenceLearner）](#loop-1--偏好学习器preferencelearner)
   - [Loop 2 · 技能合成器（SkillSynthesizer）](#loop-2--技能合成器skillsynthesizer)
   - [Loop 3 · 策略优化器（StrategyOptimizer）](#loop-3--策略优化器strategyoptimizer)
5. [洞见生成器（InsightGenerator）](#5-洞见生成器insightgenerator)
6. [轨迹记录器（TrajectoryRecorder）](#6-轨迹记录器trajectoryrecorder)
7. [模型路由器（ModelRouter）](#7-模型路由器modelrouter)
8. [强化学习桥接层（RL Bridge）](#8-强化学习桥接层rl-bridge)
9. [完整数据流](#9-完整数据流)
10. [持久化结构](#10-持久化结构)
11. [配置参数速查](#11-配置参数速查)

---

## 1. 总体架构

IronClaw 的自进化系统以**事件驱动**为核心，围绕一个中央调度引擎将三条相互独立的反馈回路串联起来，共同作用于认知代理的下一次决策。

```
┌────────────────────────────────────────────────────────────┐
│                  CognitiveAgent（认知代理）                  │
│   PERCEIVE → PLAN → ACT → OBSERVE → REFLECT                │
│        ↑ 注入偏好 / 策略提示 / 模型路由                       │
└─────────────────────┬──────────────────────────────────────┘
                      │ 触发三类事件
                      ▼
┌────────────────────────────────────────────────────────────┐
│              Evolution Engine（进化引擎）                    │
│  DispatchReflection()  DispatchEpisode()  DispatchToolExec()│
│       ↓ 异步 goroutine（含 10s 超时保护 + panic 恢复）        │
└──────┬─────────────────────┬────────────────────┬──────────┘
       │                     │                    │
       ▼                     ▼                    ▼
┌─────────────┐   ┌─────────────────┐   ┌─────────────────────┐
│ Preference  │   │  Skill          │   │  Strategy           │
│ Learner     │   │  Synthesizer    │   │  Optimizer          │
│ (Loop 1)    │   │  (Loop 2)       │   │  (Loop 3)           │
└──────┬──────┘   └────────┬────────┘   └──────────┬──────────┘
       │                   │                        │
  preferences.yaml    SKILL_*.md 草稿          strategy.yaml
       │                                            ↑
       │                          ┌─────────────────┘
       │                          │  每 6h 触发
       │                 ┌────────────────────┐
       │                 │  Insight Generator  │
       │                 │  (读取过去 7 天轨迹) │
       │                 └─────────┬───────────┘
       │                           │
       │                  ┌────────────────────┐
       │                  │ Trajectory Recorder │
       │                  │  *.jsonl (按日轮转)  │
       │                  └────────────────────┘
       │
       └──────────────────────────────────────┐
                                              ↓
                              下一次会话的 Prompt 注入
```

**设计哲学**：所有进化行为均在**主请求路径之外**异步完成，对用户侧响应延迟的额外开销小于 5ms。

---

## 2. 进化引擎（Evolution Engine）

**源文件**：`internal/evolution/engine.go`

进化引擎是整个自进化系统的**事件总线**。

### 2.1 核心数据结构

```go
// 三类事件载体

// ReflectionEvent — REFLECT 阶段完成后触发
type ReflectionEvent struct {
    SessionID      string
    UserID         string
    Goal           string
    Complexity     string    // "simple" / "moderate" / "complex"
    Succeeded      bool
    Confidence     float64
    LessonsLearned []string
    ToolsUsed      []string
    ReplanCount    int
    UserFeedback   float64   // -1(差) ~ 0(未收集) ~ +1(好)
    FinalAnswer    string
    Timestamp      time.Time
}

// EpisodeEvent — RL 回合记录完成后触发
type EpisodeEvent struct {
    SessionID    string
    EpisodeID    string
    Goal         string
    Complexity   string
    Succeeded    bool
    TotalReward  float64
    ToolSequence []string  // 有序工具调用序列
    ReplanCount  int
    DurationMs   int64
    UserFeedback float64
    Timestamp    time.Time
}

// ToolExecEvent — 每次工具执行完成后触发
type ToolExecEvent struct {
    SessionID  string
    ToolName   string
    Succeeded  bool
    Denied     bool
    DurationMs int64
    Timestamp  time.Time
}
```

### 2.2 Hook 接口

```go
type Hook interface {
    Name() string
    OnReflectionComplete(ctx context.Context, event ReflectionEvent)
    OnEpisodeComplete(ctx context.Context, event EpisodeEvent)
    OnToolExecuted(ctx context.Context, event ToolExecEvent)
}
```

四个内置实现：`PreferenceLearner`、`SkillSynthesizer`、`StrategyOptimizer`、`TrajectoryRecorder`。

### 2.3 安全调度机制

```
DispatchXxx(event)
    └─ for each hook:
           go safeDispatch(hookName, fn)
                ├─ defer wg.Done()
                ├─ defer recover()    // panic 不会崩溃主进程
                ├─ ctx with timeout   // 默认 10s
                └─ fn(ctx)
```

### 2.4 洞见反馈循环（Insights Loop）

引擎启动后，若同时满足"配置了轨迹目录"和"注册了 StrategyOptimizer"两个条件，则额外启动一个后台 goroutine：

```
每隔 6 小时:
    1. ReadTrajectories(dir, now-7d, now)
    2. 若记录数 < 5 → 跳过（数据不足）
    3. GenerateInsights(records, "auto-7d")
    4. StrategyOptimizer.ApplyInsights(report)
```

---

## 3. 认知循环与自进化的衔接

**源文件**：`internal/agent/cognitive.go`，`internal/agent/rl_helpers.go`

`CognitiveAgent` 执行 **PERCEIVE → PLAN → ACT → OBSERVE → REFLECT** 五阶段循环。自进化系统在两个关键节点介入：

### 3.1 执行前：注入进化上下文

在 **PERCEIVE** 和 **PLAN** 阶段，认知代理调用进化引擎的两个组件，将历史学到的偏好和策略注入 Prompt：

```
PERCEIVE 阶段（perceive.go）:
    ModelRouter.SelectModel(complexity)
    → 根据任务复杂度动态选择 LLM 型号

PLAN 阶段（plan.go）:
    PreferenceLearner.BuildPromptSection()
    → 注入: "USER PREFERENCES (learned from past interactions): ..."

    StrategyOptimizer.BuildPromptSection()
    → 注入: "STRATEGY HINTS (from self-evolution): ..."
```

**注入示例**（实际 Prompt 片段）：
```
USER PREFERENCES (learned from past interactions):
- Preferred tools: bash_read, file_write, git_clone
- Handles well: moderate complexity, simple complexity
- This user prefers direct execution without replanning

STRATEGY HINTS (from self-evolution):
- Replan threshold: 0.27 (replan effective (75.0% vs no-replan 52.0%))
- Tool priority adjustments:
  - bash_read: 0.55 (preferred, tool highly successful (84.0%))
- Historical success rate: 78% (120 episodes)
```

### 3.2 执行后：触发进化事件

REFLECT 阶段完成后，认知代理向进化引擎派发事件：

```go
// 触发顺序（rl_helpers.go + cognitive.go）

// 1. 每次工具执行后（ACT 阶段）
evoEngine.DispatchToolExec(ToolExecEvent{...})

// 2. REFLECT 完成后
evoEngine.DispatchReflection(ReflectionEvent{
    Succeeded:    reflection.Succeeded,
    Confidence:   reflection.Confidence,
    ToolsUsed:    toolsUsedThisSession,
    ReplanCount:  replanCount,
    UserFeedback: feedbackScore,
    ...
})

// 3. RL 回合记录后
evoEngine.DispatchEpisode(EpisodeEvent{
    TotalReward:  episodeReward,
    ToolSequence: toolSequence,
    ...
})
```

---

## 4. 三条反馈回路

### Loop 1 · 偏好学习器（PreferenceLearner）

**源文件**：`internal/evolution/preference.go`  
**触发事件**：`OnReflectionComplete`（仅在 `Succeeded=true` 且 `Confidence >= MinConfidence` 时生效）

#### 4.1.1 信号提取规则

| 信号类别 | 提取条件 | 记录内容 |
|---------|---------|---------|
| `tool_preference` | 每个参与成功任务的工具 | 工具名 → `"preferred"` |
| `complexity_handling` | 成功任务的复杂度级别 | 复杂度 → `"handles_well"` |
| `replan_tendency` | ReplanCount == 0 | `"no_replans"` → `"preferred"` |
| `replan_tendency` | ReplanCount >= 2 | `"uses_replans"` → `"approved"` |
| `replan_tendency` | ReplanCount == 1 | **跳过**（语义模糊，不计入） |

#### 4.1.2 置信度模型

```
Confidence = min(1.0, Count × 0.2)

Count = 1  → Confidence = 0.20  (初次观测)
Count = 2  → Confidence = 0.40
Count = 3  → Confidence = 0.60
Count = 5  → Confidence = 1.00  (满置信度)
```

#### 4.1.3 容量保护与驱逐策略

- 存储上限：`MaxPreferences`（默认 100 条）
- 满容后驱逐：优先淘汰**置信度最低**的条目，同等置信度下淘汰**最久未见**的条目

#### 4.1.4 持久化

- 格式：YAML
- 路径：`~/.IronClaw/evolution/preferences.yaml`
- 跨会话加载时采用**高 Count 胜出**策略合并

---

### Loop 2 · 技能合成器（SkillSynthesizer）

**源文件**：`internal/evolution/synthesizer.go`，`internal/evolution/pattern.go`  
**触发事件**：`OnEpisodeComplete`

#### 4.2.1 模式提取算法

对每条 `ToolSequence` 提取所有长度为 **2~4** 的连续子序列，使用 **Welford 在线均值算法**（O(1) 空间）维护每个模式的 `AvgReward`：

```
序列 [A, B, C, D] 的提取结果：
  长度 2: [A,B], [B,C], [C,D]
  长度 3: [A,B,C], [B,C,D]
  长度 4: [A,B,C,D]

模式 Key（顺序无关）= sorted(tools).join("|")
  示例: "bash_read|file_write"

AvgReward 更新（Welford 公式）:
  avg' = avg + (TotalReward - avg) / Count
```

#### 4.2.2 技能生成触发条件

同时满足以下两个阈值才会生成草稿：

| 条件 | 默认阈值 | 说明 |
|------|---------|------|
| `Count >= PatternThreshold` | 3 次 | 该模式出现 3 次以上 |
| `AvgReward >= RewardThreshold` | 0.5 | 平均奖励 ≥ 0.5 |

- **去重**：`generated map[patternKey]bool` 确保同一模式只生成一次草稿

#### 4.2.3 生成的 SKILL.md 草稿格式

```markdown
---
name: auto_bash_read_file_write
description: Skill auto-generated from 5 occurrences of tool pattern [bash_read|file_write] with avg reward 0.782.
status: draft
auto_generated: true
---

# auto_bash_read_file_write

## Pattern Summary

This skill was automatically synthesized from observed tool-usage patterns.

- **Tools:** bash_read, file_write
- **Occurrences:** 5
- **Average Reward:** 0.782
- **First Seen:** 2026-04-01 09:12:34
- **Last Seen:** 2026-04-14 15:44:21

## Usage

Review and refine the tool sequence below before promoting to production.

```
bash_read -> file_write
```
```

草稿保存到 `~/.IronClaw/skills/drafts/SKILL_bash_read_file_write.md`，若 `AutoNotify=true` 则在下次会话启动时通知用户。

---

### Loop 3 · 策略优化器（StrategyOptimizer）

**源文件**：`internal/evolution/optimizer.go`  
**触发事件**：`OnEpisodeComplete`

#### 4.3.1 滚动窗口

维护一个最近 **100 个 Episode** 的 FIFO 队列，每当 `episodeCount % UpdateInterval == 0` 时触发一次优化（默认 UpdateInterval=10，即第 10、20、30… 个 episode 时触发）。

> **初始状态**：策略 `version` 初始值为 1。`BuildPromptSection()` 在 `version <= 1` 时返回空字符串，意味着在首次优化运行前，不会向 Prompt 注入任何策略提示——避免尚未积累足够数据时产生误导性建议。

#### 4.3.2 统计量计算

```
overallSuccessRate    = successCount / len(episodes)
replanEffectiveness   = withReplanSuccessCount / withReplanTotal
noReplanSuccessRate   = noReplanSuccessCount / noReplanTotal
toolSuccessRate[t]    = toolSuccess[t] / toolTotal[t]  (需 >= 3 次观测)
```

#### 4.3.3 参数调整规则

**Replan 阈值调整**（需 withReplan 和 noReplan 都有数据）：

| 条件 | 动作 | 计算公式 |
|------|------|---------|
| `replanEffectiveness > 0.70` | 降低阈值（触发更多 replan） | `value × (1 - 0.10)` |
| `replanEffectiveness < 0.30` | 提高阈值（减少无效 replan） | `value × (1 + 0.10)` |
| 其余 | 不变 | — |

- 参数硬边界：`[0.01, 0.99]`
- 默认初始值：`0.3`

**工具优先级调整**（每个工具需 >= 3 次观测）：

| 条件 | 动作 | 计算公式 |
|------|------|---------|
| `toolSuccessRate > 0.80` | 提升优先级 | `value × (1 + 0.10)` |
| `toolSuccessRate < 0.50` | 降低优先级 | `value × (1 - 0.10)` |
| 其余 | 记录初始值 | `0.5`（默认值） |

- 优先级边界：`[0.0, 1.0]`

#### 4.3.4 自动回滚机制

每次优化结束后，将当前成功率与上一轮快照对比：

```
if previousSuccessRate > 0:
    decline = previousSuccessRate - currentSuccessRate
    if decline > RevertThreshold (默认 0.15):
        回滚所有参数至 Previous 值
        打印警告日志
```

**每个参数都保存** `Previous` 字段，支持精确回滚：

```yaml
replan_threshold:
  value: 0.27
  previous: 0.30
  reason: "replan effective (75.0% vs no-replan 52.0%)"
```

#### 4.3.5 持久化

- 格式：YAML
- 路径：`~/.IronClaw/evolution/strategy.yaml`
- 每次优化后自动保存，`SaveState()` 时再次保存（用于优雅停机）

---

## 5. 洞见生成器（InsightGenerator）

**源文件**：`internal/evolution/insights.go`  
**触发方式**：由 Engine 内置的 `insightsLoop()` 每 **6 小时**自动调用

### 5.1 输入

过去 7 天的轨迹记录（`TrajectoryRecord` 列表）。数据量 < 5 条时跳过。

### 5.2 分析维度

| 维度 | 统计项 |
|------|-------|
| 全局指标 | SuccessRate、AvgDurationMs、AvgReplanCount、AvgUserFeedback |
| 工具效能（Top 10） | Uses、SuccessRate、AvgDurationMs |
| 复杂度分布 | Count、SuccessRate（按 simple/moderate/complex 分桶） |
| 失败模式 | 出现 ≥ 2 次的失败工具组合（tools 排序后拼接为 key） |

### 5.3 自动建议生成规则

```
SuccessRate < 50%  且 episodes >= 5    → "考虑审查常见失败模式"
ToolSuccessRate < 50%  且 uses >= 3    → "考虑审查该工具的使用方式"
AvgReplanCount > 1.5  且 episodes >= 5 → "计划可能过于激进或置信度阈值过低"
ComplexitySuccessRate < 30%  且 count >= 3 → "建议拆分为更简单的子任务"
AvgUserFeedback < -0.3  且 episodes >= 5  → "近期质量下降，审查会话记录"
```

### 5.4 洞见 → 策略的闭环

`InsightsReport` 生成后，立即传递给 `StrategyOptimizer.ApplyInsights()`：

```
ApplyInsights(report):
    1. 对 TopTools 中每个工具:
       - SuccessRate > 80%  → 提升优先级
       - SuccessRate < 50%  → 降低优先级
    2. 若 episodes >= 5:
       - AvgReplan > 1.5 且 SuccessRate < 50% → 提高 replan 阈值
       - AvgReplan < 0.5 且 SuccessRate > 80% → 小幅降低 replan 阈值（×0.5 倍调整量）
    3. 如有调整 → strategy.version++ → 保存到 YAML
```

---

## 6. 轨迹记录器（TrajectoryRecorder）

**源文件**：`internal/evolution/trajectory.go`  
**Hook 响应**：`OnToolExecuted`（缓冲）+ `OnEpisodeComplete`（刷盘）

### 6.1 写入流程

```
工具执行阶段:
    OnToolExecuted(event)
    → toolBuf[sessionID] = append(toolBuf[sessionID], ToolRecord{...})

Episode 完成时:
    OnEpisodeComplete(event)
    → tools = toolBuf[sessionID];  delete(toolBuf[sessionID])
    → 若 tools 为空，用 event.ToolSequence 兜底（全部标记 Succeeded=true）
    → 组装 TrajectoryRecord
    → append() → JSON 序列化 → 写入当天 JSONL 文件
```

### 6.2 文件轮转

- 命名规则：`~/.IronClaw/evolution/trajectories/2026-04-14.jsonl`
- 每日一个文件，**惰性创建**（首次写入时建目录和文件）
- 文件句柄当天复用，跨日自动关闭旧文件并打开新文件
- 并发写入通过 `sync.Mutex` 序列化

### 6.3 单条记录结构

```json
{
  "session_id": "sess_abc123",
  "goal": "将 README.md 翻译为中文",
  "complexity": "simple",
  "tools": [
    {"name": "bash_read", "succeeded": true, "duration_ms": 34},
    {"name": "file_write", "succeeded": true, "duration_ms": 12}
  ],
  "reflection": {
    "confidence": 0.82,
    "succeeded": true,
    "lessons": ["文件已存在时需要先备份"]
  },
  "user_feedback": 0.0,
  "replan_count": 0,
  "duration_ms": 4521,
  "timestamp": "2026-04-14T15:44:21Z"
}
```

> **实现细节**：`reflection.confidence` 字段在写入时被填充为 `EpisodeEvent.TotalReward`（而非 REFLECT 阶段的 `Confidence`），这是当前实现的特性。因此在通过 `ReadTrajectories` 读取后，该字段代表**回合奖励**，而非 LLM 的置信度评分。

> **实现细节**：`reflection.confidence` 字段在写入时被填充为 `EpisodeEvent.TotalReward`（而非 REFLECT 阶段的 `Confidence`），这是当前实现的特性。因此在通过 `ReadTrajectories` 读取后，该字段代表**回合奖励**，而非 LLM 的置信度评分。

---

## 7. 模型路由器（ModelRouter）

**源文件**：`internal/evolution/router.go`  
**调用点**：PERCEIVE 阶段（复杂度判定后）

### 7.1 路由逻辑

```
SelectModel(complexity) → RouteResult{Model, MaxTokens, Routed}

PERCEIVE 阶段评估出 complexity:
  "simple"   → cfg.Simple.Model   (e.g. claude-3-haiku)
  "moderate" → cfg.Moderate.Model (e.g. claude-3-sonnet)
  "complex"  → cfg.Complex.Model  (e.g. claude-3-opus)
  其他 / 未配置 → RouteResult{Routed: false}（使用默认模型）
```

### 7.2 效果追踪

每次路由结果都会被 `RecordOutcome(complexity, succeeded)` 记录，用于未来的可观测性分析（当前版本尚未接入自动调整，作为扩展点保留）。

---

## 8. 强化学习桥接层（RL Bridge）

**源文件**：`internal/evolution/rl_bridge.go`，`internal/rl/reward.go`

RL 系统与进化系统通过**桥接层解耦**，避免 `config → evolution → rl → config` 的循环依赖。

### 8.1 奖励计算（TrajectoryReward）

进化层自有的简化奖励函数：

```
reward = 0.0
if Succeeded:          reward += 0.50
if DurationMs < 60s:   reward += 0.20  (鼓励高效执行)
if ReplanCount == 0:   reward += 0.10  (鼓励一次成功)
reward += UserFeedback × 0.20          (用户反馈权重)
reward = clamp(reward, -1.0, 1.0)
```

### 8.2 RL 系统完整奖励（EpisodeReward）

`internal/rl/reward.go` 中的四维加权奖励：

| 维度 | 计算方式 |
|------|---------|
| `TaskSuccess` | 成功 +1.0，失败 -1.0 |
| `Efficiency` | `1 - DurationMs/MaxDurationMs - 0.2 × ReplanCount`，截断到 [-1, 1] |
| `Safety` | `1 - (deniedCount + failureCount) / totalActions` |
| `UserSatisfaction` | 直接使用 UserFeedback（-1 ~ +1） |

```
TotalReward = TaskSuccess × w1 + Efficiency × w2 + Safety × w3 + UserSatisfaction × w4
```

权重 `w1~w4` 来自 `config.RewardConfig`，支持用户自定义。

### 8.3 轨迹转 RL Experience

```go
// ConvertTrajectories(records) → []RLExperience
// 网关层再将 RLExperience 映射为 rl.Experience，喂入 PPO/DQN 训练器
```

特征向量中用 **one-hot 编码**表示复杂度，工具数量、Replan 数等都经过 `min(v/maxVal, 1.0)` 归一化。

---

## 9. 完整数据流

以下是一次完整用户交互从发起到自进化生效的全链路时序：

```
用户发送消息
     │
     ▼
┌──────────────────────────────────────────┐
│  Gateway 接收请求                         │
│  从 ~/.IronClaw/evolution/ 加载:           │
│    preferences.yaml → PreferenceLearner  │
│    strategy.yaml    → StrategyOptimizer  │
└──────────────┬───────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────┐
│  PERCEIVE 阶段                            │
│  1. 评估任务复杂度 (simple/moderate/complex)│
│  2. ModelRouter.SelectModel(complexity)  │
│     → 选择最优 LLM 型号                   │
│  3. 从记忆系统检索相关历史                  │
└──────────────┬───────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────┐
│  PLAN 阶段                                │
│  Prompt 中注入进化上下文:                   │
│  ┌─────────────────────────────────────┐ │
│  │ USER PREFERENCES:                   │ │
│  │   - Preferred tools: bash_read, ... │ │
│  │   - Handles well: moderate          │ │
│  │   - Prefers direct execution        │ │
│  │ STRATEGY HINTS:                     │ │
│  │   - Replan threshold: 0.27          │ │
│  │   - bash_read: preferred (0.55)     │ │
│  │   - Success rate: 78% (120 eps)     │ │
│  └─────────────────────────────────────┘ │
│  → LLM 生成任务计划                        │
└──────────────┬───────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────┐
│  ACT 阶段                                 │
│  逐一执行工具调用                           │
│  每次执行后 → DispatchToolExec()           │
│    ↓ 异步                                 │
│    TrajectoryRecorder.toolBuf[sid]++      │
└──────────────┬───────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────┐
│  OBSERVE 阶段                             │
│  收集工具输出，格式化为 observations         │
└──────────────┬───────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────┐
│  REFLECT 阶段                             │
│  LLM 评估结果 → Reflection{               │
│    Succeeded, Confidence, LessonsLearned │
│  }                                       │
│  → 写入记忆系统 (ADD/UPDATE/NOOP/DELETE)  │
│  → DispatchReflection(event)  ←── 异步   │
│  → DispatchEpisode(event)     ←── 异步   │
└──────────────┬───────────────────────────┘
               │
               │  ←── 上方触发以下所有异步流程
               ▼
╔══════════════════════════════════════════╗
║  并发执行（独立 goroutine）                ║
╠══════════════════════════════════════════╣
║                                          ║
║  PreferenceLearner.OnReflectionComplete  ║
║    if Succeeded && Confidence >= 0.3:    ║
║      提取 tool_preference 信号            ║
║      提取 complexity_handling 信号        ║
║      提取 replan_tendency 信号            ║
║      更新置信度 = min(1.0, Count×0.2)     ║
║      → 满容时驱逐最低置信度条目             ║
║                                          ║
║  SkillSynthesizer.OnEpisodeComplete      ║
║    PatternTracker.TrackEpisode(event)    ║
║      → 提取 2~4 长度子序列               ║
║      → Welford 均值更新 AvgReward        ║
║    GetCandidates(threshold=3, reward=0.5)║
║      → 满足条件的模式 → 生成 SKILL.md 草稿 ║
║                                          ║
║  StrategyOptimizer.OnEpisodeComplete     ║
║    → 加入滚动窗口（最多 100 条）            ║
║    → 每 10 个 episode 触发优化:           ║
║       computeStats()                     ║
║       adjustReplanThreshold()            ║
║       adjustToolPriorities()             ║
║       → 若成功率下降 > 15% → 回滚         ║
║       → 持久化 strategy.yaml             ║
║                                          ║
║  TrajectoryRecorder.OnEpisodeComplete    ║
║    → 合并 toolBuf[sessionID]             ║
║    → 序列化为 JSON                        ║
║    → 追加写入 YYYY-MM-DD.jsonl           ║
║                                          ║
╚══════════════════════════════════════════╝
               │
               │  ←── 每 6 小时独立触发
               ▼
╔══════════════════════════════════════════╗
║  Insights Feedback Loop（后台定时任务）    ║
║  ReadTrajectories(dir, now-7d, now)      ║
║  GenerateInsights(records, "auto-7d")    ║
║  → SuccessRate、TopTools、FailurePatterns ║
║  → 生成 Recommendations                  ║
║  StrategyOptimizer.ApplyInsights(report) ║
║  → 进一步微调 ReplanThreshold            ║
║  → 进一步微调 ToolPriorities             ║
╚══════════════════════════════════════════╝
               │
               ▼
        下一次请求时
   Prompt 注入更新后的偏好和策略
   → 代理行为持续优化 ✓
```

---

## 10. 持久化结构

```
~/.IronClaw/
├── evolution/
│   ├── preferences.yaml          # Loop 1 输出：用户偏好
│   ├── strategy.yaml             # Loop 3 输出：认知策略
│   └── trajectories/
│       ├── 2026-04-12.jsonl      # 轨迹记录（JSONL，每日一文件）
│       ├── 2026-04-13.jsonl
│       └── 2026-04-14.jsonl
└── skills/
    └── drafts/
        ├── SKILL_bash_read_file_write.md   # Loop 2 输出：技能草稿
        └── SKILL_bash_read_http_get.md
```

### preferences.yaml 示例

```yaml
- category: tool_preference
  key: bash_read
  value: preferred
  confidence: 1.0
  count: 8
  last_seen: "2026-04-14T15:44:21Z"
- category: complexity_handling
  key: moderate
  value: handles_well
  confidence: 0.8
  count: 4
  last_seen: "2026-04-14T15:44:21Z"
- category: replan_tendency
  key: no_replans
  value: preferred
  confidence: 0.6
  count: 3
  last_seen: "2026-04-13T10:20:00Z"
```

### strategy.yaml 示例

```yaml
version: 7
updated_at: "2026-04-14T12:00:00Z"
replan_threshold:
  value: 0.27
  previous: 0.30
  reason: "replan effective (75.0% vs no-replan 52.0%)"
tool_priorities:
  bash_read:
    value: 0.55
    previous: 0.50
    reason: "tool highly successful (84.0%)"
  http_get:
    value: 0.45
    previous: 0.50
    reason: "tool underperforming (43.0%)"
metrics:
  overall_success_rate: 0.78
  episodes_analyzed: 100
```

---

## 11. 配置参数速查

```yaml
# configs/ironclaw.yaml → agent.evolution

agent:
  evolution:
    enabled: true                 # 总开关
    hook_timeout: 10s             # 单个 Hook 最大执行时间
    preference_file: preferences.yaml  # 偏好持久化路径（相对 ~/.IronClaw/evolution/）

    preference:                   # Loop 1: 偏好学习
      enabled: true
      max_preferences: 100        # 最大存储条目数
      min_confidence: 0.3         # 最低置信度阈值（至少观测 2 次才计入）

    synthesizer:                  # Loop 2: 技能合成
      enabled: true
      pattern_threshold: 3        # 模式最少出现次数
      reward_threshold: 0.5       # 模式最低平均奖励
      drafts_dir: drafts          # 草稿目录（相对 ~/.IronClaw/skills/）
      auto_notify: true           # 下次启动时通知用户

    optimizer:                    # Loop 3: 策略优化
      enabled: true
      update_interval: 10         # 每 N 个 episode 触发一次优化
      max_adjustment_percent: 10  # 单次最大调整幅度 (%)
      revert_threshold: 0.15      # 成功率下降超过此值则回滚
      strategy_file: strategy.yaml

    model_routing:                # 模型路由（默认关闭，需显式启用并配置模型名称）
      enabled: false
      simple:
        model: claude-3-haiku-20240307  # 示例值，无内置默认
        max_tokens: 4096                # 0 = 使用 LLM 提供商默认值
      moderate:
        model: claude-3-sonnet-20240229
        max_tokens: 8192
      complex:
        model: claude-3-opus-20240229
        max_tokens: 16384
```

---

## 附：关键源文件索引

| 文件 | 职责 |
|------|------|
| `internal/evolution/engine.go` | 事件总线、异步调度、Insights 定时循环 |
| `internal/evolution/config.go` | 所有配置结构体及默认值 |
| `internal/evolution/preference.go` | Loop 1：偏好学习器 |
| `internal/evolution/pattern.go` | Loop 2：工具模式追踪（Welford 算法） |
| `internal/evolution/synthesizer.go` | Loop 2：技能草稿合成器 |
| `internal/evolution/optimizer.go` | Loop 3：策略优化器（含 ApplyInsights） |
| `internal/evolution/insights.go` | 洞见报告生成与推荐 |
| `internal/evolution/trajectory.go` | 轨迹记录与 JSONL 读写 |
| `internal/evolution/router.go` | 基于复杂度的模型路由 |
| `internal/evolution/rl_bridge.go` | 轨迹 → RL 经验转换 |
| `internal/agent/cognitive.go` | 认知代理主循环，集成进化引擎 |
| `internal/agent/reflect.go` | REFLECT 阶段，触发进化事件的源头 |
| `internal/rl/reward.go` | RL 四维奖励计算 |
