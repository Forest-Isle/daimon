# 01 - 入口与 CLI 系统

## 文件结构

```
cmd/ironclaw/
├── main.go     # 入口点 + start/version/skill 命令
├── tui.go      # TUI 终端交互命令
└── memory.go   # memory 迁移/恢复命令
```

## main.go — 程序入口

### 职责
- 定义 Cobra CLI 根命令及所有子命令
- 全局配置路径、版本信息 (通过 ldflags 注入)
- 日志初始化

### 命令树

```
ironclaw
├── start       # 启动完整运行时（Telegram 通道）
├── tui         # 启动 TUI 终端交互模式
├── version     # 打印版本信息
├── skill       # 技能管理
│   ├── list    # 列出已安装技能
│   ├── search  # 搜索 ClawHub 技能仓库
│   ├── install # 安装技能
│   ├── update  # 更新技能
│   └── remove  # 删除技能
└── memory      # 记忆管理
    ├── migrate # 从 SQLite 迁移到文件存储
    └── restore # 恢复备份
```

### start 命令流程

```
runStart()
    │
    ├── 1. setupLogging("info")           # 初始日志
    ├── 2. config.Load(cfgPath)           # 加载 YAML 配置
    ├── 3. setupLogging(cfg.Log.Level)    # 按配置重设日志级别
    ├── 4. userdir.Apply(cfg)             # 加载用户目录覆盖
    │       (Soul.md → Personality, Memory.md → PersistentRules)
    ├── 5. gateway.New(cfg)               # 构建 Gateway（核心编排）
    ├── 6. telegram.New(token, userIDs)   # 创建 Telegram 通道
    ├── 7. gw.AddChannel(tg)             # 注册通道
    ├── 8. gw.Start(ctx)                 # 启动所有组件
    └── 9. 等待 SIGINT/SIGTERM → gw.Stop()
```

### skill 命令

技能管理通过两种方式实现：
- **内部操作**（list, remove）：直接使用 `skill.Manager`
- **外部操作**（search, install, update）：委托给 `clawhub` CLI 工具（`npm install -g clawhub`）

技能目录：`~/.IronClaw/skills/`

### 关键设计决策

1. **版本通过 ldflags 注入**：`version`, `commit`, `date` 变量在编译时设置
2. **配置路径默认值**：`configs/ironclaw.yaml`，支持 `-c` 覆盖
3. **用户目录覆盖**：`userdir.Apply()` 在 Gateway 构建前加载 `~/.IronClaw/config.yaml` 等文件
4. **优雅关闭**：通过 context cancellation + signal handling 实现

## tui.go — 终端交互模式

TUI 命令创建一个独立的 Bubble Tea 终端 UI，直接连接到 Agent Runtime，不经过 Telegram。

```
newTUICmd()
    │
    ├── 加载配置 + 用户目录覆盖
    ├── gateway.New(cfg)
    ├── tui.NewAdapter(cfg.TUI)    # 创建 TUI 适配器
    ├── gw.AddChannel(tuiAdapter)  # 注册 TUI 通道
    ├── gw.Start(ctx)
    └── tuiAdapter.Run()           # 阻塞运行 Bubble Tea 主循环
```

## memory.go — 记忆迁移工具

提供从旧版 SQLite `memory_facts` 表到新版 Markdown 文件的迁移：

```
ironclaw memory migrate
    │
    ├── 备份到 ~/.ironclaw/backups/
    └── 逐条转换为 Markdown 文件

ironclaw memory restore
    │
    └── 从备份恢复
```

## 与其他模块的关系

```
main.go
    │
    ├──▶ config.Load()        # 配置加载
    ├──▶ userdir.Apply()      # 用户目录
    ├──▶ gateway.New()        # Gateway 构建（所有核心模块在此接线）
    ├──▶ telegram.New()       # Telegram 通道
    └──▶ skill.New()          # Skill 管理（仅 list/remove 使用）
```
