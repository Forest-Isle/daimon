# IronClaw 瘦身分析报告

> 目标：少即是多 — 提炼最核心价值，削减不必要功能，后期重新规划设计。
> 日期：2026-06-09

---

## 一、当前状态总览

| 指标 | 数量 |
|------|------|
| Go 源文件 | 321 |
| 生产代码行数 | ~49,000 |
| 测试代码行数 | ~25,000 |
| 内部包数量 | 25 |
| 二进制入口点 | 2（`ironclaw start`、`ironclaw tui`） |

### 各包代码量排名（生产代码）

| 排名 | 包 | 生产行数 | 测试行数 | 引用方数量 |
|------|-----|---------|---------|-----------|
| 1 | `agent` | 8,544 | 5,915 | 9 |
| 2 | `tool` | 5,022 | 3,771 | 24 |
| 3 | `channel` | 3,500 | 616 | 19 |
| 4 | `gateway` | 2,552 | 165 | 3 |
| 5 | `memory` | 2,244 | 1,255 | 11 |
| 6 | `config` | 1,190 | 515 | 18 |
| 7 | `hook` | 991 | 511 | 7 |
| 8 | `sandbox` | 925 | 451 | 5 |
| 9 | `taskledger` | 781 | 698 | 5 |
| 10 | `dag` | 534 | 388 | 1 |
| 11 | `worktree` | 555 | 169 | 2 |
| 12 | `mcp` | 527 | 667 | 3 |
| 13 | `session` | 455 | 52 | 19 |
| 14 | `observability` | 427 | 362 | 5 |
| 15 | `skill` | 417 | 114 | 5 |
| 16 | `userdir` | 360 | 255 | 5 |
| 17 | `eval` | 328 | 329 | **0（死代码）** |
| 18 | `ratelimit` | 247 | 453 | 2 |
| 19 | `health` | 178 | 278 | 2 |
| 20 | `store` | 168 | 504 | 13 |
| 21 | `scheduler` | 164 | 313 | 2 |
| 22 | `logging` | 131 | 121 | 2 |
| 23 | `errors` | 121 | 231 | 5 |
| 24 | `util` | 23 | 112 | 6 |

### 已完成的瘦身（最近5次提交，已删除23,570行）

| 提交 | 删除内容 |
|------|---------|
| `df9f110` | evolution、cogmetrics、web/studio、stale knowledge refs |
| `81f6e57` | web dashboard subsystem |
| `9fa7ab3` | Knowledge Base、Knowledge Graph subsystems |
| `68b6d97` | eval subsystem CLI commands |
| `78aef37` | P3#11-13 标记完成 |
| *(pending)* | Phase 1: 删除 eval + discord 死代码 (~1,300 行) |
| *(pending)* | Phase 2: 折叠薄封装 logging/health/ratelimit/dag + 删 plan_task (~2,600 行) |

---

## 二、逐包深度分析

### 2.1 `agent` — 8,544 行，最大怪兽

**结论：保留核心循环，砍掉过度基建和未使用的 backend。**

#### 文件分类明细

**核心循环（1,234 行）— 必须保留：**

| 文件 | 行数 | 说明 |
|------|------|------|
| `provider.go` | 97 | LLM Provider 接口定义 |
| `agent.go` | 388 | 主 Agent 结构体 |
| `unified_loop.go` | 60 | 统一循环入口 |
| `simple_loop.go` | 45 | 简化循环 |
| `loop_common.go` | 282 | 循环公共逻辑 |
| `loop_strategy.go` | 14 | 策略接口 |
| `deps.go` | 141 | 依赖注入结构 |
| `context.go` | 120 | 上下文类型 |
| `model_context.go` | 48 | 模型上下文 |
| `noop.go` | 39 | 空实现 |

**LLM Provider（1,511 行）— 保留：**

| 文件 | 行数 | 说明 |
|------|------|------|
| `openai.go` | 743 | OpenAI 兼容 provider |
| `claude_provider.go` | 501 | Claude provider |
| `retry.go` | 152 | 重试逻辑 |
| `tokenizer.go` | 115 | Token 计数 |

**Sub-Agent 系统（1,991 行）— 保留核心，砍编排器：**

| 文件 | 行数 | 判定 |
|------|------|------|
| `subagent.go` | 397 | **保留** — 子代理核心 |
| `subagent_result.go` | 218 | **保留** |
| `subagent_context.go` | 81 | **保留** |
| `agent_manager.go` | 258 | **保留** |
| `agent_tool.go` | 176 | **保留** — agent 工具注册 |
| `agent_mcp.go` | 158 | **保留** — per-agent MCP |
| `agent_hooks.go` | 152 | **保留** |
| `orchestrator.go` | 169 | **砍** — 并行调度，过度抽象 |
| `background.go` | 234 | **保留** — 后台 agent 管理 |
| `spec.go` | 148 | **保留** — agent 规格定义 |

**Context/Compression（833 行）— 保留：**

| 文件 | 行数 | 说明 |
|------|------|------|
| `compression.go` | 420 | 上下文压缩 |
| `context_manager.go` | 314 | Pipeline 上下文管理 |
| `token_budget.go` | 99 | Token 预算 |

**Plan/Team/Speculative（1,006 行）— 全部可砍，后期插件化：**

| 文件 | 行数 | 判定 |
|------|------|------|
| `plan_mode.go` | 369 | **砍** — plan→approve→execute 流程，增加复杂度但边际 UX 收益 |
| `speculative.go` | 141 | **砍** — streaming 期间预执行只读工具，过早优化 |
| `team_manager.go` | 179 | **砍** — 团队编排 |
| `team.go` | 101 | **砍** |
| `team_task.go` | 118 | **砍** |
| `team_message.go` | 98 | **砍** |

**Backends（593 行）— 只保留 in-process：**

| 文件 | 行数 | 判定 |
|------|------|------|
| `backend.go` | 137 | **精简** — 接口定义保留 |
| `backend_ipc.go` | 89 | **砍** — IPC 通信，subprocess/docker 需要 |
| `backend_subprocess.go` | 163 | **砍** — os/exec 子进程，无人使用 |
| `backend_docker.go` | 160 | **砍** — Docker 容器执行，无人使用 |
| `fork.go` | 44 | **砍** — fork 模式 |

> 注：`spec.go` 中注释标明 backend_subprocess/docker 是"future A2A remote-agent fields"。当前 A2A 远程执行未实现，这些 backend 是预留代码。

**基建管道（1,376 行）— 大部分可砍：**

| 文件 | 行数 | 判定 |
|------|------|------|
| `message_bus.go` | 190 | **砍** — 事件总线，过度基建 |
| `inproc_bus.go` | 62 | **砍** — 进程内总线实现 |
| `emitter.go` | 91 | **砍** — 薄封装 |
| `trace.go` | 99 | **砍** — 调用链追踪 |
| `events.go` | 100 | **保留** — 事件类型定义 |
| `circuit_breaker.go` | 100 | **砍** — 熔断器，早熟优化 |
| `cache_metrics.go` | 144 | **砍** — 缓存指标 |
| `codebase_index.go` | 278 | **砍** — 代码库索引，独立关注点 |
| `prompt_cache.go` | 130 | **保留** — prompt 缓存有实际价值 |
| `permission.go` | 136 | **保留** — 权限检查 |
| `tool_bridge.go` | 46 | **保留** — 工具桥接 |

**Agent 包瘦身目标：8,544 → ~4,000 行。砍 4,500+ 行。**

---

### 2.2 `tool` — 5,022 行

**结论：逐工具审计，砍掉不常用工具。**

| 文件 | 行数 | 判定 |
|------|------|------|
| `code_intel.go` | 559 | **保留** — 代码智能核心 |
| `test_run.go` | 463 | **保留** — 测试运行 |
| `file_patch.go` | 404 | **保留** — 文件补丁 |
| `interceptor_verify.go` | 286 | **保留** — 拦截器校验 |
| `browser_extract.go` | 262 | **砍** — 浏览器内容提取，极少使用 |
| `tool.go` | 258 | **保留** — 工具注册核心 |
| `browser_search.go` | 253 | **砍** — 浏览器搜索 |
| `trust_tracker.go` | 235 | **砍** — 信任追踪，过度设计 |
| `memory.go` | 223 | **保留** — 记忆工具 |
| `permissions.go` | 213 | **保留** — 权限管理 |
| `bash.go` | 212 | **保留** — Bash 执行 |
| `plan_task.go` | 170 | **砍** — 依赖 dag 包，单一消费者 |
| `interceptor_audit.go` | 141 | **保留** — 审计拦截器 |
| `http.go` | 129 | **保留** — HTTP 工具 |
| `interceptor_sandbox.go` | 126 | **保留** — 沙箱拦截器 |
| `file_edit.go` | 124 | **保留** |
| `resultstore.go` | 123 | **保留** — 结果存储 |
| `browser.go` | 122 | **砍** — 浏览器工具基类 |
| `file_read.go` | 116 | **保留** |
| `interceptor.go` | 99 | **保留** — 拦截器链 |
| `code_intel_search.go` | 99 | **保留** |
| `interceptor_permission.go` | 94 | **保留** |
| `file_write.go` | 92 | **保留** |
| `skill.go` | 87 | **保留** — 技能工具 |
| `file_list.go` | 70 | **保留** |
| `interceptor_hook.go` | 39 | **保留** |
| `policy.go` | 23 | **保留** |

**Tool 包瘦身目标：5,022 → ~3,800 行。砍 browser + trust_tracker + plan_task。**

---

### 2.3 `channel` — 3,500 行

**结论：砍 discord，保留 tui + telegram。**

| 子包 | 行数 | 引用方 | 判定 |
|------|------|--------|------|
| `channel/` 基类 | 1,634 | 19 | **保留** — 接口 + 路由 |
| `channel/tui` | 2,680 | 1（cmd/tui.go） | **保留** — Bubble Tea TUI，主要 UX |
| `channel/telegram` | 511 | 1（cmd/main.go） | **保留** — Telegram bot |
| `channel/discord` | 625 | **0** | **砍** — 死代码，无人引用 |

**Channel 包瘦身目标：3,500 → 2,875 行。**

---

### 2.4 死代码 — 可立即删除

| 包 | 生产行数 | 测试行数 | 引用方 | 说明 |
|----|---------|---------|--------|------|
| `internal/eval` | 328 | 329 | 0 | eval 子系统，git 记录已标"删除"但文件仍在磁盘 |
| `internal/channel/discord` | 625 | ~100 | 0 | Discord 适配器，无人使用 |

**小计：~1,050 生产行 + ~430 测试行可直接删除。**

---

### 2.5 薄封装 — 可折叠或删除

| 包 | 生产行数 | 测试行数 | 引用方 | 建议 |
|----|---------|---------|--------|------|
| `logging` | 131 | 121 | 2（仅 mcp） | 薄 slog 封装，内联到 mcp 或直接用 stdlib slog |
| `health` | 178 | 278 | 2（gateway） | 微型 HTTP `/health` 端点，并入 gateway |
| `ratelimit` | 247 | 453 | 2（gateway） | 令牌桶限流，测试臃肿，直接删 |
| `dag` | 534 | 388 | 1（tool/plan_task.go） | 完整 DAG 执行器仅一个消费者。砍 plan_task 后 dag 即成死代码 |

**小计：~1,090 生产行可消除，4 个包消失。**

---

## 三、分阶段执行计划

### Phase 1 — 立即删除（零风险，秒杀）

**目标：删除死代码，-1,700 行，消灭 2 个包。**

| 步骤 | 操作 | 预计节省 |
|------|------|---------|
| 1.1 | `git rm -r internal/eval/` | 328 生产 + 329 测试 |
| 1.2 | `git rm -r internal/channel/discord/` | 625 生产 + ~100 测试 |
| 1.3 | 清理 `cmd/` 和 `gateway/` 中残留的 eval/discord import | 少量 |
| 1.4 | 运行 `make build-bin && make vet` 验证 | — |

**验证标准：**
- `make build-bin` 编译通过
- `make vet` 无警告
- `grep -r "internal/eval\|channel/discord" internal/ cmd/ --include='*.go'` 无结果

---

### Phase 2 — 折叠薄封装（低风险）

**目标：消除 4 个包，-1,900 行。**

#### 2.1 删除 `internal/logging`

- `internal/mcp/adapter.go` 和 `manager.go` 改用 `log/slog` 标准库
- 删除 `internal/logging/` 目录
- 节省：131 生产 + 121 测试

#### 2.2 折叠 `internal/health`

- 将 `/health` HTTP 端点直接写入 `internal/gateway/http.go`
- 删除 `internal/health/` 目录
- 节省：178 生产 + 278 测试

#### 2.3 删除 `internal/ratelimit`

- 限流逻辑在 observability 子系统中未被有效使用
- 直接删除 `internal/ratelimit/`
- 清理 `gateway.go` 和 `subsystem_observability.go` 中的引用
- 节省：247 生产 + 453 测试

#### 2.4 删除 `internal/dag`

- 前提：砍掉 `internal/tool/plan_task.go`（唯一消费者）
- plan_task 功能可后期以更简单方式重新实现
- 同时删除 `internal/dag/`
- 节省：534 生产 + 388 测试 + 170（plan_task.go）

**验证标准：**
- `make build-bin && make vet` 通过
- `make test-short` 通过
- grep 确认无残留引用

---

### Phase 3 — Agent 瘦身（中风险，最高收益）

**目标：agent 包 8,544 → ~4,000 行，-4,500 行。**

#### 3.1 删除未使用的 backend（~460 行）

| 删除文件 | 行数 |
|---------|------|
| `backend_subprocess.go` | 163 |
| `backend_docker.go` | 160 |
| `backend_ipc.go` | 89 |
| `fork.go` | 44 |

保留 `backend.go`（接口定义），精简为仅 in-process 模式。

#### 3.2 删除 plan/team/speculative（~1,006 行）

| 删除文件 | 行数 |
|---------|------|
| `plan_mode.go` | 369 |
| `speculative.go` | 141 |
| `team_manager.go` | 179 |
| `team.go` | 101 |
| `team_task.go` | 118 |
| `team_message.go` | 98 |

清理 gateway 中的 team/plan/speculative 初始化代码：
- `init_multiagent.go` 中 team coordinator 相关
- `gateway.go` 中 team feature gate
- `features.go` 中 team/speculative feature 注册
- `commands.go` 中 `/team` 命令

#### 3.3 删除过度基建管道（~850 行）

| 删除文件 | 行数 |
|---------|------|
| `message_bus.go` | 190 |
| `inproc_bus.go` | 62 |
| `emitter.go` | 91 |
| `trace.go` | 99 |
| `circuit_breaker.go` | 100 |
| `cache_metrics.go` | 144 |
| `codebase_index.go` | 278 |

保留：`events.go`、`prompt_cache.go`、`permission.go`、`tool_bridge.go`

#### 3.4 删除 orchestrator（169 行）

agent 编排器过度抽象，可在 agent_manager 中直接管理。

#### 3.5 清理相关测试文件

删除上述文件对应的 `*_test.go`，约 2,000+ 测试行。

**验证标准：**
- `make build-bin && make vet` 通过
- `make test-short` 通过
- Gateway 启动后 TUI/Telegram 基础对话功能正常
- Sub-agent 调用功能正常
- 上下文压缩功能正常

---

### Phase 4 — Tool 审计（需要逐工具评估）

**目标：tool 包 5,022 → ~3,800 行，-1,200 行。**

| 步骤 | 操作 | 预计节省 |
|------|------|---------|
| 4.1 | 删除 `browser_extract.go`、`browser_search.go`、`browser.go` | 637 |
| 4.2 | 删除 `trust_tracker.go` | 235 |
| 4.3 | 删除 `plan_task.go`（已在 Phase 2.4 处理） | 170 |
| 4.4 | 清理对应测试文件 | ~500 |

**验证标准：**
- tool 注册表仍包含所有核心工具（bash、file_*、memory、code_intel、http、skill）
- `make test-short` 通过

---

### Phase 5 — 测试清理（收尾）

**目标：删除与已砍代码对应的孤立测试，-5,000+ 测试行。**

- 清理所有引用已删除包/文件的测试
- 清理 integration test 中被砍功能的测试用例
- 运行 `make test-short` 确认全绿

---

## 四、执行优先级矩阵

```
                    高风险
                      │
      Phase 4         │   Phase 3
      Tool 审计        │   Agent 瘦身
      (1,200行)       │   (4,500行)
                      │
  ────────────────────┼────────────────────
                      │
      Phase 1         │   Phase 2
      死代码删除       │   薄封装折叠
      (1,700行)       │   (1,900行)
                      │
                    低风险
```

**推荐顺序：Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5**

---

## 五、终态目标

| 指标 | 当前 | 目标 | 变化 |
|------|------|------|------|
| 生产代码行数 | ~49,000 | ~35,000 | **-28%** |
| 测试代码行数 | ~25,000 | ~15,000 | **-40%** |
| 内部包数量 | 25 | ~15 | **-40%** |
| Agent 包行数 | 8,544 | ~4,000 | **-53%** |
| Tool 包行数 | 5,022 | ~3,800 | **-24%** |

### 终态保留的核心包

| 包 | 说明 |
|----|------|
| `agent` | 精简后的 agent 核心循环 + provider + sub-agent |
| `tool` | 核心工具集（bash、file、memory、code_intel、http、skill） |
| `channel` | TUI + Telegram 适配器 |
| `gateway` | 组合根 |
| `memory` | 持久化记忆系统 |
| `config` | 配置加载 |
| `session` | 会话管理 |
| `store` | SQLite 数据层 |
| `sandbox` | Docker 沙箱 |
| `hook` | Hook 系统 |
| `mcp` | MCP 集成 |
| `skill` | Skill 加载 |
| `worktree` | Git worktree 隔离 |
| `scheduler` | 任务调度 |
| `userdir` | 用户目录管理 |
| `observability` | 可观测性（精简） |

### 砍掉的包

| 包 | 原因 |
|----|------|
| `eval` | 死代码 |
| `channel/discord` | 死代码 |
| `logging` | 薄封装，用 stdlib slog |
| `health` | 并入 gateway |
| `ratelimit` | 过度设计 |
| `dag` | 单一消费者，随 plan_task 删除 |

### 后期可插件化回归的功能

以下功能在当前阶段被砍，但架构上保留扩展点，后期需要时可以插件形式回归：

| 功能 | 涉及文件 | 回归优先级 |
|------|---------|-----------|
| Team 编排 | `team*.go`（5 文件） | 中 |
| Plan Mode | `plan_mode.go` | 中 |
| Speculative 执行 | `speculative.go` | 低 |
| Docker/Subprocess Backend | `backend_docker.go`、`backend_subprocess.go` | 低（等 A2A 实现） |
| 浏览器工具 | `browser*.go` | 低 |
| Trust Tracker | `trust_tracker.go` | 低 |
| Codebase Index | `codebase_index.go` | 中 |

---

## 六、风险与缓解

| 风险 | 等级 | 缓解措施 |
|------|------|---------|
| Agent 瘦身后核心循环异常 | 中 | Phase 3 每步后运行集成测试；保留 git history 可回滚 |
| Team/Plan 功能有隐藏依赖 | 低 | Phase 3.2 前全局 grep feature gate 引用 |
| Tool 审计误删常用工具 | 低 | Phase 4 前检查 tool registry 注册表和命令路由 |
| 测试大量失败 | 中 | 每 Phase 完成后立即 `make test-short`，修复后再推进 |

---

## 七、执行记录

| Phase | 日期 | 状态 | 实际节省行数 | 备注 |
|-------|------|------|-------------|------|
| Phase 1 | 2026-06-09 | ✅ 完成 | 1,282 行 | eval(657) + discord(625), 零 import 引用, build/vet/test 全绿 |
| Phase 2 | 2026-06-09 | ✅ 完成 | ~2,600 行 | logging→mcp, health→gateway, 删 ratelimit/dag/plan_task/tool_bridge, 消灭4包 |
| Phase 3 | — | 待执行 | — | — |
| Phase 4 | — | 待执行 | — | — |
| Phase 5 | — | 待执行 | — | — |
