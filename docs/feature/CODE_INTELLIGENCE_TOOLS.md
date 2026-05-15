# Code Intelligence Tools — Programmatic Codebase Navigation

**日期**: 2026-05-16
**范围**: 新增三个只读代码智能工具（`grep_code`、`find_symbol`、`list_imports`），为 agent 提供语义级别的代码导航能力，替代脆弱的 ad-hoc `bash grep` 命令。对标 Cursor 的代码库理解和 Devin 的代码搜索能力。

## 概述

在代码库中导航是 coding agent 最常见的操作。IronClaw 此前依赖 bash 工具执行 `grep -rn` 命令——这有几个问题：
1. **无结构化输出**：agent 收到的是原始文本，需要自行解析
2. **无语言感知**：搜索 `function foo` 在 Go/Python/JS 中语法不同
3. **无导入理解**：要理解依赖关系必须手动解析文件
4. **无超时控制**：大仓库搜索可能卡住整个 agent 循环

三个新工具提供专门化、结构化、语言感知的代码导航。

## 架构

### 共享执行层

所有三个工具共享 `runCodeIntelCommand()` 执行层：

```go
const codeIntelTimeout = 5 * time.Second

func runCodeIntelCommand(ctx context.Context, dir, name string, args ...string) (string, string, error) {
    ctx, cancel := context.WithTimeout(ctx, codeIntelTimeout)
    defer cancel()
    cmd := exec.CommandContext(ctx, name, args...)
    cmd.Dir = dir
    // 捕获 stdout + stderr，处理超时
}
```

5 秒硬超时防止大仓库搜索阻塞 agent 循环。超时后返回明确错误信息。

### 路径解析

```go
func resolveCodeIntelPath(workingDir, path string) string {
    if path == "" {
        return workingDir    // 未指定时搜索整个仓库
    }
    if filepath.IsAbs(path) {
        return filepath.Clean(path)
    }
    return filepath.Join(workingDir, path)
}
```

## 工具 1：`grep_code`

### 契约

| 字段 | 值 |
|------|-----|
| **工具名** | `grep_code` |
| **只读** | `true` |
| **需审批** | `false` |
| **并行安全** | `ParallelSafe` — 可与其他只读工具完全并行 |
| **执行方式** | 委托给系统 `grep`（非 bash shell） |

### Input Schema

```json
{
    "pattern": "正则表达式模式（必填）— 使用 grep -E 语法",
    "path": "要搜索的子目录（可选，默认：workingDir）",
    "include": "文件 glob 过滤，如 '*.go' 或 '*.{ts,tsx}'（可选）",
    "max_results": "最大返回匹配数（可选，默认 50）"
}
```

### Execute 逻辑

```
1. 构建 grep 命令
   grep -rnI --binary-files=without-match -E "<pattern>" [--include <glob>] <path>
                                      │         │                  │
                          -r 递归      │   文件类型过滤     搜索路径
                          -n 行号      │
                          -I 跳过二进制

2. exec.CommandContext("grep", args...)
   — 不是 bash shell，无注入风险
   — 5 秒超时

3. 解析输出
   ├── grep exit 1（无匹配）→ Result{Output: "", match_count: 0}，不是错误
   └── grep exit 2（错误）  → Result{Error: ...}

4. 截断到 max_results 行，返回 match_count + returned_count
```

### 输出示例

```
internal/agent/act.go:83:func (e *Executor) Run(
internal/agent/act.go:95:func (e *Executor) RunWithContext(
internal/agent/runtime.go:142:func (r *Runtime) Run(
```

Metadata:
```json
{
    "match_count": 3, "returned_count": 3, "path": "internal/"
}
```

## 工具 2：`find_symbol`

### 契约

| 字段 | 值 |
|------|-----|
| **工具名** | `find_symbol` |
| **只读** | `true` |
| **需审批** | `false` |
| **并行安全** | `ParallelSafe` |

### Input Schema

```json
{
    "name": "要查找的符号名，支持部分匹配（必填）",
    "include": "文件 glob，如 '*.go'（可选）",
    "kind": "function | type | var | any（可选，默认 any）"
}
```

### 语言感知模式生成

`buildFindSymbolPattern()` 根据 `kind` 和语言生成精确的正则模式：

**kind=function**:
```
Go:     ^func (receiver )?name\(
Python: ^def name\(
JS:     ^function name\b
JS:     ^(const|let|var) name = (async )?function
JS:     ^(const|let|var) name = (...) =>
```

**kind=type**:
```
Go:        ^type name\b
Python:    ^class name\b
TypeScript: ^interface name\b
```

**kind=var**:
```
Go:     ^var name\b
JS:     ^(const|let|var) name\b
多语言: ^name *[:=]
```

**kind=any**: 合并以上所有模式

### 符号种类自动检测

匹配后，`detectSymbolKind()` 从匹配行推断符号种类：

```go
func detectSymbolKind(line string) string {
    switch {
    case hasPrefix("func "), hasPrefix("def "), hasPrefix("function "):
        return "function"
    case hasPrefix("type "), hasPrefix("class "), hasPrefix("interface "):
        return "type"
    case hasPrefix("var "), hasPrefix("const "), hasPrefix("let "):
        return "var"
    default:
        return "any"
    }
}
```

### 输出示例

```
function internal/agent/act.go:83:func (e *Executor) Run(
function internal/agent/act.go:95:func (e *Executor) RunWithContext(
type internal/agent/cognitive_types.go:51:type ProjectContext struct {
```

Metadata:
```json
{
    "match_count": 3, "kind": "any",
    "matches": [
        {"kind": "function", "match": "internal/agent/act.go:83:func (e *Executor) Run("},
        ...
    ]
}
```

## 工具 3：`list_imports`

### 契约

| 字段 | 值 |
|------|-----|
| **工具名** | `list_imports` |
| **只读** | `true` |
| **需审批** | `false` |
| **并行安全** | `ParallelSafe` |

### Input Schema

```json
{
    "file_path": "源文件路径（必填）"
}
```

### 语言感知导入提取

根据文件扩展名选用不同的解析器：

**Go (`.go`)**:
```
import "single"              → import: "single"
import (                      → block start
    "multi1"                 → import: "multi1"
    alias "multi2"           → import: "multi2"
)                             → block end
```

**Python (`.py`)**:
```
import os, sys                → import: os, import: sys
from django.db import models  → from: django.db
```

**JS/TS (`.js`, `.jsx`, `.ts`, `.tsx`, `.mjs`, `.cjs`)**:
```
import { X } from "module"   → import: module
import "side-effect"         → import: side-effect
const X = require("module")  → require: module
```

**Generic fallback**: 正则 `^(import|from)\s+(.+)$`

### 输出格式

```
行号:种类:模块名
1:import:fmt
2:import:github.com/Forest-Isle/IronClaw/internal/agent
5:import:context
```

Metadata:
```json
{
    "file_path": "/abs/path/to/file.go",
    "imports": [
        {"line": 1, "kind": "import", "module": "fmt", "raw": "\"fmt\""},
        {"line": 2, "kind": "import", "module": "github.com/...", "raw": "\"github.com/...\""}
    ]
}
```

## 工具能力声明（三工具共享模式）

```go
func (t *GrepCodeTool) Capabilities() ToolCapabilities {
    return ToolCapabilities{
        IsReadOnly:      true,
        IsDestructive:   false,
        RequiresNetwork: false,
        ApprovalMode:    "never",
        ParallelSafety:  ParallelSafe,  // 无状态，完全可并行
    }
}
```

| 能力 | grep_code | find_symbol | list_imports |
|------|:--:|:--:|:--:|
| IsReadOnly | ✅ | ✅ | ✅ |
| IsDestructive | ❌ | ❌ | ❌ |
| RequiresNetwork | ❌ | ❌ | ❌ |
| ApprovalMode | never | never | never |
| ParallelSafety | Safe | Safe | Safe |

全部 `ParallelSafe`——意味着 Executor 的并行调度器可以将它们与其他安全工具自由组合并行执行。

## 注册

在 `init_tools.go` 和 `headless.go` 中统一注册：

```go
if gw.cfg.Tools.File.Enabled {
    // ... file_read, file_write, file_edit, file_patch, file_list ...
    gw.tools.Register(tool.NewGrepCodeTool("."))
    gw.tools.Register(tool.NewFindSymbolTool("."))
    gw.tools.Register(tool.NewListImportsTool("."))
}
```

三个工具共享 `tools.file.enabled` 门控，因为它们是只读代码探索工具，与文件读取的安全级别相同。

## 与 LSP 的关系

代码智能工具使用 **grep + 正则模式匹配**，而非 LSP 协议。这是有意的设计选择：

| 维度 | Grep 方案 | LSP 方案 |
|------|----------|---------|
| **语言支持** | 所有语言（通用） | 需要语言服务器（gopls/pyright/ts-server） |
| **安装依赖** | 零（grep 普遍可用） | 需要安装和管理语言服务器 |
| **语义精确度** | 语法级别（正则可覆盖绝大多数用例） | 语义级别（类型解析、跳转定义） |
| **性能** | < 1s（grep 原生速度） | 首次索引需数秒到数十秒 |
| **离线可用** | ✅ | ⚠️ 部分语言服务器需要网络下载 |

LSP 集成（go-to-definition、find-references、diagnostics）计划在 v3 中实现，作为 grep 方案的高级补充。

## 测试覆盖

| 测试 | 说明 |
|------|------|
| `TestGrepCode/basic_pattern_match` | 基本正则搜索 + 结果计数 |
| `TestGrepCode/file_type_filtering` | `--include *.go` 过滤 |
| `TestGrepCode/max_results_capping` | 结果数截断 |
| `TestFindSymbol/go_function_definition` | Go func 匹配 |
| `TestFindSymbol/go_type_definition` | Go type 匹配 |
| `TestFindSymbol/fallback_pattern` | any 种类回退 |
| `TestListImports/go_imports` | Go import 块解析 |
| `TestListImports/python_imports` | Python import 解析 |
| `TestListImports/missing_file` | 文件不存在的错误处理 |

## 文件

| 文件 | 说明 |
|------|------|
| `internal/tool/code_intel.go` | GrepCodeTool + FindSymbolTool + ListImportsTool + 共享执行层 + 语言感知模式生成 + 导入解析器（Go/Python/JS/通用） |
| `internal/tool/code_intel_test.go` | 9 个子测试 |
| `internal/gateway/init_tools.go` | 三工具注册 |
| `internal/gateway/headless.go` | Headless 模式注册 |
