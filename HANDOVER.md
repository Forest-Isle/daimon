# Daimon 实施进度与接续指南

> 基线: `refound/daimon` @ `8cd046c`,工作树干净
> 模型: Claude Sonnet 4.6(1M context)
> 日期: 2026-06-12
> 产出文档: `DAIMON_BLUEPRINT.md`(完整蓝图),`HANDOVER.md`(本文件)

---

## 一、项目状态总览

Daimon 已从 coding-agent runtime 重铸为**主权个人代理**,完成了蓝图 Phase 0–2 的全部数据层和核心逻辑,并通过了全量测试(`make test` CGO+fts5+race 0 失败)。

**当前架构(已实现,在活路径上):**

```
TUI/Telegram 消息进 → agent.HandleMessage(锁/session)
  ├─ agent.SetKernel(episodeRunner) → episode.Execute(CognitiveRequest)
  │    ├─ composeSystem(人格/规则/记忆/identity/事项/episode_close 指令)
  │    ├─ 裸 ReAct(stream→tool_calls→loop)
  │    ├─ 工具走 a.invokeTool → InterceptorChain(permission/hook/user-hook/verify/action/audit/activity)
  │    │    └─ action interceptor: 分类可逆性 → 记录信任战绩 → 盖 action_class receipt
  │    ├─ episode_close 强制交账(schema 校验 status/summary/world_writes)
  │    └─ salvage 兜底(provider JSON 提取 → 转录本启发式)
  ├─ 失败回退 legacy LinearLoop
  └─ 发布 ProviderExchange/ToolRoundTrip → telemetry 录制 → ~/.daimon/replays/
```

**三器官(并行子系统,逻辑就绪,待集成进实时路径):**

```
heart(event 流:持久化→去重→崩溃恢复) ─┐
attention(路由:rules→model→cognize 兜底) ─┼─→ 待 P2-5 集成
action(信任账本/hold 队列,拦截器已接线) ─┘
```

## 二、已提交(12 commits,按顺序)

```
8cd046c refound(p2-4): attention router (rules → model → cognize default)
0a62547 refound(p2-3): heart event substrate (persist-before-route + dedup + recovery)
36db1f5 refound(p2-2): action interceptor records trust track record on tool calls
1f1eeb1 refound(p2-1): action layer data foundation (trust ledger, undo, holds)
872a870 fix(episode): reintegrate kernel with runtime cross-cutting assets
5fbb13a refound(p1-4): episode kernel goes live; remove plan/Reflexion/fact-extraction scaffolding
876cf14 refound: IronClaw → Daimon, P0+P1 core
63166d2 feat(tui): render cache, CJK width, dynamic input height, tool activity visibility
ea63cbc feat(provider): Claude extended-thinking round-trip channel
b4d32a2 refactor(agent): remove vestigial agent.mode config and /mode command
fb49378 refactor(config): drop project-folder config override, global only
f49435b fix(setup): drop single-option mode step, always write base_url
```

`876cf14` 及之后的 7 个 commit 是本次改造的全部增量。

## 三、新增/修改清单

### 新增包(6 个)

| 包 | 作用 | 状态 |
|---|---|---|
| `internal/appdir` | `~/.daimon` 路径 + 迁移常量的单一来源 | 已提交 |
| `internal/world` | 世界模型:commitments/journal 表 + Store + Identity 文件层 | 已提交 |
| `internal/episode` | 认知内核:Runner(裸 ReAct+episode_close)+Composer+salvage | 已提交 |
| `internal/action` | 行动层:信任账本/undo/holds + Classifier + 拦截器 | 已提交 |
| `internal/heart` | 事件心脏:Event 持久化/去重/崩溃恢复/TimerSource | 已提交 |
| `internal/attention` | 注意力路由:RulesRouter/LLMModelRouter/Chain/FeedbackStore | 已提交 |

### 新增迁移(4 个)

| 迁移 | 表 | 状态 |
|---|---|---|
| 027 | commitments, journal | 已提交 |
| 028 | trust_ledger, undo_journal, holds | 已提交 |
| 029 | events(partial unique on dedup_key, unrouted 索引) | 已提交 |
| 030 | attention_feedback | 已提交 |

### 改造的核心文件

| 文件 | 改动 |
|---|---|
| `internal/agent/agent.go` | Agent 加 kernel/kernelEnabled 字段;runKernel 注入 persona/rules/memories/transcript;invokeTool 加 recordToSession 参数 |
| `internal/agent/cognitive.go` | **新增**:CognitiveKernel 接口(use-site),CognitiveRequest/CognitiveOutcome/ToolInvokeFunc |
| `internal/gateway/gateway.go` | 删 episode 分支(``crand`/`msgToEpisodeState`/`newULID`),加 `gw.agent.SetKernel` |
| `internal/gateway/subsystem_tool.go` | 加 `ts.ActionStore`,拦截链加 `action.Interceptor` |
| `internal/tool/interceptor_verify.go` | 删 planStore 参数 |
| `internal/agent/events.go` | 删 ReflectionTriggered |
| `configs/daimon.example.yaml` | `agent.episode_enabled: true`(替代 `max_reflections`),`telemetry.replay_enabled/dir` |

### 删除的文件

| 文件 | 原因 |
|---|---|
| `internal/agent/reflection.go`+test | Reflexion 自纠被 episode 退出契约取代 |
| `internal/gateway/plan_store.go` | plan 功能被 commitment 工具取代 |
| `internal/tool/plan.go`+test | 同上 |
| `internal/memory/facts.go` 的 LLMFactExtractor 调用 | 逐消息 LLM 抽取被 episode 交账 WorldWrites 取代(类型保留供 sleep 用) |
| `internal/agent/plan_injection_test.go` | plan 注入已删除 |

## 四、当前实时路径的完整机制清单

| 维度 | 状态 | 证据 |
|---|---|---|
| Per-session 互斥锁 | ✅ | kernel 跑在 HandleMessage 内,继承 sessionLocks |
| Session 持久化 | ✅ | HandleMessage 不变:AddMessage → Persist → SessionEnded |
| 权限审批 | ✅ | invokeTool → InterceptorChain(permission 拦截器) |
| Hook 执行 | ✅ | invokeTool → hook + user-hook 拦截器 |
| 审计日志 | ✅ | invokeTool → audit 拦截器 |
| 行动归属(信任账本) | ✅ | invokeTool → action 拦截器(记录+盖 receipt) |
| 回放录制 | ✅ | episode 发布 ProviderExchange/TurnClosed,invokeTool 发布 ToolRoundTrip→telemetry 录制 |
| 人格注入 | ✅ | CognitiveRequest.Persona(Soul.md)→composeSystem |
| 规则注入 | ✅ | CognitiveRequest.Rules(Memory.md)→composeSystem |
| 记忆检索 | ✅ | CognitiveRequest.Memories(buildMemoryPromptSection)→composeSystem |
| 事项 digest | ✅ | composeSystem 从 world.Store.CommitmentsDigest 实时组装 |
| 身份 digest | ✅ | composeSystem 从 world.Identity.Digest 实时组装 |
| 退出契约 | ✅ | episode_close 强制 schema 校验 + salvage 兜底 |
| 优雅降级 | ✅ | kernel 失败/Status=failed → 回退 legacy LinearLoop |
| 配置开关 | ✅ | `agent.episode_enabled: false` 整体回退旧路径 |

## 五、蓝图模块完成度

| 蓝图模块(§4) | 完成度 | 说明 |
|---|---|---|
| chapter(改名) | ✅ 100% | P0-A |
| episode(§4.3) | ✅ 100% | P1-3 + P1-4 + 集成修复 |
| world(§4.4) | ✅ 80% | 数据层+tools+Identity;检索门面(x)待 sleep 阶段 |
| action(§4.6) | ✅ 70% | 数据层+拦截器(记录);hold execution(x),trust-gated approval(x),undo execution(x) |
| heart(§4.1) | ✅ 70% | Event/Source/Store/Heart/TimerSource;channel→Source 适配(x),接入实时路径(x) |
| attention(§4.2) | ✅ 70% | Rules/LLMModel/Chain/Feedback;接入 heart→episode 实时分发(x) |
| replay(§4.10) | ✅ 30% | P0-B 录制(ProviderExchange/ToolRoundTrip/TurnClosed);harness/回放/回归集(x) |
| sleep(§4.8) | ⬜ 0% | reconcile/rollup/drift/digest/distill 全(x) |
| proposals(§4.9) | ⬜ 0% | 模拟情节/提案队列(x) |
| mind(§4.7) | ✅ 60% | Provider 抽象存在;shadow(x),分档(x),缓存协商(x) |
| values(§4.5) | ⬜ 0% | 价值条目/问一次/漂移检测(x) |
| economy(§4.11) | ⬜ 0% | 成本核算/ROI/节流(x) |
| selfops(§4.12) | ⬜ 0% | 看门狗/金丝雀/回滚(x) |

## 六、下一步实施路线

优先级按蓝图 §7 路线图第 12–13 周+:

### 6.1 P2-5 heart→attention→episode 实时集成(Phase 2 收口)

**这是最近的一步,也是"从编码工具到常驻代理"的关键转折。**

需要做的:
1. **heart 接入 gateway 生命周期**:`gateway.New` 建 `heart.Store`,注册 `TelegramSource`/`TUISource`/`TimerSource`。每个 Source 把入站消息/定时事件 emit 为 Event。
2. **attention.Chain 作为 heart.Handler**:事件投递给 Chain→Route,根据 Verdict 路由——`Cognize`→点燃 episode,`Ignore`→跳过,`Reflex`→执行 `agent.heart.reflexes[reflex_id]` 配置的 deterministic tool-workflow,`WakeUser`→推 Notification。
3. **channel→Source 适配器**:telegram/tui 各包一层 `heart.Source` 实现(不删旧 channel 路径,绞杀器)。
4. **handleInbound 分流**:heart 路径和旧直接路径共存(heart 路径默认关,`agent.heart_enabled` 配置开关),开关开时才走 heart→attention→episode 链。
5. **早报 timer**:`TimerSource{Kind:"internal.daily_brief", Interval:24h}` 注册后走 deterministic `deliverDailyBrief`; 自定义反射由 `ReflexID` 映射到显式 workflow。

**涉及文件**:`internal/gateway/gateway.go`(heart/attention 初始化+生命周期),`internal/channel/tui/adapter.go`/`telegram/adapter.go`(Source 适配),`internal/gateway/subsystem_heart.go`(新增),`configs/daimon.example.yaml`(`agent.heart_enabled`)。

**风险**:碰 TUI/telegram 实时路径;改 gateway 初始化。建议先小验证(cat 一个事件,手动喂进 heart,看它走到 episode),再上实时。

### 6.2 replay harness(Phase 3 先行)

P0-B 录制器已持续写数据到 `~/.daimon/replays/`。下一步让这些数据可测:回放 harness 加载历史 JSONL→重打分(feed 给影子模型,评测 Outcome 质量)+ 回归集(用户纠正过的 episode 自动入集)。

### 6.3 sleep reconcile + digest + distill(Phase 3 核心)

sleep 是**复利飞轮**——离线作业让代理跑得越久越便宜。优先级:
1. **reconcile**:记忆衰减/巩固/矛盾和解(P1-2 全部内容,从热路径移出)
2. **digest**:重算 identity digest + commitments digest(Composer 当前读实时文件,没问题但昂贵;缓存进 digest.md 降频)
3. **distill**:扫描 journal 重复≥3 次且全 verified 的情节模式→生成 workflow spec/SKILL.md→金丝雀注册(首次 hold)→转正进 attention 反射表

### 6.4 mind.shadow(Phase 3)

影子脑:eventBus 复制事件流给候选模型只推理不执行,回放评测周报对比。这是换脑无感的前提。

### 6.5 Phase 4 器官(按价值排序,不设死线)

- economy 月报与节流
- selfops 看门狗与金丝雀回滚
- calendar/fs/webhook 感官(新 Source 实现)
- 本地路由模型替换 haiku 降本
- values 漂移检测成熟
- A2A(等协议生态,不抢跑)

## 七、关键接口(改代码前必读)

### CognitiveKernel(use-site,在 agent 包定义)

```go
// internal/agent/cognitive.go
type CognitiveKernel interface {
    Execute(ctx context.Context, req CognitiveRequest) (CognitiveOutcome, error)
}
type ToolInvokeFunc func(ctx context.Context, iteration int, call ToolUseBlock) (output string, isError bool)
type CognitiveRequest struct {
    SessionID, Goal, Trigger, Persona, Rules, Memories, Model, Provider string
    Transcript []CompletionMessage
    ToolDefs   []ToolDefinition
    Invoke     ToolInvokeFunc
}
```

episode.Runner 实现此接口。**所有 runtime 资产(persona/rules/memories/transcript/工具/拦截链/审批/录制)由 agent 组装并注入 request**,kernel 自身零子系统依赖。

### heart.Source(长驻事件发射器)

```go
// internal/heart/heart.go
type Source interface {
    Name() string
    Run(ctx context.Context, emit func(Event)) error  // 长驻,断线自重连
}
```

### attention.Router(任意实现)

```go
type Router interface {
    Route(ctx context.Context, ev heart.Event) (Verdict, error)
}
```

Chain 是默认组合实现(rules→model→cognize),可整体替换。

### action.Store(信任升降级——确定性规则,不经模型)

- `promotionThreshold`: `AskEvery→AskFirst` 需 1 次 verified,`→HoldThenAuto` 需 3 次,`→FullAuto` 需 10 次
- `classCeiling`: Irreversible 封顶 HoldThenAuto(永不 full auto);其余封顶 FullAuto
- `RecordCorrection`: 降一级 + 永久冻结升级(corrected>0)

## 八、构建与验证

```bash
make build-bin           # 编译二进制 bin/daimon
make vet                 # 静态检查
make test-short          # 快速测试(无 race)
make test                # 完整: CGO + fts5 + race
CGO_ENABLED=1 go test -tags fts5 -race ./internal/...  # 全部内部包
```

## 九、用户目录

```
~/.daimon/               (从 ~/.ironclaw 迁移,有 compat symlink ~/.ironclaw→~/.daimon)
  config.yaml
  world/identity/        (digest.md + preferences/*.md,代理可自编辑)
  skills/                (SKILL.md 技能库)
  agents/                (子 agent 定义)
  replays/               (P0-B 录制: YYYY-MM-DD.jsonl)
  data/daimon.db         (SQLite,含全部迁移)
  memory/                (文件存储)
```

`~/.daimon` 已 git 化(genesis commit 在 876cf14),`.gitignore` 排除了 `data/`/`replays/`/`traces/`/`audit/`。

## 十、已知限制/风险(实施时注意)

1. **episode 路径在 TUI 端尚未做端到端真机验证**——所有结构和单测通过,但没跑过一轮真实交互对话。首次实跑建议用 `agent.episode_enabled: false` 回退,确认体验正常再逐步开。
2. **action 拦截器只记录不 enforce**——trust level 在累,但没有 action 去查它来决策是否放行。等 compensable/irreversible 工具(发邮件/支付)到位后,在 permission 拦截器里加 trust-gated approval。
3. **heart/attention 是并行子系统**——测试 green,但没接进实时入站路径。P2-5 集成碰 gateway 初始化 + channel 适配,风险最高。
4. **world 检索门面缺失**——Composer 用 `Identity.Digest()+CommitmentsDigest()`,但没有跨三层(journal+身份层)的向量检索。sleep 阶段补。
5. **replay 录制数据在积累,但无回放工具读它们**——`~/.daimon/replays/` 有 JSONL,但只能手查。build replay harness 是 P1-5 的内容。
6. **codex relay 账号池紧张**(3 账号,间歇 502),后续自行实施时不要依赖 codex 派发。
7. **Telegram 是单一主渠道单点**,TUI 永远可用备援。

## 十一、接续会话的推荐第一步

1. `git log --oneline -3` 确认在 `8cd046c`
2. `make test` 确认全绿
3. 读 `DAIMON_BLUEPRINT.md` 获取完整上下文
4. 决定:做 P2-5(集成 heart→attention→episode,风险最高但价值最大)还是先做 replay harness(低风险,让已有的录制数据可见)还是 sleep reconcile(复利飞轮)
