# 03 · attention — 注意力路由

> 包路径 `internal/attention` · 蓝图 §4.2

## 职责

判断每个事件**值不值得唤醒昂贵的认知路径**。常驻代理成本结构的根基（宪法第 5 条「认知是贵的」）。

设计偏置（源码包注释）：**宁可误醒（over-wake）**——漏掉一个重要事件的代价远大于多花一次认知，所以未决事件默认 `Cognize`。

## 核心类型

```go
// internal/attention/attention.go
type Action int
const (
    Ignore   Action = iota // 丢弃事件
    Reflex                  // 跑预编译确定性处理器（skill/workflow）
    Cognize                 // 花一整段认知情节
    WakeUser                // 直接打断用户
)

type Verdict struct {
    Action   Action
    ReflexID string // Action==Reflex 时：技能/workflow id
    Priority int    // 0 紧急 … 3 闲时批处理
    Reason   string // 审计用
}

type Router interface {
    Route(ctx context.Context, ev heart.Event) (Verdict, error)
}
```

## 三级责任链

`Chain`（`attention.go:131`）按**最便宜优先**组合三级，前级命中即返回：

```
Chain.Route(ev):
  1. 硬白名单    isHighRisk(ev.Kind)?  → WakeUser（永不下放）
  2. rules       rules.Route(ev)        → 命中即返回（确定性，用户可编辑）
  3. 小模型      model.Route(ev)        → decided 即返回（可选中间层）
  4. 兜底        → Cognize（"宁可醒"）
```

### 1) 硬白名单（不可覆盖）

```go
func DefaultHighRiskKinds() []string {
    return []string{"payment.", "security.", "legal.", "account.delete"}
}
```

高风险 kind（按精确值或前缀匹配，`payment.charge`/`payment.refund` 都命中）**永远 WakeUser**，先于任何规则或模型决策。这是宪法第 4 条「不可逆高风险永远人签」的结构保证——即便有 ignore 规则（甚至 sleep 合成的规则）也无法把高风险事件下路由到 Ignore。

`SetHighRiskKinds` 只在启动时配置一次、重启时重新应用，不为并发修改加保护（配置一次、读多写零）。

### 2) rules（确定性，用户可编辑）

`RulesRouter` 是最便宜的层：按 `source`/`kind`/`payload` 子串匹配，首条命中即返回。规则存 `~/.daimon/attention/rules.yaml`，用户可手编辑；**sleep 的 synthesize-rules 作业也会合成新规则写入此文件**（带 git commit，可一键 `daimon attention revert`）。

```go
type Rule struct {
    Source, Kind, Contains string // 空 = 通配
    Action, ReflexID       string
    Priority               int
}
```

格式错误的规则被跳过（`ParseAction` 失败 `continue`），不会静默吞掉事件。
当 `Action == Reflex` 时，`ReflexID` 必须能在 `agent.heart.reflexes` 中找到显式 workflow 配置；否则 dispatcher fail-closed 记录错误，不会改走 Cognize。

### 3) 小模型（可选中间层）

`ModelRouter` 接口（`LLMModelRouter` 实现）用 haiku 档小模型对规则未覆盖的事件分诊。输入 = 事件摘要 + 当前事项 digest（≤1k tokens），输出 = Verdict JSON。**model 抽签或出错时 `decided=false`**，责任链落到 Cognize 兜底——不让一次路由调用失败导致事件被错误 Ignore。其 token 消耗经 `RouteCostSink` 记入经济台账（activity class = `"routing"`）。

### 4) 兜底：Cognize

默认刻意是 Cognize 而非 Ignore：未分类事件值得一次思考，而非被静默丢弃。

## 误判回流

用户纠正（"这个不用管" / "怎么没告诉我"）写入 `attention_feedback` 表（迁移 030），sleep 的 `SynthesizeRulesJob` 据此合成/调整规则，闭合学习环。

```go
type FeedbackStore struct { db *sql.DB }
func (s *FeedbackStore) Record(ctx, fb Feedback) error  // {EventID, ExpectedAction, GivenAction, Note}
```

回流闭环：`attention_feedback` → sleep `SynthesizeRulesJob`（仅一致纠正、阈值门控、绝不重复已有规则）→ 写 `rules.yaml`（git commit）→ 下次同类事件被规则层廉价处理。详见 [09-sleep.md](09-sleep.md)。

## 数据

- `attention_feedback`（迁移 030）：`(event_id, expected_action, given_action, note, created_at)`。
- `rules.yaml`（文件，git 同仓）：路由规则，含 synthesized 标记。

## 跨包接缝

- **← heart**：`Chain` 是 heart 的 `Handler`（经 gateway `eventDispatcher.handle` 适配）。Route 返回的 `Verdict.Action.String()` 即 heart 持久化的 verdict 字符串。
- **→ episode**：`Cognize` 分支在 `heart_dispatch` 里调 `agent.RunInternalEpisode`。
- **→ channel**：`WakeUser` 分支推 primary channel 通知。
- **← mind**：`LLMModelRouter` 持 `mind.Provider` 做分诊调用。
- **→ economy**：`RouteCostSink` 记路由成本。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 三级最便宜优先 | 宪法 5「认知是贵的」 | 绝大多数事件在规则层 0 成本处理，路由总成本占比 <5% |
| 硬白名单先于一切 | 宪法 4「可逆优先」 | 高风险永远人签，模型/规则都无权下放（含 sleep 合成规则） |
| 兜底 Cognize 而非 Ignore | over-wake 偏置 | WakeUser 漏报零容忍（北极星指标 7） |
| 小模型 abstain 落兜底 | 安全 | 路由调用失败不致事件被错误丢弃 |

蓝图验收：路由成本 <5%；注入 100 个标注事件，WakeUser 召回率 100%（漏报零容忍），Ignore 准确率 >80%。其中"WakeUser 硬不变式"由白名单结构 + `TestHardWhitelistOverridesRules` 测试保证。

下一篇：[04-episode.md](04-episode.md) — 情节内核。
