# Post-Edit Verification — Write-Then-Verify Interceptor

**日期**: 2026-05-16
**范围**: 新增 `VerifyInterceptor`，在每次文件写入操作后自动重新读取文件并运行 `git diff`，防止静默写入失败和部分编辑遗漏。对标 Claude Code / Devin 的自动验证模式。

## 概述

顶级 coding agent 的共同特征之一是**写入后验证**——不是执行完工具就结束，而是主动确认修改确实发生了、文件可读、diff 符合预期、没有被截断。

IronClaw 此前缺少这一层。`VerifyInterceptor` 在拦截器链中自动对所有写操作追加验证步骤，完全透明——agent 无需主动调用验证工具，验证结果直接注入到 tool result 的 metadata 中。

## 架构

### 拦截器链位置

```
PermissionInterceptor  →  HookInterceptor  →  UserHookInterceptor 
    →  SandboxInterceptor  →  [VerifyInterceptor]  →  AuditInterceptor
```

Verify 位于 Sandbox 之后（写操作已被允许）和 Audit 之前（验证数据被记录到审计日志）。

### 数据结构

```go
type VerifyInterceptor struct {
    workingDir string      // git 仓库根路径
    logger     *slog.Logger
}

type verifyMetadata struct {
    DiffSummary   string `json:"diff_summary,omitempty"`   // git diff --stat 输出
    FileReadable  bool   `json:"file_readable"`            // 文件是否可读
    FileSizeBytes int64  `json:"file_size_bytes,omitempty"`// 文件大小
}
```

### 拦截器接口适配

`VerifyInterceptor` 实现了仓库中实际的 `ToolInterceptor` 接口（`Name()` + `Intercept(ctx, call, next)`），而非假设的 `BeforeExecute/AfterExecute` 接口：

```go
func (v *VerifyInterceptor) Intercept(
    ctx context.Context,
    call *ToolCall,
    next InterceptorFunc,
) (*ToolResult, error) {
    result, err := next(ctx, call)
    // 仅在执行成功（无错误、result.Error 为空）且为写工具时验证
    if err != nil || result == nil || result.Error != "" || !shouldVerifyToolCall(call) {
        return result, err
    }
    // ... 验证逻辑 ...
    return result, err
}
```

## 验证流程

### 触发条件

`shouldVerifyToolCall()` 仅在以下情况触发：

| 工具 | 触发条件 |
|------|---------|
| `file_write` | 总是 |
| `file_edit` | 总是 |
| `file_patch` | 总是 |
| `bash` | `bashLikelyWritesJSON()` 检测到写操作关键字（`>`, `>>`, `tee`, `touch`, `mkdir`, `sed -i`, `git commit` 等 17 个模式） |

### 验证步骤

```
1. 提取目标路径
   ├── file_write/edit/patch → 解析 JSON input 的 "path" 字段
   └── bash                  → 从命令字符串提取输出/修改路径
       ├── 重定向检测：`> file` `>> file` → verifyRedirectRE
       ├── tee 检测：`tee file` `tee -a file` → verifyTeeRE
       ├── sed -i 检测 → verifySedInPlace
       ├── touch 检测 → verifyTouchRE
       └── 其他：mkdir, truncate, install, cp, mv（9 个正则匹配器）
    │
2. os.Stat() → 确认文件存在且不是目录
    │
3. 文件 > 1MB → 跳过读取，记录 warning（防止大文件内存问题）
    │
4. os.ReadFile() → 确认文件可读
    │
5. git diff 三级回退（见下方）
    │
6. 结果注入 result.Metadata["verify"] 和 result.Metadata["verify_warnings"]
```

### Git Diff 三级回退

```go
// 第一级：git diff --stat（已跟踪且已修改的文件）
runGitDiffStat(ctx, dir, "diff", "--stat", "--", relPath)

// 第二级：git diff --cached --stat（已暂存但未提交的文件）
runGitDiffStat(ctx, dir, "diff", "--cached", "--stat", "--", relPath)

// 第三级：git status --porcelain（未跟踪的新文件）
runGitDiffStat(ctx, dir, "status", "--porcelain", "--", relPath)
```

这使得验证能够检测到所有三种状态的变更：已跟踪修改、已暂存、新建/未跟踪。

### Git 不可用降级

当 git 不在 PATH 或工作目录不是 git 仓库时：

```go
cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
if exec.Error → "verification warning: git unavailable: ..."
if exit error → "verification warning: working directory is not a git repository"
```

验证永远不会因为 git 不可用而失败——只追加 warning。

## 关键设计决策

### 验证是建议性的，永远不阻断

```go
// 验证失败只会追加 warning，不会修改 result.Error
// 工具执行成功 → result 保持 success 状态
// 只有 metadata.verify_warnings 会反映验证问题
```

这避免了验证层的假阳性故障打断 agent 工作流——验证数据是**信息增量**而非**阻断条件**。

### 1MB 文件读取上限

```go
const verifyReadLimitBytes int64 = 1 << 20 // 1MB

if info.Size() > verifyReadLimitBytes {
    warnings = append(warnings, "verification skipped: file exceeds 1MB read limit")
}
```

防止验证大文件时造成内存压力。大文件仍会被 `os.Stat()` 确认存在，只是不读取内容。

### Metadata 兼容性

拦截器链的 `ToolResult.Metadata` 是 `map[string]string`，不是 `map[string]any`。为保持与现有链的兼容性，verify 数据通过 JSON 序列化后作为字符串存储：

```go
result.Metadata["verify"] = json.Marshal(verifyMetadata{...})  // JSON string
result.Metadata["verify_warnings"] = json.Marshal(warnings)    // JSON string
```

## 配置

```yaml
tools:
  verify:
    enabled: true  # 默认开启
```

在 `internal/config/config.go` 中定义：

```go
type VerifyConfig struct {
    Enabled bool `yaml:"enabled"`
}
```

在 `init_tools.go` 中条件性插入：

```go
verifyInterceptor := tool.NewVerifyInterceptor(".")
if gw.cfg.Tools.Verify.Enabled {
    interceptors = append(interceptors, verifyInterceptor)
}
```

## 验证输出示例

成功的 file_write 后的 Metadata：

```json
{
    "verify": "{\"diff_summary\":\"foo.go | 5 +++--\\n 1 file changed, 3 insertions(+), 2 deletions(-)\",\"file_readable\":true,\"file_size_bytes\":1024}",
    "success": "true"
}
```

bash 创建新文件后（第三级 git status 回退）：

```json
{
    "verify": "{\"diff_summary\":\"?? new_file.txt\",\"file_readable\":true,\"file_size_bytes\":12}",
    "verify_warnings": "[]" 
}
```

## 测试覆盖

| 测试 | 说明 |
|------|------|
| `TestVerifyInterceptor_FileWriteAddsVerificationMetadata` | file_write 后 metadata 包含 diff_summary 和 file_readable |
| `TestVerifyInterceptor_ReadOnlyToolPassesThrough` | 只读工具不触发验证，result 不变 |
| `TestVerifyInterceptor_FailedToolPassesThrough` | 执行失败的工具不触发验证，直接返回原始错误 |
| `TestVerifyInterceptor_BashWriteCommandVerifies` | bash 写命令（`echo > file`）被正确识别和验证 |
| `TestVerifyInterceptor_BashReadOnlyCommandSkipsVerification` | bash 只读命令（`ls`）不触发验证 |

## 文件

| 文件 | 说明 |
|------|------|
| `internal/tool/interceptor_verify.go` | VerifyInterceptor 核心 + bash 写检测 + git diff 三级回退 + 路径提取（9 个正则匹配器） |
| `internal/tool/interceptor_verify_test.go` | 5 个测试用例 |
| `internal/config/config.go` | VerifyConfig 类型 + 默认值（Enabled: true） |
| `internal/gateway/init_tools.go` | 拦截器链集成 + 条件性插入 |

## 性能考量

- `os.Stat()` + `os.ReadFile()` 对常规源文件 < 1ms
- `git diff --stat` 对单个文件 < 5ms
- 验证的总延迟增量通常在 2-8ms 范围内
- 对于 agent 的工具执行周期（通常 100ms-2s），验证开销可忽略不计
