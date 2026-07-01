# 22 · 术语表 + 北极星指标 + 不变量速查

> 收尾参考：跨模块术语、健康度量、设计不变量一页速查

## 术语表

| 术语 | 含义 |
|---|---|
| **Daimon** | 主权个人代理。从 IronClaw coding-agent 运行时重铸。"铁打的爪，流水的脑"——agent 不朽，模型可换 |
| **铁打的爪 / 流水的脑** | 不变的部分（身份/信任/技能/价值/世界模型）vs 可替换的认知引擎（LLM）|
| **agent（这里特指本体）** | 跨模型存活的身份+状态集合，非某次对话 |
| **episode（情节）** | 一次有界认知执行单元。裸 ReAct 环 + `episode_close` 退出契约。强制交账 |
| **Outcome（交账）** | 情节终态结构：status(done/blocked/handed_off) + summary(≤500) + mutations + value_created |
| **交账强制** | 每情节必产 Outcome，落 world journal（宪法第 3 条）|
| **salvaged（抢救交账）** | 模型未好好 `episode_close` 时框架从 transcript 兜底提取 Outcome，标 `salvaged=true`。北极星指标 6 盯此率（<2%）|
| **退出契约** | episode 必经 `episode_close` 工具交账才算完成。schema 强制（宪法 2 例外之一）|
| **CognitiveKernel** | agent↔episode 的接缝接口。agent 不知 episode，经此注入认知内核 |
| **heart（心脏）** | 事件源汇聚 + 先落库后路由 + 去重 + 崩溃恢复。统一事件流 |
| **attention（注意力）** | 事件路由：硬白名单→rules→model→Cognize 兜底。决定 Ignore/Reflex/Cognize/WakeUser |
| **Cognize** | 路由动作：起一个情节认知处理事件 |
| **WakeUser** | 路由动作：推用户。高风险 kind 硬白名单永不下放给模型路由 |
| **Reflex（反射）** | 路由动作：免 LLM 执行显式配置的预编 tool-workflow |
| **world（世界模型）** | 三层状态：identity（身份文件）+ commitments（承诺）+ journal（流水）。唯一真相 |
| **commitment（承诺）** | 项目/承诺/截止/关注/例程，state=active/waiting/done/dropped |
| **values（价值）** | 用户价值条目（confidence/provenance/state）。自主行动许可源之一，ask-once 门控 |
| **drift（漂移）** | 价值条目与近期行为不一致的检测信号 |
| **action（行动层）** | 工具副作用治理：values→trust→classify→hold→execute→undo→verify→audit |
| **可逆性分级** | Reversible(0)/Compensable(1)/Irreversible(2)。按可逆性分域治理，非危险度 |
| **trust（信任等级）** | AskEvery→AskFirst→HoldThenAuto→FullAuto。阈值 1/3/10 verified；Irreversible 封顶 HoldThenAuto |
| **hold（补偿性延迟）** | Compensable 行动延迟执行留 recall 窗口。状态机 pending→executing→executed/failed |
| **undo（撤销回执）** | Reversible 行动 stamp receipt，可 `daimon undo` 逆转 |
| **verify（客观判据）** | 有测试/编译/断言时框架直接跑写 Receipt.Verified（宪法 2 例外之二）|
| **receipt（回执）** | 行动的许可来源 stamp：value:id / trust:level / reversible / interactive |
| **sleep（睡眠整固）** | 离线维护：reconcile/rollup/digest/drift/synthesize/distill/promote/proposals。确定性无内部时钟 |
| **distill（蒸馏）** | 扫 journal 重复≥3 且全 verified 的模式 → 技能草稿。自治转正受 Canary 阻塞 |
| **proposals（提案）** | 代理主动建议队列。pending/accepted/dismissed/expired。typed accept（episode vs promote_skill）|
| **replay（回放）** | 录制 JSONL 离线评测：重打分/回归集/金丝雀。换脑回归门控 |
| **Canary（金丝雀）** | 候选配置对 must-pass 回归集（corrected∪salvaged）门控，fail 退出码非零 |
| **economy（经济）** | 成本台账 + ROI 报表（by-class $ + value_created）+ 节流 advisor/enforcement |
| **activity class** | 路由 kind 聚合，成本归因维度 |
| **selfops（自我运维）** | 健康看门狗：5 信号巡检，离认知路径，失败安全良性默认 |
| **mind（模型层）** | Provider 抽象：Claude/OpenAI、缓存协商、重试熔断、Shadow 影子脑 |
| **gateway（组合根）** | 唯一装配点：subsystem + init_*.go 显式接线，驱动生命周期 |
| **绞杀者（strangler）** | 重铸方法：新路径渐进取代旧 IronClaw，旧路径跑通才拆 |
| **诚实墙** | 受真实约束无法 in-process 实现的能力（§706 行为 canary/多步 replay/自动 reflex 转正）|
| **§706** | 自我修改安全边界：草稿绝不自动加载/执行，确定性文件移动绝不走 LLM 情节 |

## 北极星指标（蓝图 §8）

| # | 指标 | 含义 | 方向 |
|---|---|---|---|
| 1 | 提案采纳率 | 预期质量 | ↑ 目标 >50% |
| 2 | 自治率（无审批行动占比）| 信任深度 | ↑，护栏：纠正/回滚率 <2% |
| 3 | 单位价值认知成本 | 复利在发生 | ↓ 持续 |
| 4 | "从零回答"率（已告知事项被再问）| 连续性 | → 0 |
| 5 | 换脑回归数（回放）| 身份在状态里 | → 0 |
| 6 | salvaged 交账率 | 退出契约健康度 | <2% |
| 7 | WakeUser 漏报 | 路由安全 | = 0（零容忍）|

实现：economy costs + replay 情节记录产出 1/3/5/6；2/4/7 由 journal 与 attention_feedback 推导。

## 七条宪法不变量速查（[00-overview.md](00-overview.md)）

| # | 不变量 | 一句话 | 落地 |
|---|---|---|---|
| 1 | 状态在外 | world 是唯一真相 | 单 SQLite，凭据环境注入 |
| 2 | 换脑无感 | 模型可换不改身份 | CognitiveKernel 接缝，mind.Provider 抽象 |
| 3 | 交账强制 | 每情节必产 Outcome | episode_close 退出契约 + salvage |
| 4 | 可逆优先 | 可逆自由跑，不可逆永人签 | 可逆性分级 + trust 天花板 + revert 矩阵 |
| 5 | 认知是贵的 | 确定性能办的不用 LLM | internal.* 分支离认知路径 |
| 6 | 本地主权 | 数据不外流 | 本地 DB，无遥测，环境凭据 |
| 7 | 不替模型思考 | 框架给契约不给内容 | 裸 ReAct，不注入推理步骤 |

**两例外**：退出契约（强制 episode_close）+ 客观判据（有测试直接跑写 verified）。

## 设计不变量速查

| 不变量 | 出处 |
|---|---|
| 依赖单向零环（外→内）| [01-architecture.md](01-architecture.md) |
| chat 与 autonomous 共用拦截链，无治理绕过 | [16-channels-agent.md](16-channels-agent.md) |
| 拦截链序 permission→hook→user_hooks→read_before_edit→verify→audit→action→activity | [15-tools.md](15-tools.md) |
| 高风险 kind 硬白名单永唤醒（结构保证）| [03-attention.md](03-attention.md) |
| 成本异步 observational，绝不延迟情节 | [12-economy.md](12-economy.md) |
| 看门狗失败安全良性默认绝不误唤醒 | [13-selfops.md](13-selfops.md) |
| 价值走 world 不走成本台账（world 唯一真相）| [12-economy.md](12-economy.md) |
| 自我修改各有单独 revert | [20-security-governance.md](20-security-governance.md) |

---

← 返回 [README.md](README.md) 索引 · [00-overview.md](00-overview.md) 总览
