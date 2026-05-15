# Precision Diff Editing — Surgical Unified Diff Application

**日期**: 2026-05-16
**范围**: 新增 `file_patch` 工具，对标 Claude Code 的核心精编能力——基于 unified diff 的精密代码修改，含 ±1 行容错、dry-run 预览、自动 git 验证。

## 概述

Claude Code 最核心的竞争优势是**精密字符串匹配编辑**——不是粗糙的整文件覆盖或行号搜索替换，而是用 unified diff 格式精确指定要修改的内容，自动定位上下文、应用修改、验证结果。

IronClaw 此前只有 `file_write`（整文件覆盖）和 `file_edit`（search/replace 模式），缺少这种精密编辑能力。`file_patch` 填补了这一核心缺口。

## 架构

### 纯 Go Unified Diff 解析器

没有依赖外部 `patch(1)` 命令——diff 解析和应用全部在 Go 中实现，提供细粒度错误报告和跨平台兼容性。

### 数据结构

```go
type FilePatchTool struct {
    workingDir string  // 文件路径解析的基准目录
}

type patchHunk struct {
    OldStart int        // @@ -old_start,old_count ...
    OldCount int
    NewStart int
    NewCount int
    Lines    []patchLine
    Header   string      // 原始 hunk header
}

type patchLine struct {
    Kind rune            // ' ' 上下文, '-' 删除, '+' 添加
    Text string
}
```

### 解析流程

```
Raw Patch Text
    │
    ▼
parseUnifiedDiff()
    ├── 按 @@" 识别 hunk 边界
    ├── 正则 `^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`" 解析 header
    ├── 逐行分类：' ' → context, '-' → removal, '+' → addition
    └── 忽略 diff 元数据行（--- / +++ / index / diff）
    │
    ▼
applyPatchHunks() 
    ├── hunkSequences(hunk) → 提取 original 和 replacement 行序列
    ├── locateHunk() → 在当前文件中定位 original 序列
    │   ├── 精确匹配：baseIndex
    │   ├── +1 行偏移
    │   └── -1 行偏移
    ├── 应用替换：current[:pos] + replacement + current[pos+len(original):]
    └── 追踪 lineDelta 用于后续 hunk 偏移计算
    │
    ▼
gitDiffStat() — 验证修改
```

### ±1 行容错机制

`locateHunk()` 实现三级匹配策略：

```go
func locateHunk(lines []string, baseIndex int, original []string) (int, int, error) {
    candidates := []int{baseIndex, baseIndex - 1, baseIndex + 1}
    for _, candidate := range candidates {
        if matchesAt(lines, candidate, original) {
            return candidate, candidate - baseIndex, nil
        }
    }
    return 0, 0, fmt.Errorf("context mismatch near line %d", baseIndex+1)
}
```

当文件行号因之前的 hunk 发生偏移或被其他进程修改时，±1 容错使得 patch 仍然可以正确应用。匹配失败时返回详细的上下文不匹配错误，包含期望的行内容。

## API

### 工具契约

| 字段 | 值 |
|------|-----|
| **工具名** | `file_patch` |
| **需审批** | `false`（由 PlanMode 拦截层控制写入审批） |
| **只读** | `false` |
| **并行安全** | `PathScoped` — 不同文件可并行，同文件排队 |

### Input Schema

```json
{
    "path": "相对或绝对文件路径（必填）",
    "patch": "unified diff 格式的 patch 内容（必填）",
    "dry_run": "布尔值，预览变更而不实际写入（可选，默认 false）"
}
```

### Execute 逻辑

1. 解析 `path` — 相对路径基于 `workingDir` 拼接
2. 读取目标文件内容
3. 调用 `parseUnifiedDiff()` 解析 patch 文本为 hunk 列表
4. 如果 `dry_run`：返回每个 hunk 的预览（header + old_start + line_count），不写入
5. 否则：调用 `applyPatchHunks()` 应用修改
6. `os.WriteFile()` 写入 patched 内容
7. 运行 `git diff --stat -- <path>` 获取变更摘要
8. 返回 `Result{Output, Metadata: {diff_summary, hunks, warnings}}`

### 边界情况

| 场景 | 行为 |
|------|------|
| **空 patch** | 返回 "no changes applied" + warning |
| **无 hunk** | 返回 "patch contained no hunks" |
| **上下文不匹配** | 返回 Error + 期望的行内容 |
| **`\ No newline at end of file`** | 忽略该标记行 |
| **文件不存在** | 返回 `os.ReadFile` 错误 |
| **末尾添加** | `locateHunk` 的 `len(original)==0` 分支直接 clamp 到末尾位置 |

### 与 PlanMode 的整合

`file_patch` 在 `isPlanModeWriteTool()` 中注册为写工具：

```go
case "file_write", "file_edit", "worktree_create", "worktree_merge", "file_patch":
    return true
```

当 PlanMode 激活时，`file_patch` 只能在已审批的 plan 框架内执行。

## 工具能力声明

```go
func (t *FilePatchTool) Capabilities() ToolCapabilities {
    return ToolCapabilities{
        IsReadOnly:      false,
        IsDestructive:   false,
        RequiresNetwork: false,
        ApprovalMode:    "auto",
        ParallelSafety:  ParallelPathScoped,
    }
}

func (t *FilePatchTool) ExtractPaths(input []byte) ([]string, error) {
    // 提取 path 字段，基于 workingDir 解析为绝对路径
    // 返回 CanonicalizePath() 的结果用于冲突检测
}
```

`ParallelPathScoped` 声明使得 Executor 可以对不同文件并行应用 patch，而同文件操作自动串行化。

## 注册

### Gateway 注册路径

**主路径** (`internal/gateway/init_tools.go`)：
```go
if gw.cfg.Tools.File.Enabled {
    // ... file_read, file_write, file_edit ...
    gw.tools.Register(tool.NewFilePatchTool("."))
    // ... file_list ...
}
```

**Headless 路径** (`internal/gateway/headless.go`)：同上注册，确保所有运行模式下工具可用。

### Sandbox 集成

`FilePatchTool` 在 sandbox 拦截器中被识别为写工具，自动纳入文件路径白名单检查：

```go
// internal/tool/interceptor_sandbox.go
case "file_write", "file_edit", "file_patch":
    return true
```

## 与 file_edit 的差异

| 维度 | `file_edit` | `file_patch` |
|------|-------------|-------------|
| **定位方式** | 字符串 search/replace | Unified diff hunk 上下文匹配 |
| **多段修改** | 单次单文件 | 单次多 hunk（同一文件） |
| **容错** | 精确字符串匹配 | ±1 行偏移容错 |
| **预览** | 无 | `dry_run` 模式 |
| **验证** | 无 | 自动 `git diff --stat` |
| **精确度** | 依赖搜索字符串唯一性 | 依赖上下文行唯一性 |

## 测试覆盖

| 测试 | 说明 |
|------|------|
| `TestFilePatchSingleHunk` | 单 hunk：修改一个函数的几行 |
| `TestFilePatchMultiHunk` | 多 hunk：修改文件中的两个不连续区域 |
| `TestFilePatchAddAtEnd` | 在文件末尾添加新行 |
| `TestFilePatchFailure` | 上下文不匹配——期望返回错误 |
| `TestFilePatchDryRun` | Dry run 模式——验证预览内容正确 |
| `TestFilePatchEmptyPatch` | 空 patch——返回 no-op 成功 |

## 文件

| 文件 | 说明 |
|------|------|
| `internal/tool/file_patch.go` | FilePatchTool 核心实现 + unified diff 解析器 + hunk 应用引擎 + git 验证 |
| `internal/tool/file_patch_test.go` | 6 个测试用例覆盖所有边界情况 |
| `internal/tool/interceptor_sandbox.go` | 添加 `file_patch` 到写工具识别列表 |
| `internal/tool/interceptor_sandbox_test.go` | 补充 `file_patch` 的 sandbox 测试 |
| `internal/gateway/init_tools.go` | 工具注册 |
| `internal/gateway/headless.go` | Headless 模式工具注册 |
