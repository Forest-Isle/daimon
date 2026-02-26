# IronClaw

**本地优先的 AI Agent 运行时，Go 语言实现。**

[English](README.md)

[![CI](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/punkopunko/ironclaw/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/punkopunko/ironclaw)](go.mod)
[![Release](https://img.shields.io/github/v/release/punkopunko/ironclaw)](https://github.com/punkopunko/ironclaw/releases)

IronClaw 是一个自托管的 AI Agent 运行时，完全运行在你自己的基础设施上。它将 Claude AI 与实际工具（Shell 命令、文件操作、HTTP 请求）连接起来，并通过 Telegram 等渠道进行交互。所有数据本地存储在 SQLite 中。

## 功能特性

- **Claude AI Agent** — 基于 Anthropic Claude，支持多轮对话和上下文压缩
- **Telegram Bot 渠道** — 通过 Telegram 与 Agent 对话，支持用户级访问控制
- **工具系统** — 内置 Bash 执行、文件读写、HTTP 请求、浏览器自动化等工具
- **本地存储** — 基于 SQLite 的持久化存储，支持向量记忆搜索实现长期记忆
- **定时任务** — 基于 Cron 的任务调度，支持自动化工作流
- **工具审批** — 可配置的敏感工具执行审批机制
- **HTTP 网关** — 可选的 REST API，支持程序化访问
- **会话管理** — 按用户隔离的对话会话与历史记录

## 架构概览

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│  Telegram    │────▶│   Gateway    │────▶│   Agent     │
│  Channel     │◀────│   (Router)   │◀────│   Runtime   │
└─────────────┘     └──────────────┘     └──────┬──────┘
                           │                     │
                    ┌──────┴──────┐        ┌─────┴──────┐
                    │   HTTP API  │        │   Tools    │
                    │  (可选)      │        │ bash/file/ │
                    └─────────────┘        │ http/browse│
                                           └─────┬──────┘
                                                  │
                    ┌─────────────┐        ┌──────┴──────┐
                    │  Scheduler  │        │   Store     │
                    │  (cron)     │        │  (SQLite)   │
                    └─────────────┘        └─────────────┘
                                           ┌─────────────┐
                                           │   Memory    │
                                           │ (embedding) │
                                           └─────────────┘
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
| `agent` | 最大迭代次数、系统提示词 |
| `store` | SQLite 数据库路径 |
| `memory` | 基于 Embedding 的记忆搜索 |
| `scheduler` | Cron 任务调度器 |
| `tools` | 各工具的启用/禁用、超时、审批设置 |
| `server` | 可选的 HTTP API 端点 |
| `log` | 日志级别和格式 |

配置值中可使用 `${VAR_NAME}` 语法引用环境变量。

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
- [ ] 自定义工具插件系统
- [ ] Discord / Slack 渠道适配器
- [ ] 多 Agent 协作
- [ ] RAG 文档摄取
- [ ] Webhook 触发器

## 贡献

欢迎贡献！请在提交 PR 前阅读[贡献指南](CONTRIBUTING_zh.md)。

## 许可证

[MIT](LICENSE)
