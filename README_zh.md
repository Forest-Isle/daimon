# IronClaw

**本地优先的 AI Agent 运行时，Go 语言实现。**

[English](README.md)

[![CI](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/punkopunko/ironclaw)](go.mod)
[![Release](https://img.shields.io/github/v/release/punkopunko/ironclaw)](https://github.com/punkopunko/ironclaw/releases)

IronClaw 是一个自托管的 AI Agent 运行时，完全运行在你自己的基础设施上。它将 Claude AI 与实际工具（Shell 命令、文件操作、HTTP 请求）连接起来，并通过 Telegram 等渠道进行交互。所有数据本地存储在 SQLite 中。

## 功能特性

- **双模式 Agent** — 简单线性循环或认知五阶段循环（PERCEIVE → PLAN → ACT → OBSERVE → REFLECT），支持重规划
- **长期记忆** — mem0 风格的事实提取，三级作用域（session/user/global），FTS5+向量混合搜索，生命周期管理（ADD/UPDATE/DELETE），自动聚合提升
- **知识库** — 文档摄取管线（Markdown、代码、文本、网页），BM25+向量混合检索，可选 LLM 重排序
- **知识图谱** — 实体/关系三元组提取，递归 CTE 多跳遍历，溯源追踪
- **MCP 协议** — 多 MCP 服务器连接，热重载，自动工具发现与注册
- **技能系统** — 可扩展的 SKILL.md 格式，内置 ClawHub 公共注册中心，支持搜索、安装和管理技能
- **Telegram Bot 渠道** — 流式消息更新，内联键盘审批工具执行和重规划决策，用户级访问控制
- **工具系统** — 内置 Bash 执行、文件读写、HTTP 请求、浏览器自动化等工具
- **人格与用户目录** — 自动初始化 `~/.IronClaw/`，包含人格文件（Soul.md、Memory.md、Agent.md）和用户级 MCP 配置
- **本地存储** — SQLite WAL 模式，内嵌迁移，FTS5 全文搜索（优雅降级）
- **定时任务** — 基于 Cron 的任务调度，数据库持久化
- **工具审批** — 可配置的逐工具审批机制，Telegram 内联键盘交互
- **HTTP 网关** — 可选的 REST API，支持程序化访问
- **会话管理** — 按用户隔离的对话会话，支持历史压缩

## 架构概览

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Telegram    │────▶│   Gateway    │────▶│   Agent         │
│  Channel     │◀────│   (Router)   │◀────│ Simple/Cognitive│
└─────────────┘     └──────┬───────┘     └──────┬──────────┘
                           │                     │
                    ┌──────┴──────┐        ┌─────┴──────┐
                    │  HTTP API   │        │   Tools    │
                    │  (可选)      │        │ bash/file/ │
                    └─────────────┘        │ http/mcp   │
                                           └─────┬──────┘
                                                  │
┌─────────────┐  ┌─────────────┐  ┌───────────────┴───────┐
│  Scheduler  │  │   Skills    │  │       Store (SQLite)   │
│  (cron)     │  │  (ClawHub)  │  ├────────────┬───────────┤
└─────────────┘  └─────────────┘  │  Memory    │ Knowledge │
                                  │ (FTS5+vec) │ (BM25+vec)│
┌─────────────┐                   ├────────────┤───────────┤
│  User Dir   │                   │  Knowledge Graph       │
│ (~/.IronClaw)│                  │  (entity triples)      │
└─────────────┘                   └────────────────────────┘
```

## 快速开始

### 从源码构建

```bash
# 克隆仓库
git clone https://github.com/punkopunko/ironclaw.git
cd ironclaw

# 复制并编辑配置文件
cp configs/ironclaw.example.yaml configs/ironclaw.yaml
# 填入你的 ANTHROPIC_API_KEY 和 TELEGRAM_BOT_TOKEN
vim configs/ironclaw.yaml

# 构建（需要 CGO 支持 SQLite）
make build

# 运行
./bin/ironclaw start
```

### Docker

```bash
# 复制并编辑配置文件
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# 使用 Docker Compose 启动
docker compose up -d
```

### 预编译二进制

从 [Releases](https://github.com/punkopunko/ironclaw/releases) 页面下载。

```bash
# 下载（以 Linux amd64 为例）
curl -LO https://github.com/punkopunko/ironclaw/releases/latest/download/ironclaw_linux_amd64.tar.gz
tar xzf ironclaw_linux_amd64.tar.gz

# 复制并编辑配置文件
cp configs/ironclaw.example.yaml configs/ironclaw.yaml

# 运行
./ironclaw start
```

## 配置说明

IronClaw 使用 YAML 配置文件。完整配置项请参考 [`configs/ironclaw.example.yaml`](configs/ironclaw.example.yaml)。

主要配置项：

| 配置段 | 说明 |
|--------|------|
| `llm` | AI 提供商配置（API Key、模型、最大 Token 数） |
| `telegram` | Bot Token 和允许的用户 ID |
| `agent` | 模式（simple/cognitive）、最大迭代次数、系统提示词、人格 |
| `store` | SQLite 数据库路径 |
| `memory` | 事实提取、作用域、相似度阈值、聚合、BM25/向量权重 |
| `knowledge` | 文档摄取目录、分块大小、混合检索、重排序、知识图谱 |
| `skills` | 启用/禁用、额外技能目录 |
| `scheduler` | Cron 任务调度器 |
| `tools` | 各工具的启用/禁用、超时、审批设置、MCP 服务器 |
| `server` | 可选的 HTTP API 端点 |
| `log` | 日志级别和格式 |

配置值中可使用 `${VAR_NAME}` 语法引用环境变量。

## 技能管理

IronClaw 支持通过 SKILL.md 文件扩展技能，并集成 [ClawHub](https://clawhub.ai) 公共注册中心。

```bash
# 列出已安装的技能（包括内置）
ironclaw skill list

# 搜索技能
ironclaw skill search "web scraping"

# 安装技能
ironclaw skill install <slug>

# 更新所有技能
ironclaw skill update

# 移除技能
ironclaw skill remove <name>
```

技能存储在 `~/.IronClaw/skills/`。需要 `clawhub` CLI（`npm install -g clawhub`）。

## 用户目录

首次运行时，IronClaw 会自动初始化 `~/.IronClaw/`：

- `Soul.md` — Agent 人格与沟通风格
- `Memory.md` — 持久化规则与偏好
- `Agent.md` — 核心系统提示词模板
- `skills/` — 用户安装的技能
- `mcp/` — MCP 服务器配置（YAML，支持热重载）

## 开发指南

```bash
# 构建
make build

# 运行测试
make test

# 代码检查（需要 golangci-lint）
make lint

# 格式化代码
make fmt

# 构建 Docker 镜像
make docker

# 查看所有目标
make help
```

## 路线图

- [ ] 多 LLM 提供商支持（OpenAI、本地模型）
- [ ] Web UI 管理面板
- [ ] Discord / Slack 渠道适配器
- [ ] 多 Agent 协作
- [ ] Webhook 触发器
- [x] ~~自定义工具插件系统~~（技能系统 + MCP）
- [x] ~~RAG 文档摄取~~（知识库 + 知识图谱）

## 贡献

欢迎贡献！请在提交 PR 前阅读[贡献指南](CONTRIBUTING_zh.md)。

## 许可证

[MIT](LICENSE)
