# 10 - Skill 技能系统

## 文件结构

```
internal/skill/
├── skill.go       # Skill 结构体 + 解析
├── manager.go     # Manager — 加载/匹配/管理
└── builtin/       # 内置技能目录
```

## 一、Skill 定义

### 文件格式：SKILL.md

```markdown
---
name: "web-scraper"
description: "Web scraping and data extraction skill"
version: "1.0.0"
author: "community"
tags: ["web", "scraping", "data"]
metadata:
  openclaw:
    requires:
      env: ["PROXY_URL"]
      bins: ["chromium"]
    primaryEnv: "PROXY_URL"
---

# Web Scraper Skill

这里是技能的完整内容...
（Markdown 格式的指令/提示词/工作流描述）
```

### 数据结构

```go
type Skill struct {
    // 元数据（急加载）
    Name        string
    Description string
    Version     string
    Author      string
    Tags        []string
    Metadata    SkillMeta
    Path        string       // 文件绝对路径

    // 内容（懒加载）
    content     string
    contentOnce sync.Once
    contentErr  error
}
```

### 懒加载设计

```
ParseSkill(path)
    │
    ├── 读取文件
    ├── splitFrontmatter()
    │   ├── YAML frontmatter → 元数据（立即解析）
    │   └── Markdown body → 延迟加载
    │
    └── 返回 Skill（仅含元数据）

skill.Content()  ← 首次调用时加载
    │
    ├── sync.Once 保证只加载一次
    ├── 重新读取文件
    ├── splitFrontmatter()
    └── 返回 Markdown body
```

**设计原因**：Agent 系统启动时可能加载数十个技能，但每次对话只使用 1-2 个。懒加载避免启动时浪费内存。

## 二、Manager（技能管理器）

```go
type Manager struct {
    skills map[string]*Skill   // name → Skill
    mu     sync.RWMutex
}
```

### 加载来源

```
1. LoadBuiltin()       → 内置技能 (builtin/ 目录)
2. LoadDir(path)       → 用户技能 (~/.IronClaw/skills/)
3. LoadDir(extraDirs)  → 额外目录 (配置指定)
```

### 技能匹配

```go
// BuildPromptSection 构建注入系统提示的技能摘要
func (m *Manager) BuildPromptSection(userText string) string
```

匹配逻辑：
1. 遍历所有已加载技能
2. 根据 name、description、tags 与 userText 匹配
3. 匹配的技能：元数据摘要注入系统提示
4. Agent 需要完整内容时通过 `read_skill` 工具加载

### 渐进式披露模式

```
┌──────────────────────────────────────────────────┐
│                系统提示注入                        │
│                                                   │
│  ## Available Skills                              │
│  - web-scraper: Web scraping and data extraction  │
│  - code-reviewer: Code review and analysis        │
│  - data-viz: Data visualization generation        │
│                                                   │
│  Use `read_skill` tool to load full content.      │
└──────────────────────────────────────────────────┘
            │
            │ Agent 判断需要使用某个技能
            ▼
┌──────────────────────────────────────────────────┐
│  read_skill({ "name": "web-scraper" })           │
│                                                   │
│  → 返回完整 Markdown 内容                         │
│  → Agent 按技能指令执行                           │
└──────────────────────────────────────────────────┘
```

这种设计避免了将所有技能的完整内容都塞入系统提示，大幅节省 token。

## 三、ClawHub 集成

技能的搜索/安装/更新通过外部 CLI 工具 `clawhub` 完成：

```bash
# 搜索
ironclaw skill search "web scraping"
# → 委托: clawhub search "web scraping" --limit 10

# 安装
ironclaw skill install web-scraper
# → 委托: clawhub install web-scraper --workdir ~/.IronClaw

# 更新
ironclaw skill update web-scraper
# → 委托: clawhub update web-scraper --workdir ~/.IronClaw

# 列表（内部实现）
ironclaw skill list
# → 直接读取 ~/.IronClaw/skills/

# 删除（内部实现）
ironclaw skill remove web-scraper
# → 删除目录 + 确认
```

## 四、OpenClaw 元数据

```yaml
metadata:
  openclaw:
    requires:
      env: ["PROXY_URL", "API_KEY"]    # 必需环境变量
      bins: ["chromium", "ffmpeg"]      # 必需二进制
    primaryEnv: "PROXY_URL"             # 主要环境变量
```

用于：
- 安装时检查依赖
- 运行时验证环境
- ClawHub 仓库中的兼容性标注

## 设计亮点

1. **懒加载**：元数据急加载 + 内容懒加载，优化启动性能
2. **渐进披露**：系统提示只含摘要，完整内容按需加载
3. **外部包管理**：搜索/安装委托给 clawhub，保持 Go 代码轻量
4. **YAML + Markdown**：人类可读的技能格式，易于编写和分享
