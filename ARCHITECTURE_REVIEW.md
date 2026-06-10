# IronClaw 架构评审与改造路线

> 生成日期: 2026-06-10
> 范围: 全链路流程 / 架构模式 / 模块设计 对标业内成熟实现与前沿探索
> 用途: 作为后续改造的驱动文档。每个改进项含 **目标 / 方案 / 涉及文件 / 验收标准 / 估算**,可逐项推进。
> 基线: `main` @ 9534651(已完成 Slim Phase 7,移除 worktree/scheduler-pkg/observability/sandbox/taskledger/evolution)
>
> **进度(2026-06-10 更新):** ✅ P0-2 (loop 诚实化,采方案 B) · ✅ P0-1 (计划工具 + 验证闭环) · ✅ Reflexion 自纠(§三-A 补完)已落地。
> 详见各小节的「✅ 已完成」标注与 §六 变更记录。

---

## 目录

1. [全链路流程](#一全链路流程)
2. [架构模式评估](#二架构模式评估)
3. [对标差距与不足](#三对标差距与不足)
4. [改进路线图(P0/P1/P2)](#四改进路线图)
5. [附录:关键文件索引 / 对标表](#五附录)

---

## 一、全链路流程

一条用户消息的端到端路径:

```
Channel(TUI / Telegram / Scheduler) ──inbound──▶ gateway.handleInbound
  ├─ commands.Dispatch                                  斜杠命令短路
  └─ agent.HandleMessage
       ├─ per-session 互斥锁  key="channel:channel_id"   串行化同会话
       ├─ session.Get          内存 sync.Map → SQLite
       ├─ AddMessage(role=user)
       ├─ buildSystemPrompt
       │     人格 + 系统提示 + 规则
       │     ‖ <!-- DYNAMIC_CONTEXT --> ‖              缓存边界
       │     记忆检索(top5) + 技能清单 + 子agent清单
       ├─ Hook.FireOnUserMessage                        注入 git/workdir 上下文
       ├─ ContextMgr.Compress                           按阈值跑分层压缩
       └─ strategy.Execute (Unified / Simple)
            └─ for iteration < maxIter(默认20):
                 ├─ publish MetricsTick
                 ├─ provider.Stream → 累积 text + toolCalls
                 │     └─ 413 → ReactiveCompress → 重试
                 ├─ AddMessage(assistant) / AddMessage(tool_use)
                 ├─ len(toolCalls)==0 ⇒ return          自然收敛
                 └─ dispatchTools  Unified=并行 / Simple=串行
                      └─ executeToolCall
                           └─ Interceptor.Execute
                                permission → hook → user-hook → verify → audit
                                → approvalFn → tool.Execute(直接落地宿主)
       ├─ memory.Save(user msg)
       ├─ go extractFacts                               异步: LLM 抽事实 → Lifecycle 入库
       └─ session.Persist                               SQLite
```

**本质:** 经典 ReAct 单环 + 分层上下文压缩 + 拦截链工具治理 + 混合检索记忆,组合根集中装配。工程完成度高,**执行内核的"智能"层很薄**。

关键事实:
- `maxIter` 默认 20,到顶即停;唯一的"自我管理"是 `computeBudgetPressure` 往工具结果末尾塞一句"预算告急"文字。
- 记忆检索发生在 **消息级**(`buildSystemPrompt` 每条用户消息一次),循环内迭代不再检索。
- 工具直接在宿主执行,无沙箱(见 §三-B)。

---

## 二、架构模式评估

### 2.1 做得扎实的部分(保留)

| 维度 | 实现 | 评价 |
|---|---|---|
| 组合根 | `gateway.New` 单点装配,subsystem + `init_*.go` 显式接线,依赖用指针 `*AgentDeps` 后期回填 | 清晰、可测、低耦合 |
| 工具治理 | `InterceptorChain` 责任链 `permission→hook→user-hook→verify→audit` | 正交解耦,易扩展 |
| 接口边界 | `Provider` / `Channel` / `LoopStrategy` / `Tool` 四个干净接口 | 边界合理 |
| Tool 能力模型 | `Capability`:`IsDestructive` / `ParallelSafety` / `PathScoped` | 有前瞻性 |
| 记忆检索 | FTS5 + 向量 RRF 融合(k=60,`0.7*rrf + 0.3*strength`) | 务实的工业做法 |
| 上下文韧性 | 413 反应式压缩 + tiktoken 精确计数 + tool_use/tool_result 配对修复 | 细节到位 |

### 2.2 结构性问题(需处理)

1. **~~LoopStrategy 形同虚设。~~ ✅ 已修复(P0-2,方案 B)。** 原 `SimpleLoop` 与 `UnifiedLoop` 仅差"串行 vs 并行派发工具",`SetMode` 中 `cognitive` 直接映射到 `UnifiedLoop`,三种 mode 实际坍缩为 2 种行为。**现已合并为单一 `LinearLoop`(默认并行派发),删除 `SimpleLoop`/`UnifiedLoop` 及死代码 `unifiedNonStreaming`;`agent.mode` 取 `linear`(canonical),旧值 `simple`/`unified`/`cognitive` 兼容映射到 `LinearLoop`。抽象名实相符,无死分支。**

2. **Agent 是共享可变单例。** `SetStrategy/SetModel` 原地改全局状态,靠 per-session 锁兜底。单用户够用,但运行时配置与执行状态耦合在一个对象上。

3. **记忆检索与主环解耦不足。** 检索是"消息级"而非"步骤级",长任务后续迭代拿不到新相关记忆/代码上下文。

---

## 三、对标差距与不足

按"对智能体能力的影响"排序。每条:**现状 → 对标 → 影响 → 证据**。

### A. 执行内核过于朴素 ⭐ 最该突破 — ✅ 已完成(P0-1 + Reflexion 自纠)

- **现状(原):** 纯 ReAct `while(tool_calls){...}`,maxIter 到顶即停;唯一"自我管理"是 `computeBudgetPressure` 往末尾塞预算告警文字。**无显式计划、无 TODO 状态、无反思、无完成度校验、无失败自纠。**
- **✅ 已落地(P0-1):** 新增 `plan` 工具(create/update/status,step 带 `success_criteria`,状态存 `session.Metadata["plan"]`),始终注册可用;`buildSystemPrompt` 每条消息注入当前 plan;`VerifyInterceptor` 升级为 plan-aware,写类工具后追加验证提示回灌上下文。
- **✅ 已落地(Reflexion 自纠):** `LinearLoop` 收敛点(无 tool_calls)增加自我批判:若当前 plan 仍有未完成步骤(非 done/failed),注入一条 grounded 在各步骤 `criteria` 上的自检提示并继续循环,要求模型逐步验证或显式标记。由 `agent.max_reflections` 约束(默认 3,负值禁用);计数为**每任务局部变量**(不污染会话状态);`maxIter` 仍是硬上限。**无 plan 时零行为变化。**
- **🟡 仍可深化:** 推理预算(o-series reasoning tokens)、完成度量化评分、强制验证执行(目前是"提示模型去验"而非框架强制跑 test_run)。
- **对标:** Claude Code 有 `TodoWrite` 显式计划状态机 + plan mode;Codex/OpenHands 有 planner;前沿 Reflexion(自我批判重试,**已实现 plan-grounded 版本**)、Plan-and-Solve、Tree-of-Thoughts、o-series 推理预算。
- **证据:** `internal/tool/plan.go`(plan 工具);`internal/agent/agent.go:buildSystemPrompt` + `executeToolCall`(注入/回灌);`internal/tool/interceptor_verify.go:buildPlanVerifyHint`;`internal/agent/reflection.go`(`maybeReflect`/`injectReflection`/`incompletePlanSteps`)+ `linear_loop.go`(收敛点接线);`internal/agent/events.go:ReflectionTriggered`(可观测)。

### B. 零隔离的宿主执行 ⭐ 最该补的安全缺口

- **现状:** `bash` 走 `exec.CommandContext(ctx,"bash","-c",cmd)` 直接落地宿主;文件工具无路径围栏,`file_read/write` 可读写任意路径(含 `~/.ssh`、`/etc`);命令拦截仅子串黑名单(`policy.CheckBashCommand`),`r\m`/别名/`bash -c` 即可绕过;"always approve" 仅存内存。
- **对标:** Codex CLI 默认 Landlock(Linux)/Seatbelt(macOS)沙箱 + 三档审批;Claude Code 持久化权限 + bash 沙箱;Devin/OpenHands 跑在容器/VM。
- **影响:** 当前安全姿态 = "完全信任模型 + 子串黑名单"。本地单人玩具可接受,但任何"半自治/远程触发(Telegram/Scheduler)"场景都是真实命令执行风险面。**这是从个人工具走向产品的硬门槛。**
- **证据:** `internal/tool/bash.go:94`;`internal/tool/tool.go:235-244` `ResolveWorkPath` 无 `..` 校验;`internal/tool/permissions.go` 子串黑名单;Telegram "always approve" 仅 in-memory `sync.Map`。

### C. Provider 抽象漏掉"推理通道"

- **现状:** `Provider` 接口只有 `Complete/Stream`,数据结构**无 thinking/reasoning 字段**;prompt cache 的 `cache_control` 硬编码在 Anthropic 路径,靠 system prompt 里 `<!-- CACHE_BOUNDARY -->` 注释切分——Anthropic 专属漏抽象。仅 Claude + OpenAI。
- **对标:** 现代 Agent 普遍把 extended thinking(Claude)/ reasoning tokens(o-series)作为一等公民透传与展示;缓存策略应是 Provider 能力协商而非提示词魔法注释。
- **影响:** 拿不到/展示不了推理过程,调试困难;接入新供应商要改核心数据结构;缓存正确性依赖注释位置,脆。
- **证据:** `internal/agent/provider.go:64-90`(`CompletionResponse`/`StreamDelta` 无 thinking);`internal/agent/context_manager.go:17-18` 缓存标记;`internal/agent/claude_provider.go` `cache_control` 注入。

### D. 记忆系统:有检索,无"学习"

- **现状:** `strength` 恒为 1.0,**无时间衰减、无访问强化**;矛盾检测让 LLM 标 `ConflictingIDs` 但**只执行单条 ADD/UPDATE/DELETE,不做多记忆和解/合并**;去重用字符重叠率 `>0.8` 非语义;`CachedStore` 的 key 只含 `Text+Limit`,**忽略 scope/userID** → 跨用户/跨作用域假命中(正确性 bug);每条消息都异步抽事实,成本不低。
- **对标:** Letta/MemGPT 自编辑记忆 + in-context/archival 分页;mem0/Zep 时序知识图谱 + 事实和解 + 衰减。
- **影响:** 记忆只会"堆积 + 检索",不会"记住什么重要、遗忘什么过时";矛盾事实并存;缓存键缺隔离维度有数据串扰风险。
- **证据:** `internal/memory/file_store.go`(strength 默认 1.0 无衰减);`internal/memory/lifecycle.go`(`ConflictingIDs` 仅标记不和解);`internal/memory/retriever.go`(字符重叠去重);`internal/memory/cache.go:41-42`(key=`Text+Limit`)。

### E. 上下文工程缺"卸载"维度

- **现状:** 压缩三层(工具输出削减 → 旧半摘要 → 紧急截断)做得不错,但都是**有损摘要**,摘要前还把每条截到 500 字符。主环无 sub-agent 上下文卸载机制。
- **对标:** Claude Code 把重搜索/重读派给 sub-agent,主上下文只收结论。
- **影响:** 长会话主上下文必然被有损摘要侵蚀;关键早期细节可能在"旧半摘要"中丢失且不可逆。
- **证据:** `internal/agent/compression.go`(三层 pipeline);`internal/agent/context_manager.go:261`(摘要前 `TruncateStr(...,500)`)。

### F. 多智能体编排很浅

- **现状:** 仅 `Spawn`/`SpawnParallel`(FailFast),子 agent 进程内、隔离会话、scoped tools、空输出重试一次。结果靠 LLM 摘要回收。**无共享黑板、无 agent 间消息(MessageBus 已删)、无层级规划、无 DAG/状态机。**
- **对标:** LangGraph 显式状态图 + checkpoint + human-in-loop;Swarm/Agents-SDK handoff;CrewAI/AutoGen 角色协作。
- **影响:** 只能做"扇出-摘要"一种拓扑,做不了"规划→分派→交叉验证→综合"的复杂工作流。
- **证据:** `internal/agent/subagent.go:42`(`Spawn`)、`:124`(`SpawnParallel`)、`:106`(空输出重试)。

### G. 可观测性与评测:基本归零 ⭐ 对 runtime 最伤

- **现状:** 有 EventBus(`MetricsTick`/`ToolExecuted`)但**无 OTel/无 trace/无 cost 核算导出**;eval harness 已在瘦身中删除。
- **对标:** OTel GenAI semconv + Langfuse/LangSmith trace;SWE-bench/terminal-bench/τ-bench 回归。
- **影响:** **无法量化"改了 prompt/策略后 agent 变好还是变坏"。**对一个 agent runtime,这是最伤的工程缺失——改进全凭手感。
- **证据:** `internal/agent/events.go`(事件已有,无 exporter);`cmd/ironclaw/` 已无 `eval.go`。

### H. 工程细节

- **流式脆弱:** TUI 50ms 轮询、Telegram 1s 编辑节流**静默丢更新**;工具流式仅 bash 接线。
- **Hook 不可用户脚本化:** 核心 hook 是编译进二进制的 Go 工厂(`safety_analyzer`/`git_context` 等),不如 Claude Code 的 shell-command hook 灵活(虽有 user-hook 拦截器补了一点)。
- **流错误吞掉:** stream 出错只 `Finish("Error:...")` 返回 nil,环内除 413 外无瞬态重试恢复。
- **证据:** `internal/channel/tui/adapter.go`(50ms pump)、`internal/channel/telegram/adapter.go`(1s 节流);`internal/agent/loop_common.go:71-75`(吞错)。

---

## 四、改进路线图

> 优先级:**P0** = 决定 agent 是否"可信赖"的核心缺口;**P1** = 显著提升能力/正确性;**P2** = 按需的架构升级。
> 每项均可在现有接口边界内增量落地,不需推倒重来。

---

### P0-1 · 显式计划/TODO 状态 + 验证闭环 — ✅ 已完成(2026-06-10)

**问题:** §三-A。框架不提供 plan-act-verify 循环,违背项目自身 Goal-Driven 原则。

**目标:** 多步任务有可检视的计划产物;写类操作后能自动判定"是否达标"。

**实际落地:**
1. ✅ 新增 `plan` 工具:模型维护 step 列表(每步带 `criteria`/`status`),操作 create/update/status,状态存 `session.Metadata["plan"]`;在 `InitTools` 始终注册(不受 feature 开关影响)。
2. ✅ `VerifyInterceptor` 升级为 plan-aware:写类工具(`file_write/edit/patch`、写命令 `bash`)执行后,若 plan 有 `in_progress` 且 criteria 含 test/lint/build 的步骤,生成 `plan_verify_hint` 写入结果 Metadata;`executeToolCall` 把 hint 追加到工具结果内容,回灌下一轮上下文。**采"提示模型去验"而非"强制执行 test_run",保持模型自主、避免误触发。**
3. ✅ `buildSystemPrompt` 每条消息注入当前 plan(含 nil-session 守卫),模型每轮都看到最新进度。
4. ✅ session ID 经 `tool.WithSessionID` 透传至工具上下文,使 plan 存储按会话隔离。

**涉及文件(实际):** 新增 `internal/tool/plan.go`、`internal/gateway/plan_store.go`;改 `internal/tool/interceptor_verify.go`(`buildPlanVerifyHint`)、`internal/tool/tool.go`(`WithSessionID`/`SessionIDFromContext`)、`internal/session/session.go`(`SetMetadata`/`GetMetadata`)、`internal/agent/agent.go`(`buildSystemPrompt` 注入 + `executeToolCall` 回灌)、`internal/gateway/subsystem_tool.go`(注册 + 接线)。

**验收标准:**
- ✅ 计划生命周期(create→update→complete)、验证提示生成/边界、错误处理:`internal/tool/plan_test.go`(5)、`internal/agent/plan_injection_test.go`(3)、`evals/eval_test.go`(5)覆盖。
- ✅ `make build-bin && make vet && make test`(race)全绿(19/19 packages)。

**估算(原):** 中(3–5 天)。 **实际:** 当日完成(采"提示式验证",未做强制 test_run / Reflexion 重试,留待后续)。

---

### P0-2 · LoopStrategy 做实或砍掉(诚实化) — ✅ 已完成(2026-06-10,采方案 B)

**问题:** §二-2.2-1。三 mode 坍缩为 2 行为,抽象名实不符。

**目标:** 要么让 `cognitive` 真正承载差异化策略,要么删除 vestigial mode,保留一个诚实的环。

**实际落地(方案 B):**
- ✅ 删除 `SimpleLoop`(`simple_loop.go`)与 `UnifiedLoop`(`unified_loop.go`,含死代码 `unifiedNonStreaming`),合并为单一 `LinearLoop`(`linear_loop.go`,默认并行派发独立 tool_use)。
- ✅ `SetMode` 收敛:`linear` 为 canonical,`simple`/`unified`/`cognitive` 兼容映射到 `LinearLoop`;未知值返回错误。
- ✅ 配置/文档/TUI/setup 向导同步收敛到 `linear`;`LoopStrategy` 接口保留(图引擎 P2-1 仍会用)。

> **未采方案 A 的原因:** 真 `CognitiveLoop`(Reflexion 式 reflect 阶段)与 P0-1 强耦合且成本更高;先用最小改动消除"名实不符"的概念债,reflect/自纠留待 §三-A 的 🟡 后续项,避免一次引入过多未验证机制。

**涉及文件(实际):** 删 `internal/agent/simple_loop.go`、`unified_loop.go`;新增 `linear_loop.go`;改 `internal/gateway/gateway.go`(`SetMode`)、`commands.go`、`internal/config/config_agent.go`、`internal/agent/spec.go`、`internal/agent/subagent.go`、`configs/ironclaw.example.yaml`、`cmd/ironclaw/setup.go`、`internal/channel/tui/{commands,model,model_view,adapter}.go`、`CLAUDE.md` 及相关测试。

**验收标准:**
- ✅ B:`agent.mode` 取值与实际行为一一对应,无死分支(`gateway_test.go`/`suggestions_test.go`/`hierarchy_test.go` 更新)。
- ✅ `make build-bin && make vet && make test`(race)全绿。

**估算(原):** A 中(3–4 天);B 小(0.5 天)。 **实际:** 当日完成。

---

### P0-3 · 可插拔执行后端(可选沙箱)+ 路径围栏

**问题:** §三-B。零隔离宿主执行 + 无路径围栏 + 子串黑名单可绕过。

**目标:** 默认安全可配;远程触发场景可强制隔离。

**方案:**
1. 抽象 `ExecBackend` 接口,三实现:
   - `host`(现状,默认仅本地开发)
   - `sandboxed`:macOS `sandbox-exec`(Seatbelt)、Linux `bubblewrap`/Landlock
   - `container`:可选 Docker/Podman
2. 文件工具加 **workdir 路径围栏**:拒绝 `..` 逃逸,可配根前缀白名单。
3. bash 黑名单换 **AST 解析**(`mvdan.cc/sh`)替代子串匹配。
4. "always approve" **持久化** 到 `~/.ironclaw`(权限决策落盘)。

**涉及文件:** 新增 `internal/tool/exec_backend.go`;`internal/tool/bash.go`(走 backend)、`tool.go:ResolveWorkPath`(围栏)、`policy.go`(AST 解析)、`permissions.go`(持久化);`internal/config`(`tools.exec.backend`、`tools.file.roots`);`configs/ironclaw.example.yaml`。

**验收标准:**
- `tools.exec.backend=sandboxed` 时,`bash` 内 `cat /etc/passwd` / 写 workdir 外路径被拒。
- `file_write ../../x` 在围栏开启时被拒。
- `r\m -rf` 类绕过被 AST 解析识别。
- 重启后"always approve"仍生效。
- `make test` 全绿。

**估算:** 大(5–8 天;先做 macOS + 路径围栏 + AST,容器后置)。

---

### P1-1 · Provider 增加 reasoning 通道 + 缓存能力协商

**问题:** §三-C。

**目标:** 透传/展示推理;缓存策略去 Anthropic 耦合。

**方案:**
1. `StreamDelta`/`CompletionResponse` 增 `Thinking string`(或 `ReasoningSummary`),Claude thinking / o-series reasoning 透传;TUI 折叠展示。
2. 把 `cache_control` 从 system prompt 注释升级为 `Provider.CachePolicy()` 能力协商;`SplitSystemPrompt` 改由 Provider 决定切点。

**涉及文件:** `internal/agent/provider.go`、`claude_provider.go`、`openai.go`;`internal/agent/context_manager.go:SplitSystemPrompt`;`internal/channel/tui/`(展示)。

**验收标准:**
- Claude thinking 模型下,TUI 能看到推理摘要(可折叠)。
- 去掉 `<!-- CACHE_BOUNDARY -->` 注释后缓存仍正确(`GetCacheStats` 显示命中)。

**估算:** 中(3 天)。

---

### P1-2 · 记忆"会学习":衰减 + 强化 + 和解 + 修缓存键

**问题:** §三-D。

**目标:** 记忆会遗忘过时、强化高频、和解矛盾;消除跨用户串扰。

**方案:**
1. `strength` 引入 **时间衰减**(指数衰减)+ **访问强化**(检索命中即加权)。
2. 矛盾事实 **和解**:`UPDATE` 新事实 + `SoftInvalidate` 旧条,而非并存。
3. 去重换 **嵌入相似度**(已有 1536d 向量)替代字符重叠。
4. **修 `CachedStore` key**:纳入 `scope/userID/excludeTypes`(正确性 bug)。

**涉及文件:** `internal/memory/file_store.go`(衰减/强化)、`lifecycle.go`(和解)、`retriever.go`(语义去重)、`cache.go`(key)。

**验收标准:**
- 新增单测:旧未访问记忆 strength 随时间下降;高频命中记忆 strength 上升。
- 矛盾事实注入后,旧条被 `valid_to` 标记,检索只返回新条。
- 缓存键单测覆盖"同 Text 不同 userID 不串"。
- `make test` 全绿。

**估算:** 中(3–4 天)。

---

### P1-3 · 主环 sub-agent 上下文卸载

**问题:** §三-E。

**目标:** 大检索/大文件读不污染主上下文。

**方案:** 复用现成 `SubAgentManager`:当某工具结果超阈值(或属"探索类"操作)时,自动派一次性 sub-agent 执行并只回收结论。改的是"何时自动派发"的策略,非新机制。

**涉及文件:** `internal/agent/linear_loop.go` / `loop_common.go`(派发决策);`internal/agent/subagent.go`(复用)。

**验收标准:** 一次大 `grep`/大文件读任务,主会话 token 增量显著低于内联执行(用 `MetricsTick` 对比)。

**估算:** 中(2–3 天)。

---

### P1-4 · 可观测性:OTel GenAI trace + cost 核算

**问题:** §三-G。

**目标:** 每步可追踪,token/费用可核算(可选开启,不违背瘦身初衷)。

**方案:** EventBus 已有 `MetricsTick`/`ToolExecuted`/`SessionStarted/Ended`,接 OTel exporter 输出 per-step span(模型、token、缓存命中、工具耗时、费用)。开关由 feature/config 控制,默认关。

**涉及文件:** 新增 `internal/observe/otel.go`(订阅 EventBus);`internal/gateway/features.go`(feature 开关);`internal/config`(endpoint)。

**验收标准:** 开启后能在 OTel collector / Langfuse 看到一次会话的完整 span 树与 token 成本;关闭时零开销。

**估算:** 中(2–3 天)。

---

### P1-5 · 评测回归 harness ⭐ 解锁"可证明的改进"

**问题:** §三-G。没有它,前面所有改进无法证明有效。

**目标:** 改 prompt/策略后能量化好坏。

**方案:**
1. 自建 **黄金任务集**(10–30 个本仓库内可复现的编码/检索任务,带断言)。
2. 接 **SWE-bench-lite / terminal-bench** 子集。
3. CI 跑回归,产出 `pass@k` + 步数 + token 成本,落盘对比。

**涉及文件:** 新增 `cmd/ironclaw/eval.go`(重建)、`evals/`(任务集);CI 配置。

**验收标准:** `ironclaw eval --suite golden` 产出可对比报告;两次 prompt 改动间能看出指标差异。

**估算:** 中–大(4–6 天,任务集建设是主要成本)。

---

### P2-1 · 有向图执行引擎(按需)

**问题:** §三-F。复杂工作流做不了。

**目标:** 支持"规划→分派→交叉验证→综合"等拓扑。

**方案:** 在 `LoopStrategy` 之上引入轻量 **有向图执行引擎**(节点=agent/tool/判定,边=条件),带 checkpoint 与 human-in-loop 检查点;把外层 Workflow 能力下沉为 runtime 一等公民。**仅在确有复杂工作流需求时启动。**

**涉及文件:** 新增 `internal/orchestration/graph.go`;`internal/agent/loop_strategy.go`(图策略实现)。

**验收标准:** 能定义并执行一个"3 子agent 并行 + 投票综合"的图,带中途 checkpoint 恢复。

**估算:** 大(7–10 天)。

---

### 推进顺序建议

```
里程碑 1(可信赖内核):  ✅ P0-2 mode 诚实化 → ✅ P0-1 计划/验证闭环 → ✅ Reflexion 自纠(§三-A) → 🟡 P1-5 评测 harness(已起步:evals/ 固定断言;待建 golden set / SWE-bench)
里程碑 2(安全):        P0-3 沙箱 + 路径围栏  ← 下一步
里程碑 3(能力/正确性):  P1-2 记忆学习 → P1-1 reasoning 通道 → P1-3 上下文卸载
里程碑 4(运维):        P1-4 可观测性
里程碑 5(按需):        P2-1 图引擎
```

> 把 **P1-5 评测 harness 提前到里程碑 1**:它是验证 P0-1/P0-2 是否真的让 agent 变好的标尺,否则改进无法证伪。
> **现状:** `evals/` 已落地 5 个固定断言用例作为回归守卫(最小前置);完整 golden set / SWE-bench 子集待内核稳定后增量建设。

---

## 五、附录

### 5.1 关键文件索引

| 模块 | 文件 | 作用 |
|---|---|---|
| 组合根 | `internal/gateway/gateway.go` | 装配 / 生命周期 / handleInbound |
| 子系统装配 | `internal/gateway/subsystem_*.go`、`init_*.go` | 各子系统初始化 |
| 执行环 | `internal/agent/linear_loop.go`、`loop_common.go`、`reflection.go` | ReAct 主环(单一 `LinearLoop`)+ Reflexion 自纠 |
| 策略接口 | `internal/agent/loop_strategy.go` | LoopStrategy |
| Agent | `internal/agent/agent.go` | HandleMessage / buildSystemPrompt / executeToolCall |
| 依赖 | `internal/agent/deps.go` | Core/Memory/Security/MultiAgent Deps |
| Provider | `internal/agent/provider.go`、`claude_provider.go`、`openai.go` | LLM 后端 |
| 上下文 | `internal/agent/context_manager.go`、`compression.go`、`tokenizer.go`、`model_context.go` | 压缩/计数/窗口 |
| 子agent | `internal/agent/subagent.go`、`subagent_*.go` | 进程内子 agent |
| 工具 | `internal/tool/bash.go`、`file_*.go`、`http.go`、`code_intel*.go`、`test_run.go`、`memory.go`、`skill.go`、`plan.go` | 内置工具(`plan` = 计划/验证) |
| 计划存储 | `internal/gateway/plan_store.go` | `tool.PlanStore` → session.Metadata 桥接 |
| 拦截链 | `internal/tool/interceptor*.go`、`permissions.go`、`policy.go`、`resultstore.go` | 权限/钩子/校验/审计 |
| 记忆 | `internal/memory/file_store.go`、`retriever.go`、`facts.go`、`lifecycle.go`、`cache.go`、`openai.go` | 混合检索/事实/生命周期 |
| 渠道 | `internal/channel/channel.go`、`tui/`、`telegram/`、`scheduler/` | TUI/Telegram/定时 |
| 会话 | `internal/session/session.go`、`manager.go` | SQLite 会话 |
| 技能 | `internal/skill/skill.go`、`manager.go`、`builtin/` | 渐进式披露 SKILL.md |
| MCP | `internal/mcp/manager.go`、`server.go`、`adapter.go` | client(stdio) + server(stdio/http) |
| Hook | `internal/hook/hook.go`、`factory.go` | Go 函数式钩子 |
| 配置 | `internal/config/` | 分层合并 + 环境变量展开 |
| 特性 | `internal/feature/` | 子系统开关 |

### 5.2 对标速查表

| 能力 | IronClaw 现状 | 业内成熟 | 前沿探索 |
|---|---|---|---|
| 执行内核 | 朴素 ReAct 单环 | Claude Code TodoWrite/plan、Codex planner | Reflexion / Plan-and-Solve / ToT |
| 安全隔离 | 宿主直跑,子串黑名单 | Codex Landlock/Seatbelt、容器 | — |
| 推理通道 | 无 thinking 字段 | Claude thinking / o-series reasoning | — |
| 记忆 | FTS5+向量 RRF,无衰减/和解 | mem0 事实和解 | Letta/MemGPT 自编辑、Zep 时序图谱 |
| 上下文 | 有损分层摘要 | Claude Code sub-agent 卸载 | — |
| 多智能体 | 扇出-摘要 | Swarm/Agents-SDK handoff | LangGraph 状态图、AutoGen/CrewAI |
| 可观测 | EventBus 无 exporter | OTel GenAI semconv、Langfuse | — |
| 评测 | 无 | SWE-bench、terminal-bench、τ-bench | ADAS 自动设计 |

### 5.3 验证命令

```bash
make build-bin     # 编译
make vet           # 静态检查
make test-short    # 快速测试
make test          # 完整(CGO + fts5 + race)
```

---

## 总评

IronClaw 的 **工程骨架(组合根、拦截链、混合记忆、上下文压缩)已达良好工业水准**,瘦身后的克制是明智的。真正的短板集中在两处:**(1) 执行内核停在朴素 ReAct,缺计划/反思/验证闭环;(2) 安全与可评测性近乎为零。** 这两点正是"个人工具 → 可信赖 agent 产品"之间的鸿沟。

**P0 三件事(计划-验证闭环、mode 诚实化、可选沙箱)性价比最高,且都能在现有接口边界内增量落地。** 强烈建议把评测 harness(P1-5)提前,作为衡量一切改进的标尺。

---

## 六、变更记录

### 2026-06-10 · 里程碑 1(可信赖内核)首批

- **✅ P0-2 (方案 B) loop 诚实化:** 删除 `SimpleLoop`/`UnifiedLoop`(及死代码 `unifiedNonStreaming`),合并为单一 `LinearLoop`;`agent.mode` 收敛到 `linear`(旧值兼容)。消除"三 mode 坍缩两行为"的概念债。
- **✅ P0-1 计划工具 + 验证闭环:** 新增 `plan` 工具(create/update/status,step 带 criteria/status,存 session.Metadata);`buildSystemPrompt` 每条消息注入当前 plan;`VerifyInterceptor` 升级为 plan-aware,写类工具后追加验证提示回灌上下文;session ID 经 context 透传。采"提示式验证"而非强制 test_run。
- **✅ Reflexion 自纠(§三-A 补完):** `LinearLoop` 收敛点增加 plan-grounded 自我批判:模型停下但 plan 仍有未完成步骤时,注入基于各步骤 criteria 的自检提示并继续,由 `agent.max_reflections`(默认 3,负值禁用)约束;新增 `ReflectionTriggered` 事件。**无 plan 时零行为变化。**
- **附带回归守卫:** 新建 `evals/`(5 个固定断言用例)+ `internal/tool/plan_test.go`(5)+ `internal/agent/plan_injection_test.go`(3)+ `internal/agent/reflection_test.go`(8,含 2 个 loop 集成测试验证"按预算反思后停止"与"plan 完成则零反思")。**注:这是 P1-5 的最小前置,非完整 golden set / SWE-bench harness——后者待内核稳定后增量建设。**
- **🧹 清理孤儿 reflection scaffold:** 删除瘦身前遗留的**交互式 human-in-loop replan** 脚手架(`internal/agent/` 从未调用,与上面的自主 Reflexion 是两回事):`channel.ReflectionSender`/`ReplanDecision` + 常量、TUI `modeReflection`/`reflectionRequestMsg`/`handleReflectionKey`/`renderReflectionDialog`/`reflectionBoxStyle`、Telegram `SendReflectionRequest`/`pendingReflections`/`reflect_*` callback、config `ReflectModel` + `MemoryConfig.Reflection{Count,Drift,L2}Threshold`(纯声明从未读取)。约 120 行死代码。
- **🏷️ 重命名 `CognitiveConfig` → `ExecutionConfig`**(yaml `agent.cognitive` → `agent.execution`):该结构只剩 `MaxParallelTools`/`ApprovalTimeoutSeconds`/`StreamingEnabled` 三项执行期设置,与已废的 "cognitive" mode 无关,名实相符。顺手修正默认 `agent.mode` 从遗留的 `simple` → `linear`。
- **验证:** `make build-bin && make vet && make test`(CGO + fts5 + race)全绿,18/18 packages。

**🟡 仍可深化(§三-A 后续):** o-series 推理预算、完成度量化评分、框架强制验证执行(目前为提示式)。

**下一步建议:** P0-3(沙箱 + 路径围栏,里程碑 2)。
