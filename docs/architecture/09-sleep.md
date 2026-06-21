# 09 · sleep — 睡眠整固

> 包路径 `internal/sleep` · 蓝图 §4.8 · **复利发生的地方**

## 职责

空闲时段的离线作业群。在没人等待时（timer 触发或 `/sleep` 按需）跑维护作业，保持世界模型连贯——重算 digest、折叠 journal、蒸馏技能、检测价值漂移。**蒸馏闭环是本模块的灵魂：情节（贵）→ 技能（便宜）→ 反射（免费）。** 每个转正技能把一类认知成本永久清零。

## 调度器

```go
// internal/sleep/sleep.go
type Job interface {
    Name() string
    Run(ctx context.Context) (string, error)  // 返回人读摘要；error 隔离不传播
}

type Summarizer interface {  // = memory.Completer 形状，gateway provider adapter 直接满足
    Complete(ctx, systemPrompt, userMessage string) (string, error)
}

type Runner struct { mu sync.Mutex; jobs []Job }
func (r *Runner) Run(ctx) Report  // 顺序跑、逐 job 错误/panic 隔离、TryLock 互斥
```

设计纪律：

- **顺序 + 逐 job 隔离**：一个 job 失败/panic 绝不中止 cycle，坏 job 不饿死其它（`runJob` 把 panic 转 error）。
- **cycle 互斥**：`TryLock` 门控——已有 cycle 在跑则立即返回 "skipped"，绝不重叠 clobber 彼此从陈旧快照的写入。
- **确定性（无内部时钟）**：jobs 不读内部时钟，`Summarizer` 与 `now func()` 在 gateway 边界注入，保证可测、可回放。

## 作业清单

gateway 在 `New()` 里按序注册（`gateway.go:145-180`，+ heart 启用时追加 synthesize）：

| 作业 | 做什么 | 依赖 |
|---|---|---|
| `DigestJob` | 重算代理自摘要（`fact.upsert`，稳定 id `fact_sleep_digest`，每 cycle 替换）→ identity digest | world + summarizer |
| `DriftJob` | 检测 active 价值被近期活动矛盾 → `MarkDrifting` 撤销自治（[07-values.md](07-values.md)）| values + world + summarizer |
| `RollupJob` | 折叠旧 journal（保留近期窗口 raw，折叠 ≥3 更老批次）→ 非破坏性 `rollup` 条目 | world + summarizer |
| `ReconcileJob` | 记忆和解：检测矛盾/重复 fact，supersede 陈旧者（保留规范事实，从检索移除 + correction 审计）| world + summarizer |
| `DistillJob` | 扫 journal 重复 ≥3 次且全 verified 的情节模式 → append-only `"distill candidate: "` decision（**仅检测，不生成/转正技能**）| world + summarizer |
| `PromoteJob` | 候选 → 生成惰性 `SKILL.md` 草稿入 `skills-staging`（窗口无关 SQL）| world + summarizer + fileDraftSink |
| `ProposalsJob` | 扫 72h 内 commitments → 生成预期提案队列（[10-proposals.md](10-proposals.md)）| commitmentSource + proposalsSink + summarizer + now |
| `DistillScreenJob` | 评审 staging 草稿质量/安全/结构 → 入 typed `promote_skill` 提案（cap 3/cycle，dismiss 14 天冷却按 slug）| stagedDraftSource + proposalsSink + summarizer + now |
| `SynthesizeRulesJob`（heart 启用时）| 从路由纠正合成 attention 规则（仅一致纠正、阈值 2、绝不重复已有规则）→ 写 `rules.yaml`（git commit）| feedbackCorrectionSource + rulesFileSink + canaryCorpus |
| `throttleEvalJob` | 刷新经济节流 gate（始终注册，enforce 关时 no-op）| gateway refresh 闭包 |

## 蒸馏闭环（本模块灵魂）

蒸馏分三段，**自治转正受金丝雀阻塞**（诚实墙）：

```
DistillJob          扫重复模式 → "distill candidate" 决策（仅检测）
    ↓
PromoteJob          候选 → SKILL.md 草稿 → skills-staging（不被加载、零执行风险）
    ↓
DistillScreenJob    LLM judge 草稿质量/安全/结构 → typed promote_skill 提案
    ↓
proposals coordinator  用户 accept → skill.PromoteDraft 确定性文件移动（staging→active）
```

**为什么不自动转正**：技能经 `read_skill` 工具懒加载、非 system-prompt 注入 → replay-Canary 无法行为级验证草稿（行为 canary 需多步轨迹执行 = §706 墙）。所以自动筛只能 judge 草稿质量 + 结构/安全校验，**最终转正是 operator 人签**（`daimon skill promote`）。这保留宪法第 4 条「不可逆永人签」——一个自动转正的技能会自治执行，是蓝图最高的"带病转正"风险。详见 [17-skills-workflow.md](17-skills-workflow.md)。

## 自治调度（§4.8）

sleep 原仅 `/sleep` 按需同步。自治调度（镜像 daily-brief/selfops timer）：

- config `agent.heart.sleep_interval_minutes`（0=off 默认 / 1440=daily）+ `sleep_idle_minutes`（idle 门）。
- `internal.sleep` timer 事件 → gateway `triggerAutonomousSleep`：先 idle 检查（`now - lastEventAt >= idle`）→ `atomic.CompareAndSwap` 守卫（in-flight 则 no-op，cycle 绝不重叠）→ `go func` 脱离短命 dispatch ctx（`context.WithoutCancel` + 有界 cycle timeout）→ `runAutonomousSleepCycle`（`sleep.Run` + proposal 投递）→ 立即返回，heart loop 永不阻塞。
- 手动 `/sleep` 与自治 cycle 共享同一守卫（不可重叠双写 world/proposals）。
- 默认 off（interval 0 → 无 timer 注册，逐字节同旧行为）。

## 数据

读写 world（journal/commitments/facts）；写 proposals（迁移 033）；写 `rules.yaml`（文件）；写 `skills-staging`（文件）。

## 跨包接缝

- **→ world**：所有 job 的事实/事项/摘要读写。
- **→ proposals**：ProposalsJob / DistillScreenJob 入队。
- **→ values**：DriftJob `MarkDrifting`。
- **→ attention**：SynthesizeRulesJob 写 `rules.yaml`（闭合 feedback 回流环）。
- **→ skill**：PromoteJob 写 staging 草稿。
- **← gateway**：Summarizer（provider adapter）、now、各 source/sink 在 gateway 边界注入。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 离线作业群 | 复利 | 让代理跑得越久越便宜，不阻塞事件处理 |
| 逐 job 隔离 + cycle 互斥 | 鲁棒性 | 坏 job 不饿死其它；不重叠 clobber |
| 蒸馏不自动转正 | 宪法 4 + §706 墙 | 自治执行技能需行为 canary，懒加载无法行为验证 → 人签 |
| 确定性（时钟外注入）| 可回放 | jobs 可测、可金丝雀回放 |

蓝图验收：和解后矛盾事实检索只返回新条目；连续 4 周蒸馏出 ≥1 转正技能且其后该模式零认知调用；sleep 作业全程不阻塞事件处理（独立 goroutine + 低优先级）。

下一篇：[10-proposals.md](10-proposals.md) — 预期引擎。
