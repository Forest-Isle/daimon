# Top-Tier Agent Upgrade — 2026-05-15

**日期**: 2026-05-15
**范围**: 全系统升级，对标 Claude Code / Devin / Manus / OpenAI Agents SDK / Google A2A 标准

## 改动规模

```
修改: 13 个文件  +444 / -51 行
新增: 11 个文件  2,515 行
总计: 24 个文件  ~3,000 行
```

## 七大能力升级

| # | 能力 | 对标 | 状态 |
|---|------|------|------|
| 1 | **Evolution Brain → Engine 集成** | Manus unified learning | ✅ 已接线 |
| 2 | **Worktree Code Isolation** | Claude Code / Codex workflow | ✅ Feature + 4 tools |
| 3 | **Plan → Approve → Execute** | Claude Code Plan Mode / Devin | ✅ 已接入 Executor |
| 4 | **Structured Output + Tool Choice** | OpenAI Agents SDK | ✅ Claude + OpenAI provider |
| 5 | **User-Extensible Hooks** | Claude Code hooks | ✅ 拦截器链集成 |
| 6 | **A2A Protocol** | Google A2A | ✅ Client + Server + Feature |
| 7 | **Streaming Tool Outputs** | Claude Code / Cursor | ✅ Context-driven + Channel 适配 |

## 详细文档索引

| 文档 | 说明 |
|------|------|
| [EVOLUTION_BRAIN_ENGINE_INTEGRATION.md](EVOLUTION_BRAIN_ENGINE_INTEGRATION.md) | Brain 从死代码到 Engine 核心协调器 |
| [WORKTREE_CODE_ISOLATION.md](WORKTREE_CODE_ISOLATION.md) | Git worktree 安全的代码修改工作流 |
| [PLAN_MODE_APPROVAL_WORKFLOW.md](PLAN_MODE_APPROVAL_WORKFLOW.md) | LLM 计划生成 + 用户审批 + 工具门控 |
| [STRUCTURED_OUTPUT_AND_TOOL_CHOICE.md](STRUCTURED_OUTPUT_AND_TOOL_CHOICE.md) | ToolChoice + ResponseFormat 双 provider 支持 |
| [USER_EXTENSIBLE_HOOK_SYSTEM.md](USER_EXTENSIBLE_HOOK_SYSTEM.md) | 用户脚本 hook：6 种事件 × 优先级排序 |
| [A2A_AGENT_PROTOCOL.md](A2A_AGENT_PROTOCOL.md) | Agent 间互操作：发现 + 任务 + 状态 |
| [STREAMING_TOOL_OUTPUTS.md](STREAMING_TOOL_OUTPUTS.md) | 实时 stdout 流式传输，消除"冻结"体验 |

## 能力矩阵（改造后）

```
                         IronClaw  Claude Code  Devin   Manus   OpenAI SDK
                         ────────  ───────────  ─────   ─────   ──────────
认知循环 + 自进化         ████████  ░░░░        ░░░░    ██████  ░░░░
Worktree 隔离            ████████  ████████    ██████  ░░░░    ░░░░
Plan Mode                ████████  ████████    ██████  ░░░░    ░░░░
结构化输出               ████████  ░░░░        ░░░░    ░░░░    ████████
User Hooks               ████████  ████████    ░░░░    ░░░░    ░░░░
A2A Protocol             ████████  ░░░░        ░░░░    ░░░░    ░░░░
Streaming Tool Outputs   ████████  ████████    ░░░░    ░░░░    ░░░░
MCP + Skills             ████████  ████████    ░░░░    ░░░░    ░░░░
多 Agent + RL            ████████  ░░░░        ██████  ██████  ░░░░
Eval Harness             ████████  ░░░░        ░░░░    ░░░░    ░░░░
```

## Core Files Modified

| 文件 | 改动摘要 |
|------|---------|
| `internal/evolution/engine.go` | +Brain, +SetBrain, +Brain(), DispatchEpisode/RunInsightsCycle 走 Brain |
| `internal/gateway/gateway.go` | +userHookMgr, +a2aServer, +planMode, +startA2AServer, +stopA2AServer, handleInbound stream wiring |
| `internal/gateway/init_cognitive.go` | PlanMode 创建注入 + Brain 接线 (`_ = brain` → `SetBrain`) |
| `internal/gateway/init_tools.go` | UserHookManager 初始化 + userHookInterceptor + 拦截器链集成 + Worktree tools 注册 |
| `internal/gateway/features.go` | +worktree feature (auto-detect git) + +a2a feature (hot-reloadable) + lifecycle hooks |
| `internal/agent/act.go` | +planMode 字段 + SetPlanMode 方法 |
| `internal/agent/cognitive.go` | +planMode 字段 + SetPlanMode 方法 |
| `internal/agent/provider.go` | +ToolChoice + ResponseFormat + JSONSchema 类型 |
| `internal/agent/stream.go` | Anthropic tool_choice + output_config 映射 |
| `internal/agent/openai.go` | OpenAI tool_choice + response_format 映射 |
| `internal/channel/channel.go` | +ToolStreamWriter 接口 |
| `internal/tool/tool.go` | +StreamCallback 类型 + WithStreamCallback + StreamCallbackFromContext |
| `internal/tool/bash.go` | 条件 pipe 流式传输 stdout |

## New Packages

| 包 | 文件数 | 说明 |
|----|--------|------|
| `internal/worktree/` | 3 | Git worktree 管理 + agent 工具 + 测试 |
| `internal/agent/plan_mode.go` | 2 | Plan Mode 核心 + 测试 |
| `internal/hook/user_hooks.go` | 2 | User hook 系统 + 测试 |
| `internal/a2a/` | 4 | A2A 协议/客户端/服务端 + 测试 |

## 待完成

- [ ] Plan Mode 的 `agent.plan_mode.enabled` / `agent.plan_mode.auto_approve` 配置项
- [ ] SandboxTestGate 从占位符改为真正在沙箱中执行 skill 测试
- [ ] A2A Server 端口可配置 (`a2a.port`)
- [ ] Discord channel 实现 `ToolStreamWriter`
- [ ] Web Dashboard 通过 WebSocket 支持 `ToolStreamWriter`
- [ ] Worktree 路径注册到 FileGuard allowedDirs（叠加 sandbox 安全）
