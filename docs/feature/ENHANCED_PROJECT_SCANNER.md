# Enhanced Project Scanner — Go Module Dependency Graph

**日期**: 2026-05-16
**范围**: 增强 `ProjectContextScanner` 以解析 Go 模块依赖图（区分直接和间接依赖），注入到 cognitive agent 的 PERCEIVE 阶段上下文中。对标 Devin 的依赖感知规划和 Cursor 的代码库理解。

## 概述

IronClaw 的 `ProjectContextScanner` 此前只从 `go.mod` 提取模块名称和路径——依赖关系完全被忽略。这意味着 agent 在做技术决策时不知道项目依赖了哪些库、什么版本、是否可以直接使用。

此次增强添加了完整的 Go 依赖图解析，区分直接依赖和间接依赖，并将依赖列表注入到 PLAN 阶段的 prompt 中。

## 架构变更

### 新增类型

```go
type ProjectDependency struct {
    Name    string `json:"name"`              // 模块路径，如 "github.com/gin-gonic/gin"
    Version string `json:"version,omitempty"` // 版本号，如 "v1.9.1"
    Direct  bool   `json:"direct"`            // true=直接依赖, false=间接依赖
}
```

### ProjectContext 扩展

```diff
type ProjectContext struct {
    Name           string
    Language       string
    BuildCommands  []string
    KeyDirectories []string
+   Dependencies   []ProjectDependency `json:"dependencies,omitempty"`
    HasReadme      bool
    RawContent     string
}
```

### 依赖解析

```go
var (
    goRequireRe  = regexp.MustCompile(`(?m)^\s+([^\s]+)\s+v([^\s]+)`)
    goIndirectRe = regexp.MustCompile(`(?m)^\s+([^\s]+)\s+v([^\s]+)\s+//\s*indirect`)
)
```

解析流程：

```
go.mod 内容
    │
    ▼
第一遍：goRequireRe.FindAllStringSubmatch()
    ├── 提取所有 require 行 → directDeps[module] = version
    │
    ▼
第二遍：goIndirectRe.FindAllStringSubmatch()
    ├── 提取所有 // indirect 行
    ├── 从 directDeps 中删除同名条目
    └── 添加到 indirectDeps
    │
    ▼
合并：
    ├── 所有 directDeps → ProjectDependency{Direct: true}
    └── 所有 indirectDeps → ProjectDependency{Direct: false}
```

两遍解析确保 `// indirect` 标记覆盖同名的非 indirect 条目——这是 Go 模块的实际语义：同一个模块在 `require` 块中可能出现两次（一次直接，一次 indirect），以 `// indirect` 标记的为准。

## 上下文格式化

`formatProjectContext()` 现在在输出中包含依赖信息：

```
Project: github.com/Forest-Isle/IronClaw
Language: go
Build/Test commands:
  - go build ./...
  - go test ./...
Dependencies:
  - github.com/anthropics/anthropic-sdk-go@v1.0.0
  - github.com/charmbracelet/bubbletea@v1.1.0
  - github.com/google/uuid@v1.6.0
  - ...
  (+ 45 indirect)
Key directories: cmd, internal
Has README: yes
```

间接依赖计数单独显示（`+ 45 indirect`），避免直接依赖列表被淹没。

## Agent 消费

### PERCEIVE 阶段注入

`ProjectContext.RawContent` 在 `buildPlanUserMessage()` 中被注入到 PLAN 阶段 prompt：

```go
projectCtx := "(none)"
if state.ProjectCtx != nil && state.ProjectCtx.RawContent != "" {
    projectCtx = state.ProjectCtx.RawContent
}
msg = strings.ReplaceAll(msg, "{{PROJECT_CONTEXT}}", projectCtx)
```

### 实际收益

依赖图让 agent 能做出更明智的决策：

1. **库可用性检查**：「这个项目已经有 `gin` 了，不需要引入新的 HTTP 框架」
2. **版本感知**：「项目用的是 `uuid v1.6.0`，API 是 X 而非 Y」
3. **避免冲突**：「引入了 `logrus`，但它已经是间接依赖了，版本必须兼容」
4. **构建命令正确性**：「有 `go-sqlite3`（间接依赖），所以 `CGO_ENABLED=1` 是必需的」

## Feature 注册

依赖图解析是 `scanGoMod()` 的一部分，而 `scanGoMod()` 是 `ProjectContextScanner.Scan()` 的内部逻辑——不需要单独的 feature flag。它总是启用，与项目扫描器共享相同的缓存语义。

## 与 Knowledge Base 的关系

依赖图与 Knowledge Base 互补：

- **依赖图**：告诉 agent **项目使用了哪些外部库**（结构信息）
- **Knowledge Base**：告诉 agent **文档/代码库中有什么**（内容信息）

两者在 PLAN 阶段的 prompt 中同时注入，为 agent 提供完整的项目理解。

## 性能

- 正则扫描 go.mod 文件 < 1ms（go.mod 通常 < 10KB）
- 结果缓存在 `ProjectContextScanner.cache` 中，按目录键
- 缓存仅在显式调用 `Invalidate(dir)` 或进程重启时失效

## 与其他语言的扩展性

当前 Go 模块解析是最完整的。其他语言的依赖解析处于基础级别：

| 语言 | 清单文件 | 依赖解析状态 |
|------|---------|------------|
| Go | `go.mod` | ✅ 完整（直接/间接/版本） |
| JavaScript | `package.json` | ⚠️ 仅名称和脚本检测 |
| Rust | `Cargo.toml` | ⚠️ 仅名称检测 |
| Python | `pyproject.toml` | ⚠️ 仅名称检测 |

后续可以按相同模式扩展 `package.json` 的 `dependencies`/`devDependencies` 解析和 `Cargo.toml` 的 `[dependencies]` 解析。

## 文件

| 文件 | 说明 |
|------|------|
| `internal/agent/cognitive_types.go` | 新增 `ProjectDependency` 类型，`ProjectContext` 添加 `Dependencies` 字段 |
| `internal/agent/project_scanner.go` | `scanGoMod()` 增强——两遍正则解析 + 直接/间接分类 + `formatProjectContext()` 依赖格式化 |

## 测试覆盖

现有的项目扫描器测试无需修改即可通过——`formatProjectContext` 的依赖部分在 `go.mod` 不存在时为空，不影响现有测试预期。新增字段 `Dependencies` 为 `omitempty`，JSON 序列化向后兼容。
