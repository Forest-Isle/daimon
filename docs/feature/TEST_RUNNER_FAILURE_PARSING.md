# Test Runner with Failure Parsing — Structured Test Execution

**日期**: 2026-05-16
**范围**: 新增 `test_run` 工具，为 agent 提供自动化测试运行 + 结构化失败解析能力，支撑 test→fail→analyze→fix→retest 闭环。对标 Claude Code / Devin 的测试驱动开发工作流。

## 概述

顶级 coding agent 的核心工作流之一是**测试驱动修复**：

```
Run Tests → Parse Failures → Feed Failures to LLM → Generate Fix → Run Tests Again
```

IronClaw 此前缺少这个基础设施——agent 可以通过 `bash` 工具运行 `go test`，但：
1. 输出是原始文本，agent 需要自行解析
2. 失败信息没有结构化的文件位置映射
3. 没有超时控制，慢测试会阻塞 agent 循环
4. 没有测试命令自动检测——agent 需要猜测项目用什么测试框架

`test_run` 解决了所有这四个问题。

## 架构

### 数据结构

```go
type TestRunTool struct {
    workingDir string
}

type testFailure struct {
    Name    string `json:"name"`              // 测试名称，如 "TestFoo"
    Message string `json:"message"`           // 失败消息，已截断到 512 字符
    File    string `json:"file,omitempty"`    // 文件位置，如 "foo_test.go:42"
}

type testRunOutput struct {
    Success     bool          `json:"success"`
    ExitCode    int           `json:"exit_code"`
    TotalTests  int           `json:"total_tests"`
    Passed      int           `json:"passed"`
    Failed      int           `json:"failed"`
    Failures    []testFailure `json:"failures"`
    Summary     string        `json:"summary"`
    Command     string        `json:"command"`
    Output      string        `json:"output"`
    DurationMs  int64         `json:"duration_ms"`
    WorkingDir  string        `json:"working_dir"`
    // 边界标志
    Truncated   bool `json:"truncated,omitempty"`
    TimedOut    bool `json:"timed_out,omitempty"`
    EmptyOutput bool `json:"empty_output,omitempty"`
}
```

### 执行流程

```
1. 命令确定
   ├── 用户提供了 command → 直接使用
   └── command 为空 → detectTestCommand()
       ├── go.mod 存在？   → "go test ./..."
       ├── package.json 含 test script？ → "npm test"
       ├── Cargo.toml 存在？ → "cargo test"
       └── Makefile 含 test target？ → "make test"
   │
2. 超时设置（默认 120s，可配置）
   │
3. 通过 bash -lc 执行（利用 shell 环境变量和 PATH）
   │
4. 输出截断到 max_output_lines（默认 200 行）
   │
5. parseTestOutput() — 见下方
   │
6. 构建 testRunOutput → JSON 序列化 → Result.Output
   同时填充 Result.Metadata 为结构化键值对
```

## 失败解析

### Go Test 输出解析（主要路径）

`parseGoFailures()` 实现 Go test 输出的专用状态机：

```
状态机遍历每一行：

┌─ "--- FAIL: TestName" ──→ 创建 current testFailure{Name: "TestName"}
│
├─ "    foo_test.go:42: expected X, got Y" ──→ current.File = "foo_test.go:42"
│                                              current.Message += "expected X, got Y"
│
├─ "--- PASS: TestName" ──→ finalizeFailure(current)，清空 current
│
├─ "FAIL" ──→ 同上
│
├─ 其他非空行 ──→ 追加到 current.Message（以 \n 连接）
│
└─ EOF ──→ 如果 current 不为空，finalizeFailure(current)
```

正则模式：

```go
goFailLineRE = regexp.MustCompile(`^--- FAIL: ([^ ]+)`)        // 测试名
goFileLineRE = regexp.MustCompile(`^\s+([^\s:]+_test\.go:\d+):\s*(.*)$`)  // 文件:行号 + 消息
```

### 通用失败解析（回退路径）

当 Go 解析器找不到失败时，回退到通用解析：

```go
func isFailureLine(line string) bool {
    upper := strings.ToUpper(line)
    return strings.Contains(upper, "FAIL") ||
           strings.Contains(upper, "FAILED") ||
           strings.Contains(line, "Error:") ||
           strings.Contains(strings.ToLower(line), "assertion failed")
}
```

这覆盖了 Python unittest/pytest、JS jest/mocha、Rust cargo test 等框架的输出。

### 失败消息截断

```go
const maxFailureMessageLen = 512  // 字符

func finalizeFailure(f testFailure) testFailure {
    f.Message = util.TruncateStr(f.Message, maxFailureMessageLen)
    return f
}
```

防止超长断言错误消息膨胀 agent 上下文。

## 边界情况处理

| 场景 | 行为 |
|------|------|
| **超时** | `TimedOut=true`，ExitCode=-1，自动添加 timeout testFailure |
| **命令未找到**（exit 127） | `Failed++`，添加 "command" testFailure 含错误消息 |
| **空输出** | `EmptyOutput=true`，仍返回有效的 success 状态 |
| **无失败**（exit 0） | `Passed=1, TotalTests=1, Success=true` |
| **只有失败**（exit != 0，无 PASS 行） | `Failed=len(failures), TotalTests=Failed` |
| **触发截断** | `Truncated=true`，Result.IsPartial=true |
| **go.mod 不存在** | `detectTestCommand()` 返回 "" → 需要用户提供 command |

## 自动检测逻辑

```go
func detectTestCommand(workingDir string) string {
    candidates := []candidate{
        {name: "go.mod",       fn: fileExists,        cmd: "go test ./..."},
        {name: "package.json", fn: hasNPMTestScript,  cmd: "npm test"},
        {name: "Cargo.toml",   fn: fileExists,        cmd: "cargo test"},
        {name: "Makefile",     fn: hasMakeTestTarget,  cmd: "make test"},
    }
    for _, c := range candidates {
        if c.fn(filepath.Join(workingDir, c.name)) {
            return c.cmd
        }
    }
    return ""
}
```

`hasNPMTestScript()` 解析 `package.json` 的 `scripts.test` 字段确保 test 脚本存在而不只是文件存在。`hasMakeTestTarget()` 用正则 `(?m)^test\s*:` 扫描 Makefile。

## 工具契约

| 字段 | 值 |
|------|-----|
| **工具名** | `test_run` |
| **只读** | `true`（测试运行不修改源代码） |
| **需审批** | `false` |
| **执行方式** | `bash -lc <command>`（继承 shell 环境） |
| **注册门控** | `cfg.Tools.Bash.Enabled` |

## 注册

```go
if gw.cfg.Tools.Bash.Enabled {
    gw.tools.Register(tool.NewBashTool(...))
    gw.tools.Register(tool.NewTestRunTool("."))  // 与 bash 同门控
}
```

共享 `bash.enabled` 门控因为 `test_run` 通过 shell 执行命令，安全级别与 bash 工具相同。

## 输出示例

### 成功运行

```json
{
    "success": true, "exit_code": 0,
    "total_tests": 5, "passed": 5, "failed": 0,
    "failures": [],
    "summary": "5 passed, 0 failed",
    "command": "go test ./...",
    "duration_ms": 1234
}
```

### 有失败

```json
{
    "success": false, "exit_code": 1,
    "total_tests": 5, "passed": 3, "failed": 2,
    "failures": [
        {"name": "TestFilePatch", "message": "expected X got Y\npatch context mismatch", "file": "file_patch_test.go:42"},
        {"name": "TestVerify", "message": "diff summary missing", "file": "interceptor_verify_test.go:117"}
    ],
    "summary": "3 passed, 2 failed",
    "command": "go test ./internal/tool/",
    "duration_ms": 2150
}
```

### 超时

```json
{
    "success": false, "exit_code": -1,
    "total_tests": 1, "passed": 0, "failed": 1,
    "failures": [
        {"name": "timeout", "message": "test command timed out after 2m0s"}
    ],
    "summary": "0 passed, 1 failed",
    "timed_out": true,
    "duration_ms": 120000
}
```

## Agent 工作流集成

`test_run` 的输出格式专为 LLM 消费设计——failures 数组直接提供 LLM 修复失败所需的三要素：

```
TestFilePatch  → file_patch_test.go:42  → "expected X got Y"
   测试名              文件位置                  失败原因
```

这使 agent 可以将失败信息直接注入到修复 prompt 中，无需额外的解析或格式化步骤。

典型的 test→fix 循环：

```
1. agent 调用 test_run({})  →  auto-detects "go test ./..."
2. 收到 {"failed": 2, "failures": [{name, file, message}, ...]}
3. 对于每个 failure：
   a. file_read(failure.file) → 读取测试文件
   b. grep_code(failure.name) → 定位实现代码
   c. file_patch({path, patch}) 或 file_edit → 应用修复
4. test_run({}) → 验证修复
5. 重复直到 failed == 0
```

## 测试覆盖

| 测试 | 说明 |
|------|------|
| `TestDetectTestCommand_GoMod` | go.mod 存在时自动检测为 `go test ./...` |
| `TestTestRun_ExecuteSimpleCommand` | 执行简单命令验证成功路径 |
| `TestTestRun_Timeout` | 验证超时检测和 timed_out 元数据 |
| `TestTestRun_CommandNotFound` | 验证 exit 127 处理 |

## 文件

| 文件 | 说明 |
|------|------|
| `internal/tool/test_run.go` | TestRunTool + 命令自动检测 + Go 失败解析器 + 通用失败解析 + 输出截断 |
| `internal/tool/test_run_test.go` | 4 个测试用例 |
| `internal/gateway/init_tools.go` | 工具注册（bash 门控） |
| `internal/gateway/headless.go` | Headless 模式注册 |
