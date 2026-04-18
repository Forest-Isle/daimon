# IronClaw

**本地优先的 AI Agent 运行时，Go 语言实现。**

[English](README.md)

[![CI](https://github.com/Forest-Isle/IronClaw/actions/workflows/ci.yml/badge.svg)](https://github.com/Forest-Isle/IronClaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-本地优先-003B57?logo=sqlite&logoColor=white)](https://www.sqlite.org)
[![Anthropic](https://img.shields.io/badge/Claude-AI_驱动-D97757?logo=anthropic&logoColor=white)](https://www.anthropic.com)

IronClaw 是一个自托管的 AI Agent 运行时，完全运行在你自己的基础设施上。它将 Claude AI 与实际工具（Shell 命令、文件操作、HTTP 请求、浏览器自动化）连接起来，并通过多种渠道（Telegram、终端 UI）进行交互。所有数据本地存储在 SQLite 和 Markdown 文件中。

## 功能特性

- **双模式 Agent** — 简单线性循环或认知五阶段循环（PERCEIVE → PLAN → ACT → OBSERVE → REFLECT），支持自动重规划和置信度追踪
- **高级记忆系统** — 基于 Markdown 文件的存储，融合认知科学的记忆类型分类（情景/语义/程序），重要性评分，遗忘曲线整合，自动反思（L1 模式识别 + L2 战略洞察），分层压缩（事实 → 摘要 → 用户画像），分层检索
- **知识库** — 文档摄取管线（Markdown、代码、PDF、文本、网页），BM25+向量混合检索，RRF 融合，可选 LLM 重排序
- **时序知识图谱** — 实体/关系提取，时间感知的边版本化，递归 CTE 多跳遍历，记忆-图谱双向同步，溯源追踪，自动图谱衰减
- **隐私控制** — PII 检测（邮箱、电话、身份证号、银行卡号），敏感度分级（public/private/secret），用户记忆管理工具，可配置保留策略，审计日志
- **MCP 协议** — 多 MCP 服务器连接，热重载，自动工具发现与注册
- **技能系统** — 可扩展的 SKILL.md 格式，内置 ClawHub 公共注册中心，支持搜索、安装和管理技能
- **多渠道** — Telegram Bot（流式消息，内联键盘审批）和 TUI 终端界面（Bubble Tea + Glamour Markdown 渲染）
- **HTTP 指标** — 可选的 `/metrics` 端点，暴露 Prometheus 风格的计数器：活跃会话数、工具调用总次数、LLM Token 用量和 Agent 迭代次数；通过 `http.metrics_enabled` 配置项启用（默认关闭）
- **强化学习** — 三层 RL 系统：上下文赌博机（工具选择）、PPO（规划策略）、DQN（重规划决策），含完整神经网络训练
- **工具系统** — 内置 Bash、文件读写、HTTP、浏览器自动化、技能执行、记忆管理等工具，以及基于 MCP 的动态工具发现
- **人格与用户目录** — 自动初始化 `~/.IronClaw/`，包含人格文件（Soul.md、Memory.md、Agent.md）和用户级配置
- **本地存储** — SQLite WAL 模式，18 个内嵌迁移，FTS5 全文搜索（优雅降级为 LIKE 查询）
- **定时任务** — 基于 Cron 的任务调度，数据库持久化
- **工具审批** — 可配置的逐工具审批机制，支持 Telegram 内联键盘或 TUI 交互式对话框
- **结构化验证** — 按工具类型自动生成断言（bash 退出码、HTTP 状态码、文件操作），失败上下文带类型化标注注入 REFLECT 阶段，驱动精准重规划
- **任务检查点** — 中断的认知任务自动保存状态到 SQLite；`/resume` 斜杠命令从上一次完成的子任务恢复执行
- **智能重试** — 失败上下文（错误类型、尝试次数、逐断言详情）注入 REFLECT 提示词；多次同类失败后触发降级警告
- **浏览器工具** — `browser_search`（结构化搜索结果）和 `browser_extract`（HTML 转 Markdown，支持分页）
- **工具结果缓存** — 基于 SHA256 的按任务内存缓存，自动命中只读工具结果，写操作触发路径级失效
- **项目与 Git 上下文** — 自动检测项目类型（Go/Node/Rust/Python）、构建命令、README 等，以及 Git 状态（分支、未提交文件、最近提交），注入 PLAN 提示词
- **动态上下文预算** — 按任务复杂度动态分配记忆、知识库、图谱、项目/Git 上下文的 Token 配额，避免简单任务浪费上下文窗口

## 架构概览

```
┌─────────────┐  ┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Telegram    │  │  TUI        │────▶│   Gateway    │────▶│   Agent         │
│  Channel     │  │  Channel    │◀────│   (Router)   │◀────│ Simple/Cognitive│
└─────────────┘  └─────────────┘     └──────┬───────┘     └──────┬──────────┘
                                            │                     │
                                     ┌──────┴──────┐        ┌─────┴──────┐
                                     │  HTTP API   │        │   Tools    │
                                     │  (可选)      │        │ bash/file/ │
                                     └─────────────┘        │ http/mcp   │
                                                            └─────┬──────┘
                                                                  │
┌─────────────┐  ┌─────────────┐  ┌───────────────────────────────┴───────┐
│  Scheduler  │  │   Skills    │  │            Store (SQLite)              │
│  (cron)     │  │  (ClawHub)  │  ├──────────────┬────────────────────────┤
└─────────────┘  └─────────────┘  │   Memory     │    知识库              │
                                  │  文件优先     │   (BM25 + 向量)       │
┌─────────────┐  ╔═════════════╗  │  MD + SQLite ├────────────────────────┤
│  User Dir   │  ║  RL Engine  ║  │  索引        │    知识图谱            │
│(~/.IronClaw)│  ║ Bandit/PPO/ ║──╫──────────────┤   (时序边, 溯源)      │
└─────────────┘  ║    DQN      ║  │  反思器      ├────────────────────────┤
                 ╚═════════════╝  │  压缩器      │    隐私与审计          │
                                  │  画像器      │                        │
                                  └──────────────┴────────────────────────┘
```

> **RL Engine**（强化学习引擎）是 IronClaw 的核心决策层：上下文赌博机负责工具选择，PPO 负责规划策略，DQN 负责重规划决策。三个子系统协同优化 Agent 的长期行为。

## 快速开始

### 从源码构建

```bash
# 克隆仓库
git clone https://github.com/Forest-Isle/IronClaw.git
cd ironclaw

# 复制并编辑配置文件
cp configs/ironclaw.example.yaml configs/ironclaw.yaml
# 填入你的 ANTHROPIC_API_KEY 和 TELEGRAM_BOT_TOKEN
vim configs/ironclaw.yaml

# 构建（需要 CGO 支持 SQLite）
make build

# 以 Telegram 渠道运行
./bin/ironclaw start

# 或以终端 UI 运行
./bin/ironclaw tui
```

### Docker

```bash
# 复制并编辑配置文件
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# 使用 Docker Compose 启动
docker compose up -d
```

### 预编译二进制

从 [Releases](https://github.com/Forest-Isle/IronClaw/releases) 页面下载。

```bash
# 下载（以 Linux amd64 为例）
curl -LO https://github.com/Forest-Isle/IronClaw/releases/latest/download/ironclaw_linux_amd64.tar.gz
tar xzf ironclaw_linux_amd64.tar.gz

# 复制并编辑配置文件
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# 运行
./ironclaw start
```

## 配置说明

IronClaw 使用 YAML 配置文件，支持环境变量展开（`${VAR_NAME}`）。完整配置项请参考 [`configs/ironclaw.example.yaml`](configs/ironclaw.example.yaml)。

| 配置段 | 说明 |
|--------|------|
| `llm` | AI 提供商配置（API Key、模型、最大 Token 数） |
| `telegram` | Bot Token 和允许的用户 ID |
| `tui` | 终端 UI 设置（auto_approve 模式） |
| `agent` | 模式（simple/cognitive）、最大迭代次数、RL 配置 |
| `store` | SQLite 数据库路径 |
| `memory` | 存储目录、事实提取、相似度阈值、反思/压缩阈值、保留策略 |
| `knowledge` | 文档摄取目录、分块大小、混合检索、重排序、知识图谱 |
| `skills` | 启用/禁用、额外技能目录 |
| `scheduler` | Cron 任务调度器 |
| `tools` | 各工具的启用/禁用、超时、审批设置、MCP 服务器 |
| `server` | 可选的 HTTP API 端点 |
| `http.metrics_enabled` | 启用 Prometheus 风格的 `/metrics` 端点（默认：`false`） |
| `log` | 日志级别和格式 |

## 记忆系统

IronClaw 使用**文件优先的记忆架构**，融合认知科学理论，包含五层记忆处理管线：

```
Layer 0: 工作上下文（当前对话）
    ↓ 事实提取
Layer 1: 会话事实（情景/语义/程序，含重要性与情感标注）
    ↓ 聚合提升（24h，强度 ≥ 0.5）
Layer 2: 用户事实（从会话提升）
    ↓ 压缩（同类别 ≥ 8 条事实）
Layer 3: 摘要（LLM 合并的结构化摘要）
    ↓ 反思（L1 模式识别 → L2 战略洞察）
Layer 4: 用户画像（身份、偏好、当前关注点）
```

### 记忆类型

| 类型 | 衰减速率 | 说明 |
|------|---------|------|
| **情景记忆 (episodic)** | 快（12h × 重要性） | 有时间线的具体事件和经历 |
| **语义记忆 (semantic)** | 标准（24h × 重要性） | 通用知识、偏好、稳定事实 |
| **程序记忆 (procedural)** | 慢（48h × 重要性） | 行为模式、工作流程——越用越强 |

### 存储结构

```
~/.ironclaw/memory/
├── MEMORY.md              # 所有活跃记忆的索引
├── user/                  # 长期记忆 + 摘要 + 画像
├── session/               # 会话级临时记忆
├── feedback/              # 用户修正
├── global/                # 跨用户系统记忆
└── archived/              # 自动归档的低强度记忆
```

每个记忆文件使用 YAML frontmatter：

```markdown
---
id: abc123
scope: user
type: semantic
importance: 7
emotion: neutral
sensitivity: public
strength: 0.85
created_at: 2026-03-28T10:00:00Z
---

用户偏好简洁的回答，不需要冗长的解释。
```

### 核心机制

- **混合搜索**：BM25 (FTS5) + 向量（余弦相似度）+ RRF 融合 + 强度加权
- **遗忘曲线**：基于 Ebbinghaus 的衰减 `R(t) = e^(-t/S)`，含类型相关的稳定性和访问加成
- **生命周期管理**：LLM 驱动的 ADD/UPDATE/DELETE/NOOP 决策，含冲突检测（mem0 风格）
- **反思机制**：混合触发（计数 ≥ 10 或主题漂移余弦 < 0.7），生成多层级洞察
- **隐私保护**：自动 PII 检测，敏感度分级，用户侧 `memory_manage` 工具支持选择性遗忘
- **图谱同步**：记忆生命周期事件自动同步到知识图谱（实体提取、溯源、边权弱化）

### 从旧版存储迁移

```bash
ironclaw memory migrate            # 从 SQLite 迁移到文件存储
ironclaw memory migrate --dry-run  # 仅预览
ironclaw memory restore            # 从备份恢复
```

## 知识图谱

时序知识图谱追踪实体关系并保留版本历史：

- **时序边**：`valid_from`/`valid_to` 时间戳，支持时间点查询和关系版本化
- **记忆同步**：记忆 ADD → 实体提取；UPDATE → 溯源迁移；DELETE → 边权弱化（非删除）
- **图谱衰减**：后台任务清理孤立溯源，衰减无支撑的边权，移除失效边
- **多跳遍历**：带时序谓词的递归 CTE，支持当前状态和历史查询
- **图谱增强检索**：记忆搜索结果通过图谱连通性评分进行增强

## 性能基准

以下数据在 Apple M2 Pro 上测量，使用默认 SQLite 配置，单个活跃会话：

| 操作 | p50 | p99 |
|------|-----|-----|
| 工具调用分发（bash/file/http） | ~3ms | ~10ms |
| LLM 往返时延（Claude API，流式） | ~20ms | ~50ms |
| 记忆混合搜索（FTS5 + 向量，1 万条事实） | ~5ms | ~15ms |
| 知识库检索（BM25 + 向量，1000 个分块） | ~8ms | ~25ms |

以上数据反映的是从 Agent 发起工具调用到结果写回上下文的端到端延迟。LLM 往返时延的主要影响因素是到 Claude API 的网络延迟。

## 渠道

### Telegram

全功能 Telegram Bot，支持流式消息更新、内联键盘审批工具执行和重规划决策、用户级访问控制。

### 终端 UI (TUI)

基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 和 [Glamour](https://github.com/charmbracelet/glamour) 的交互式终端界面，支持丰富的 Markdown 渲染。

```bash
ironclaw tui                # 启动交互式终端 UI
ironclaw tui --auto-approve # 自动审批所有工具调用
```

## HTTP 指标

IronClaw 通过可选的 HTTP 网关暴露 Prometheus 兼容的 `/metrics` 端点。在配置文件中启用：

```yaml
http:
  metrics_enabled: true
```

端点（`GET /metrics`）以 Prometheus 文本格式返回以下计数器：

| 指标名 | 说明 |
|--------|------|
| `ironclaw_active_sessions` | 当前活跃会话数 |
| `ironclaw_tool_calls_total` | 工具调用累计次数 |
| `ironclaw_llm_tokens_total` | LLM Token 累计用量 |
| `ironclaw_agent_iterations_total` | Agent 迭代累计次数 |

处理器实现位于 `internal/gateway/metrics.go`。该端点默认关闭（`http.metrics_enabled: false`）。

## 技能管理

IronClaw 支持通过 SKILL.md 文件扩展技能，并集成 [ClawHub](https://clawhub.ai) 公共注册中心。

```bash
ironclaw skill list              # 列出已安装的技能（包括内置）
ironclaw skill search "web"      # 搜索 ClawHub
ironclaw skill install <slug>    # 安装技能
ironclaw skill update            # 更新所有技能
ironclaw skill remove <name>     # 移除技能
```

## 用户目录

首次运行时，IronClaw 会自动初始化 `~/.IronClaw/`：

- `Soul.md` — Agent 人格与沟通风格
- `Memory.md` — 持久化规则与偏好
- `Agent.md` — 核心系统提示词模板
- `config.yaml` — 用户覆盖配置
- `skills/` — 用户安装的技能
- `mcp/` — MCP 服务器配置（YAML，支持热重载）
- `memory/` — 长期记忆（Markdown + SQLite 索引）

## 开发指南

```bash
make build          # 构建二进制（CGO_ENABLED=1, -tags fts5）
make test           # 运行所有测试
make lint           # 运行 golangci-lint
make fmt            # 格式化代码（goimports + go fmt）
make docker         # 构建 Docker 镜像
make help           # 查看所有目标
```

单个测试：

```bash
CGO_ENABLED=1 go test -tags "fts5" -run TestName ./internal/package/ -v
```

> **注意**：所有构建/测试命令都需要 `CGO_ENABLED=1` 和 `-tags fts5` —— SQLite 使用 cgo，FTS5 需要在编译时启用。

## 故障排查

### SQLite "database is locked" 错误

IronClaw 以 WAL 模式打开 SQLite，支持并发读取。如果出现锁定错误，请确保同一 `data/ironclaw.db` 文件上只有一个 `ironclaw` 进程在运行。Docker 实例和裸机实例不能共享同一数据库路径。

### FTS5 不可用

如果全文搜索静默降级为 LIKE 查询，说明你的 SQLite 编译时未包含 FTS5。请使用 `CGO_ENABLED=1 go build -tags fts5` 重新编译，或从 Releases 页面安装预编译二进制。

### Telegram Bot 无响应

1. 确认 `TELEGRAM_BOT_TOKEN` 已在 `configs/ironclaw.yaml` 或环境变量中正确设置。
2. 检查 `telegram.allowed_user_ids` 配置中是否包含你的用户 ID。
3. 运行 `ironclaw start --log-level debug` 查看原始 webhook 事件。

### LLM 调用返回 401 / 鉴权失败

确保 `ANTHROPIC_API_KEY` 已在 shell 中导出或通过配置文件设置，且该 Key 有权访问 `llm.model` 指定的模型。

## 路线图

- [ ] 多 LLM 提供商支持（OpenAI、本地模型）
- [ ] Web UI 管理面板
- [ ] Discord / Slack 渠道适配器
- [ ] 多 Agent 协作
- [ ] Webhook 触发器
- [x] ~~自定义工具插件系统~~（技能系统 + MCP）
- [x] ~~RAG 文档摄取~~（知识库 + 知识图谱）
- [x] ~~终端 UI~~（Bubble Tea TUI 渠道）
- [x] ~~高级记忆~~（类型分类、反思、压缩、隐私）

## 贡献

欢迎贡献！请在提交 PR 前阅读[贡献指南](CONTRIBUTING_zh.md)。

## 许可证

[MIT](LICENSE)
