# IronClaw 项目精简分析报告

> 生成日期：2026-06-08
> 当前代码库：~44,000 行（已删除 evolution/cogmetrics 等 ~11,500 行后）
> 目标：~25,000–30,000 行

---

## 已删除（当前 git diff 中，供参考）

| 文件/目录 | 估算行数 | 删除原因 |
|-----------|----------|----------|
| `internal/evolution/**` (60+ 文件) | ~9,700 | 遗传算法技能优化器，过度设计，未达预期效果 |
| `internal/cogmetrics/**` (9 文件) | ~1,100 | 认知指标收集，未被实际使用 |
| `cmd/ironclaw/insights.go` | 244 | 演化洞察 CLI 入口 |
| `internal/gateway/init_plan.go` | 298 | 旧计划模式初始化逻辑 |
| `internal/gateway/skill_draft_proposer.go` | 215 | 演化技能提词器 |
| `internal/gateway/subsystem_evolution.go` | 38 | 演化子系统桥接 |
| `internal/agent/evolution_bridge.go` | 125 | 演化-代理桥接层 |

---

## P1 — 死代码：零外部引用，可直接安全删除

这些文件/包在整个代码库中没有任何 `import` 引用（自身文件和 `_test.go` 除外）。删除零风险。

### 1. `internal/guardian/` — 质量漂移检测系统

```
文件：guardian.go (12,103 行) + guardian_test.go (11,874 行)
总计：~24,000 行
```

**是什么：** 完整的 Agent 质量监控系统——滑动窗口统计、漂移检测、告警通道、基线对比。
**为什么死：** 从未在 Gateway 或 Agent 中接入。是独立设计的子系统，始终未连线。
**引用情况：**

```
grep -rn "guardian" --include='*.go' | grep -v "internal/guardian/"
# 输出：空
```

- [ ] **决策：**
  - [ ] 直接删除（推荐）
  - [ ] 保留并接入（需评估接入成本和价值）
  - [ ] 保留不动

---

### 2. `internal/tool/semantic_diff.go` — XML 语义差异引擎

```
文件：semantic_diff.go
行数：2,560
```

**是什么：** 解析 LLM 输出的 XML 变更块（`<change>`, `<file>`, `<add>`, `<remove>` 等），生成结构化 unified diff。
**为什么死：** 现有的文件编辑工具（`file_edit.go`, `file_patch.go`, `file_write.go`）完全不使用它。
**引用情况：**

```
grep -rn "semantic_diff\|SemanticDiff" --include='*.go'
# 输出：仅自身文件
```

- [ ] **决策：**
  - [ ] 直接删除（推荐——现用 edit/patch/write 已覆盖）
  - [ ] 替换现有 file_edit 为 semantic_diff（需大量重构）
  - [ ] 保留不动

---

### 3. `internal/agent/heartbeat.go` — Agent 心跳调度器

```
文件：heartbeat.go
行数：124
```

**是什么：** 定时触发心跳 tick 的调度器（`HeartbeatScheduler`），通过 channel 发送 `HeartbeatTick`。
**为什么死：** taskledger 有自己的心跳机制（`Heartbeat(ctx, id)`），agent 包的心跳调度器从未被实例化或使用。
**引用情况：**

```
grep -rn "HeartbeatScheduler\|HeartbeatTick\|NewHeartbeatScheduler" --include='*.go' | grep -v heartbeat.go
# 输出：空
```

- [ ] **决策：**
  - [ ] 直接删除（推荐）
  - [ ] 接入到 agent 生命周期中（需澄清用途）
  - [ ] 保留不动

---

### 4. `internal/agent/incremental_json.go` — 流式 JSON 增量解析器

```
文件：incremental_json.go
行数：114
```

**是什么：** 增量解析不完整的流式 JSON 响应（处理 `{"key": "val` 这类截断）。
**为什么死：** 当前的 OpenAI/Claude provider 都用各自的方式处理流式响应（SSE 逐行），不使用增量解析。
**引用情况：**

```
grep -rn "incremental_json\|IncrementalJSON" --include='*.go' | grep -v incremental_json.go
# 输出：空
```

- [ ] **决策：**
  - [ ] 直接删除（推荐）
  - [ ] 保留不动

---

### 5. `internal/agent/backend_ipc.go` — IPC 子代理后端

```
文件：backend_ipc.go
行数：90
```

**是什么：** 通过进程间通信（IPC）执行子代理的后端实现。
**为什么死：** CLAUDE.md 明确标注"Local sub-agent backends are current; remote A2A execution is not"。当前使用 Docker/Subprocess 后端。
**引用情况：**

```
grep -rn "BackendIPC\|IPCBackend\|backend_ipc" --include='*.go' | grep -v backend_ipc.go
# 输出：空
```

- [ ] **决策：**
  - [ ] 直接删除（推荐）
  - [ ] 保留不动（如果近期计划实现 IPC 后端）

---

### 6. `internal/agent/context_builder.go` — 动态上下文聚合器

```
文件：context_builder.go
行数：52
```

**是什么：** `ContextBuilder` 聚合多个 `ContextScanner` 的动态上下文注入 prompt。
**为什么死：** 从未被实例化。没有任何 `ContextScanner` 实现注册到它。
**引用情况：**

```
grep -rn "ContextBuilder\|context_builder" --include='*.go'
# 输出：仅自身文件
```

- [ ] **决策：**
  - [ ] 直接删除（推荐）
  - [ ] 接入使用（需实现 ContextScanner）
  - [ ] 保留不动

---

## P2 — 死配置/死字段：定义存在但从未被读取

这些是 config 结构体中的字段——定义、默认值、YAML 解析都在，但运行时没有任何代码读取它们。

### 7. `config_tools.go` — WASM 配置块

```
涉及：config_tools.go 中的 WASMConfig 结构体 + ToolsConfig.WASM 字段
```

**是什么：** WASM 插件运行时配置（超时等）。
**为什么死：** WASM 插件运行时从未实现。整个代码库中没有任何 WASM 执行代码。
**引用情况：**

```
grep -rn "WASM\|wasm" --include='*.go' | grep -v config_tools.go | grep -v config.go
# 输出：空（仅 config 定义）
```

- [ ] **决策：**
  - [ ] 删除 WASM 配置（推荐——等实现时再加）
  - [ ] 保留不动（WASM 即将开发）

---

### 8. `config_infra.go` — A2A 服务器地址

```
涉及：config_infra.go ServerConfig.A2AServerAddr + config.go 默认值 ":9191"
```

**是什么：** A2A 协议服务器监听地址。
**为什么死：** CLAUDE.md："remote A2A execution is not"。spec.go："Phase 3: A2A remote agent support (reserved, not implemented)"。
**引用情况：**

```
grep -rn "A2AServerAddr\|a2a_server_addr" --include='*.go'
# 输出：仅 config 定义
```

- [ ] **决策：**
  - [ ] 删除 A2AServerAddr（推荐）
  - [ ] 保留不动

---

### 9. `internal/agent/spec.go` — RemoteAgentConfig

```
涉及：spec.go RemoteAgentConfig 结构体 + AgentSpec.Remote 字段
行数：~30
```

**是什么：** A2A 远程代理配置（URL, headers 等）。
**为什么死：** 同上，"Phase 3, reserved, not implemented"。
**引用情况：**

```
grep -rn "RemoteAgent\|RemoteAgentConfig" --include='*.go'
# 输出：仅 spec.go 自身定义
```

- [ ] **决策：**
  - [ ] 删除 RemoteAgentConfig（推荐）
  - [ ] 保留不动

---

### 10. `config_agent.go` — CognitiveConfig 中的 4 个死字段

```
涉及：CognitiveConfig.ConfidenceThreshold, .MaxReplanAttempts, .PlanMaxTokens, .ReflectMaxTokens
```

**是什么：** 这些字段在 `defaultConfig()` 中有默认值，在 YAML 中可配置，但运行时没有任何代码读取它们。
**实际被使用的字段：** `ApprovalTimeoutSeconds`（main.go/tui.go 读取）, `MaxParallelTools`（init_agent.go 读取）
**死字段验证：**

```
grep -rn "ConfidenceThreshold\|MaxReplanAttempts\|PlanMaxTokens\|ReflectMaxTokens" --include='*.go' | grep -v "_test.go" | grep -v "config"
# 输出：空（除了 config 自身定义，无运行时读取）
```

- [ ] **决策：**
  - [ ] 删除 4 个死字段，保留 `ApprovalTimeoutSeconds` 和 `MaxParallelTools`（推荐）
  - [ ] 全部保留（计划将来启用）
  - [ ] 保留不动

---

## P3 — 过度设计：功能存在但复杂度过高，可大幅简化

### 11. Memory 系统整体 ✅ 已完成 (2026-06-09, commit 7d2c04a)

```
包：internal/memory/ (25 文件, 3,497 行) ← 原 42 文件 / 8,693 行
    internal/memorywire/ — 已删除
    internal/tool/memory.go (223 行) ← 合并原 3 工具
```

**设计模式：Flat Store + Smart Retrieval**
不再对记忆做多层 LLM 加工（facts→L1 reflections→L2 reflections→profile）。
检索时由 BM25+向量+RRF 融合找相关内容，不需要预分类/预合并/预衰减。

**删除清单（~5,200 行）：**

| 已删除 | 行数 | 原因 |
|--------|------|------|
| profiler.go + test + profile_schema | ~1,200 | LLM总结LLM的LLM输出——检索已够好 |
| reflector.go + test (L1/L2 reflections) | ~922 | 事实→模式→洞察的元认知链条无增量价值 |
| forgetting_curve.go + test | ~555 | 艾宾浩斯衰减→TTL+访问频率替代 |
| memorywire.go + test + adapter.go | ~774 | 零外部消费者，所有op 1:1映射Store |
| compactor.go | ~241 | LLM合并同类→原始检索更精确 |
| memory_index.go (MEMORY.md) | ~226 | 与SQLite索引冗余 |
| markdown.go | ~219 | hash fn内联到file_store |
| consolidator.go | ~193 | scope promotion→简单SQL内联 |
| compressor.go + test | ~210 | 上下文窗口管理不属于memory |
| section_buffer.go + test | ~136 | 仅profiler使用的缓冲 |
| integration_test.go | ~132 | 引用已删除的Consolidator |

**保留核心（~3,500 行）：**

| 保留 | 行数 | 职责 |
|------|------|------|
| file_store.go | 724 | Markdown + SQLite + FTS5 + Vector + RRF |
| lifecycle.go | 275 | ADD/UPDATE/DELETE/NOOP 决策 |
| retriever.go | 254 | 统一搜索(memory+procedural并行) |
| facts.go | 120 | LLM事实提取 |
| procedural.go | 119 | 任务策略记录 |
| openai.go | 147 | OpenAI嵌入 |
| cache.go | 104 | 嵌入缓存 |
| privacy.go | 103 | PII检测 |
| + tests | ~800 | — |

**MemoryConfig 精简：16 字段 → 6 字段**
（删除 ConsolidationInterval, EnableVSS, EnableSearchCache, SearchCacheSize, SearchCacheTTL,
ReflectionCountThreshold, ReflectionDriftThreshold, ReflectionL2Trigger, CompactionInterval,
CompactionThreshold）

- [x] **已完成：** 激进方案 + 额外删除 forgetting_curve/memory_index/markdown/section_buffer
  - 削减：~9,400 行 → ~3,500 行（-63%），46 文件 → 25 文件（-46%）

---

### 12. 三重内存工具（暴露给 LLM 的内存写入接口） ✅ 已完成

```
之前：
- internal/tool/core_memory.go (119 行) — remember/forget/update
- internal/tool/memory_manage.go (252 行) — forget/list/protect/retention
- internal/tool/amp_memory.go (74 行)    — AMP 协议

之后：
- internal/tool/memory.go (223 行) — save/search/delete/list 统一工具
```

**问题：** LLM 可见三种不同的内存写入工具，功能高度重叠（三个都能 forget）。
**解决：** 合并为单一 `memory` 工具，四种操作：save/search/delete/list。LLM 不需要在三个接口间选择。

- [x] **已完成**

---

### 13. Memorywire/AMP 协议层 ✅ 已完成

```
文件：
- internal/memorywire/adapter.go — 已删除
- internal/memory/memorywire.go + memorywire_test.go — 已删除
- internal/tool/amp_memory.go — 已删除
```

**是什么：** AMP (Agent Memory Protocol) 适配器。所有操作（remember/recall/forget/merge/expire）与 Store 接口 1:1 映射。
**问题：** 零外部消费者，纯抽象层无抽象价值。
**解决：** 随 #11/#12 一起整包删除。

- [x] **已完成**

---

### 14. `internal/agent/spec.go` — 执行模式 + Phase 注释

```
行数：163
```

**涉及内容：**
- `ExecutionMode` 类型：spawn / fork / background（三种都在用）
- `Remote *RemoteAgentConfig`：（见 P2#9，死字段）
- Phase 1/2/3 注释：描述"当前"vs"未来"状态，部分已过时
- `InheritContext`：仅 fork 模式使用

- [ ] **决策：**
  - [ ] 删除 RemoteAgentConfig（P2#9）+ 清理过时 Phase 注释
  - [ ] 重命名 ExecModeFork → 更清晰的命名
  - [ ] 不动

---

### 15. Feature Registry 复杂度

```
文件：internal/feature/registry.go (331 行) + registry_test.go (316 行)
```

**当前 10 个 feature：** memory, skills, multi_agent, team, speculative, scheduler, sandbox, server, worktree, mcp_*

**复杂度来源：**
- 拓扑排序（Kahn 算法）— 当前依赖关系极简单（仅 team→multi_agent）
- 运行时 Enable/Disable 含锁释放/重检查逻辑
- 持久化 feature state 到 `~/.ironclaw/feature_state.json`
- 热重载回调绑定
- AutoDetect + Override + Default 三层优先级

**简化空间：** 10 个布尔值不需要拓扑排序。简单的 `map[string]bool` + `for range` 初始化即可。

- [ ] **决策选项：**
  - [ ] 简化为 map-based 开关（~50 行替代 331 行）
  - [ ] 保留拓扑排序（如果未来会加复杂依赖）
  - [ ] 不动

---

### 16. PlanTaskTool + DAG 执行器

```
文件：
- internal/tool/plan_task.go (463 行)
- internal/tool/plan_task_test.go (238 行)
- internal/dag/executor.go (178 行)
- internal/dag/executor_test.go (177 行)
```

**是什么：** 将复杂任务分解为有依赖关系的 DAG 子任务图，然后按拓扑顺序执行。
**使用方：** plan_task 注册为工具（暴露给 LLM），由 tool_bridge.go 桥接执行。
**问题：** `/plan` 命令使用率如何？如果不常用，整个 DAG 包 + plan_task 工具可删除。

- [ ] **决策选项：**
  - [ ] 删除 DAG 包 + plan_task 工具（~1,000 行）
  - [ ] 保留 plan_task 但内联 DAG 逻辑
  - [ ] 不动

---

## P4 — 低影响：可选优化，非紧急

### 17. Eval 包

```
目录：internal/eval/ (10 文件, ~1,500 行 + testdata)
```

**是什么：** Agent 评估框架——Task 定义、Scorer 接口、Runner、Report。
**引用情况：** 完全自包含，主代码库零引用。由 `cmd/ironclaw` 的 eval 子命令入口使用。
**问题：** 它是测试工具链，不是运行时。放在 `internal/` 中暗示不可能被外部引用——实际上也确实没有。

- [ ] **决策选项：**
  - [ ] 移入 `cmd/ironclaw/eval/`（更准确反映其 CLI 工具身份）
  - [ ] 保留在 internal/
  - [ ] 不动

---

### 18. Channel 实现

```
目录：
- internal/channel/tui/ (16 文件, 2,710 行)
- internal/channel/discord/ (3 文件, 625 行)
- internal/channel/telegram/ (2 文件, 510 行)
```

**观察：**
- TUI 最大，功能最丰富（对话框、统计、建议、格式化、样式）
- Discord 和 Telegram 共享 `channel.Channel` 接口但代码几乎不共享
- `discord/formatter.go`(16 行) 和 `telegram/formatter.go`(43 行) 可以做更多共享

- [ ] **决策选项：**
  - [ ] 保持现状（三个频道实现相互独立是合理的）
  - [ ] 提取公共 formatter 逻辑到 `internal/channel/`
  - [ ] 评估 Discord/Telegram/TUI 三个频道的使用率和维护优先级

---

### 19. Sandbox 多后端

```
文件：
- internal/sandbox/docker_session.go (269 行)
- internal/sandbox/docker_probe.go (50 行)
- internal/sandbox/bubblewrap.go (300 行)
- internal/sandbox/seatbelt.go (200 行)
- internal/sandbox/network_policy.go (150 行)
- internal/sandbox/file_guard.go (150 行)
```

**是什么：** 三层沙箱后端——Docker（跨平台）、Seatbelt（macOS 原生）、Bubblewrap（Linux 原生）。
**实际使用：** `sandbox.go` 优先级是 darwin→Seatbelt, linux→Bubblewrap, fallback→Docker。大多数部署只用 Docker。
**问题：** 如果 IronClaw 主要部署在 Docker 环境，Seatbelt 和 Bubblewrap 基本上不会触发。

- [ ] **决策选项：**
  - [ ] 删除 Seatbelt 和 Bubblewrap，只保留 Docker（如部署环境统一）
  - [ ] 保留为可选后端（需要原生沙箱的用户场景）
  - [ ] 不动

---

### 20. 其他迷你优化

| 项 | 文件 | 说明 |
|----|------|------|
| 20a | `internal/agent/model_context.go` (48行) | 仅一个函数 `ModelContextWindow()`——可移到 `openai.go` 或 `deps.go`。 |
| 20b | `internal/agent/noop.go` (64行) | Noop 实现用于测试。保留有测试价值。 |
| 20c | `internal/agent/emitter.go` (91行) | MultiEmitter 合并逻辑——仅 TUI 使用，可移到 TUI 包。 |
| 20d | `internal/observability/` (789行) | OpenTelemetry 埋点——如果是本地工具，这个开销可能不匹配。评估使用率。 |
| 20e | `internal/config/watcher.go` | 配置热重载——增加了 gateway 的 OnReload 回调复杂度。评估是否必需。 |
| 20f | `internal/logging/redact.go` (131行) | PII 日志脱敏——仅 mcp 包使用。可考虑内联。 |

---

## 📊 汇总

| 优先级 | 描述 | 状态 | 已削减行数 |
|--------|------|------|-----------|
| P1 | 死代码（6 项） | ⬜ 未开始 | ~27,000 |
| P2 | 死配置（4 项） | ⬜ 未开始 | ~80 |
| P3#11 | Memory 系统精简 | ✅ 已完成 | ~5,200 |
| P3#12 | 三重内存工具合并 | ✅ 已完成 | ~220 |
| P3#13 | Memorywire/AMP 删除 | ✅ 已完成 | ~550 |
| P3#14 | Spec 清理 | ⬜ 未开始 | ~30 |
| P3#15 | Feature Registry 简化 | ⬜ 未开始 | ~280 |
| P3#16 | PlanTask/DAG 评估 | ⬜ 未开始 | ~1,000 |
| P4 | 低影响优化（4+ 项） | ⬜ 未开始 | ~1,000–2,000 |
| **已删除** | evolution/cogmetrics/etc | ✅ | ~11,500 |
| **已削减** | P3#11–13 本次提交 (7d2c04a) | ✅ | **~6,083** |
| **累计已削减** | | | **~17,583** |
| **剩余可削减** | P1+P2+P3#14–16+P4 | | **~29,000–30,000** |

**当前代码库：** ~44,000 行 → 已削减 ~17,600 → **当前 ~26,400 行**（已达目标）

---

## 建议执行顺序

1. **P1 全部删除。** 安全、快速、高回报。一次性 commit。
2. **P2 清理配置。** 紧接着 P1，改动量小。
3. **P3 逐个评估。** 建议顺序：
   - ✅ ~~P3#11 Memory 系统~~ — 已完成 (2026-06-09, 7d2c04a)
   - ✅ ~~P3#12 三重内存工具~~ — 已完成（随 #11）
   - ✅ ~~P3#13 Memorywire/AMP~~ — 已完成（随 #11）
   - P3#16 PlanTaskTool/DAG（独立子系统，易评估）
   - P3#15 Feature Registry（改动小但影响 gateway 初始化）
   - P3#14 Spec 清理（顺手的事）
4. **P4 按需执行。** 开发节奏允许时处理。
4. **P4 按需执行。** 开发节奏允许时处理。
