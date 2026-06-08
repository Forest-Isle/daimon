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

### 11. Memory 系统整体

```
包：internal/memory/ (42 文件, 8,693 行) + internal/memorywire/ (1 文件, 300 行)
总计：~9,000 行
```

**组件清单：**

| 文件 | 行数 | 职责 | 必要性评估 |
|------|------|------|-----------|
| `file_store.go` | 940 | 文件系统存储（JSON 文件） | 核心，但 940 行偏大 |
| `profiler.go` | 649 | 用户画像生成（LLM 驱动） | 可简化 |
| `profiler_test.go` | 436 | — | — |
| `reflector.go` | 419 | 反思追踪（对话后分析） | 可简化 |
| `reflector_test.go` | 503 | — | — |
| `lifecycle.go` | 347 | 记忆生命周期（创建/更新/过期） | 核心 |
| `forgetting_curve.go` | 267 | 艾宾浩斯遗忘曲线 | 过度学术化 |
| `forgetting_curve_test.go` | 288 | — | — |
| `retriever.go` | 254 | 统一检索（语义+过程） | 核心 |
| `compactor.go` | 241 | 记忆压缩（摘要化旧记忆） | 可简化 |
| `memory_index.go` | 226 | 内存索引（非持久） | 可简化 |
| `memorywire.go` | 253 | AMP 协议适配（内部） | 见 #13 |
| `markdown.go` | 219 | Markdown 格式化输出 | 可简化 |
| `consolidator.go` | 193 | 会话→用户记忆提升 | 可简化 |
| `compressor.go` | 112 | 响应压缩 | 小 |
| `procedural.go` | 119 | 过程记忆（任务策略） | 核心 |
| `facts.go` | 120 | 事实提取 | 核心 |
| `cache.go` | 104 | 嵌入缓存 | 小 |
| `openai.go` | 147 | OpenAI 嵌入客户端 | 核心 |
| + `file_store_test.go`, `lifecycle_test.go` 等 | ~1,000 | — | — |

**问题：** memory 系统比 agent 系统还大（agent 包 ~7,000 行 vs memory ~9,000 行）。
**核心功能：** 存储、搜索、过期、事实提取。其余（画像、反思、压缩、合并、AMP、遗忘曲线）是附加值。

- [ ] **决策选项：**
  - [ ] **激进：** 保留 file_store + lifecycle + facts + retriever + embedding，删除 profiler/reflector/compactor/consolidator/compressor/markdown（砍 ~4,000 行）
  - [ ] **温和：** 保留大多数但标记哪些是"升级路径" vs "日常必需"
  - [ ] **保守：** 仅删除 dead code（forgetting_curve 如果没被实际调度运行）
  - [ ] 不动，逐个子组件分析

---

### 12. 三重内存工具（暴露给 LLM 的内存写入接口）

```
文件：
- internal/tool/core_memory.go (4,393 行) — 原生内存写入
- internal/tool/memory_manage.go (7,426 行) — 原生内存管理
- internal/tool/amp_memory.go (2,383 行) — AMP/Memorywire 协议
```

**问题：** LLM 可见三种不同的内存写入工具。`core_memory` 和 `memory_manage` 功能高度重叠。`amp_memory` 是并行协议。

- [ ] **决策选项：**
  - [ ] 合并 core_memory + memory_manage 为一个工具
  - [ ] 删除 amp_memory（AMP 协议用 memorywire 适配器内部处理即可）
  - [ ] 全部保留但统一接口
  - [ ] 不动

---

### 13. Memorywire/AMP 协议层

```
文件：
- internal/memorywire/adapter.go (300 行)
- internal/memory/memorywire.go (253 行)
```

**是什么：** AMP (Agent Memory Protocol) 适配器，标准化的 remember/recall/forget/merge/expire 操作。
**引用方：** gateway.go（实例化 adapter），tool/amp_memory.go（暴露给 LLM），subsystem_memory.go（存储 adapter 引用）。
**问题：** 如果删除 amp_memory 工具，memorywire 变成纯内部协议，553 行可大幅缩减或内联到 memory 包。

- [ ] **决策选项：**
  - [ ] 随 amp_memory 一起删除/内联
  - [ ] 保留为内部抽象层
  - [ ] 不动

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

| 优先级 | 描述 | 可削减行数 | 风险 |
|--------|------|-----------|------|
| P1 | 死代码（6 项） | ~27,000 | 零风险——零引用 |
| P2 | 死配置（4 项） | ~80 | 极低——仅 config 定义 |
| P3 | 过度设计（6 项） | ~5,000–8,000 | 中等——需逐个评估功能价值 |
| P4 | 低影响优化（4+ 项） | ~1,000–2,000 | 低——可选改进 |
| **已删除** | evolution/cogmetrics/etc | ~11,500 | — |
| **潜在总计** | | **~45,000–49,000** | — |

**当前代码库：** ~44,000 行 → **削减后目标：~25,000–30,000 行**

---

## 建议执行顺序

1. **P1 全部删除。** 安全、快速、高回报。一次性 commit。
2. **P2 清理配置。** 紧接着 P1，改动量小。
3. **P3 逐个评估。** 建议顺序：
   - P3#16 PlanTaskTool/DAG（独立子系统，易评估）
   - P3#15 Feature Registry（改动小但影响 gateway 初始化）
   - P3#13 Memorywire/AMP（取决于 P3#12 的决策）
   - P3#12 三重内存工具（影响 LLM 行为面）
   - P3#11 Memory 系统（最大项，需深入分析子组件依赖）
   - P3#14 Spec 清理（顺手的事）
4. **P4 按需执行。** 开发节奏允许时处理。
