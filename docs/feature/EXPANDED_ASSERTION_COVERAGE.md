# 断言覆盖扩展

**日期**: 2026-04-18
**范围**: 将 OBSERVE 阶段的结构化断言从 3 种工具类型扩展到覆盖全部主要工具类型 + 通用回退

## 概述

此前的断言系统（`generateAssertions`）仅覆盖 `bash`、`http`、`file_write`/`file_edit` 三类工具。其余工具（浏览器、MCP、技能、记忆、file_read）执行后不会生成断言，导致静默失败无法被 OBSERVE 阶段捕获。

本次改动将断言覆盖从 3 种工具扩展到 10+ 种，并引入通用回退机制，确保任何工具的显式错误都能被检测。

## 变更前后对比

### 变更前

```go
switch obs.ToolName {
case "bash":       return bashAssertions(obs)
case "http":       return httpAssertions(obs)
case "file_write", "file_edit": return fileWriteAssertions(obs)
default:           return nil  // ← 静默跳过
}
```

### 变更后

```go
switch {
case obs.ToolName == "bash":                          return bashAssertions(obs)
case obs.ToolName == "http":                          return httpAssertions(obs)
case obs.ToolName == "file_write" || "file_edit":     return fileWriteAssertions(obs)
case obs.ToolName == "file_read":                     return fileReadAssertions(obs)
case obs.ToolName == "browser_search":                return browserSearchAssertions(obs)
case obs.ToolName == "browser_extract":               return browserExtractAssertions(obs)
case strings.HasPrefix(obs.ToolName, "mcp_"):         return mcpAssertions(obs)
case strings.HasPrefix(obs.ToolName, "skill_") || "read_skill": return skillAssertions(obs)
case strings.HasPrefix(obs.ToolName, "memory_"):      return memoryAssertions(obs)
default:                                              return genericAssertions(obs)
}
```

## 新增断言详情

| 工具类型 | 断言函数 | 检查项 |
|---------|---------|--------|
| `file_read` | `fileReadAssertions` | `file read succeeded`（无 error）+ `file content is non-empty`（内容非空白） |
| `browser_search` | `browserSearchAssertions` | `search succeeded`（无 error）+ `search returned results`（JSON 解析 results 数组非空且无 error 字段） |
| `browser_extract` | `browserExtractAssertions` | `extract succeeded`（无 error）+ `extracted content is non-empty`（JSON 解析 content 字段非空且无 error 字段） |
| `mcp_*` | `mcpAssertions` | `MCP tool succeeded`（无 error）+ `MCP response has no error field`（JSON 解析 error 字段为空） |
| `skill_*` / `read_skill` | `skillAssertions` | `skill execution succeeded`（无 error）+ `skill produced output`（输出非空白） |
| `memory_*` | `memoryAssertions` | `memory operation succeeded`（无 error） |
| (其他) | `genericAssertions` | 仅在 `obs.Error != ""` 时生成 `tool execution succeeded = false`；无错误时不生成断言（保持后向兼容） |

## 设计决策

### 前缀匹配 vs 精确匹配

MCP 工具名形如 `mcp_server_tool`，技能工具名形如 `skill_name`，记忆工具名形如 `memory_search`/`memory_add`。使用 `strings.HasPrefix` 做前缀匹配，确保新注册的 MCP 服务器和技能自动获得断言覆盖。

### 通用回退

`genericAssertions` 仅在工具返回显式 `Error` 时才生成失败断言，无错误时返回 nil。这避免了为完全未知的工具生成可能误报的断言，同时确保明确的错误不会静默通过。

### JSON 输出结构解析

`browser_search`、`browser_extract`、`mcp_*` 的断言函数会尝试 JSON 解析工具输出，但解析失败时不会生成额外断言（仅保留基础的 error 检查）。这确保了非标准输出格式不会导致误报。

## 断言覆盖统计

集成测试验证了 7 种工具类型全部生成断言：

```
assertion coverage: map[
  bash:2
  browser_search:2
  file_read:2
  http:1
  mcp_github_search:1
  memory_search:1
  skill_deploy:2
]
```

## 后续改进

本次扩展覆盖后，发现多个断言函数对工具输出格式的假设与实际输出不一致（测试和代码"错得一致"）。详见 [ASSERTION_METADATA_FIX.md](ASSERTION_METADATA_FIX.md)。

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/assertion.go` | 修改 | switch 重构 + 新增 7 个断言函数 + `errorOrOK` 辅助函数 |
| `internal/agent/assertion_test.go` | 修改 | 新增 19 个测试用例覆盖全部新增断言路径 |
| `internal/agent/cognitive_integration_test.go` | 新增 | 3 个集成测试验证 Observer 与扩展断言的端到端协同 |

## 测试

19 个新增单元测试 + 3 个集成测试：

**单元测试**（每种工具类型的成功/失败/边界场景）：
- `TestGenerateAssertions_FileRead_Success/Error/EmptyContent`
- `TestGenerateAssertions_BrowserSearch_Success/Empty/Error`
- `TestGenerateAssertions_BrowserExtract_Success/EmptyContent`
- `TestGenerateAssertions_MCP_Success/ToolError/ResponseError`
- `TestGenerateAssertions_Skill_Success/Error`
- `TestGenerateAssertions_ReadSkill`
- `TestGenerateAssertions_Memory_Success/Error`
- `TestGenerateAssertions_Generic_WithError/NoError`

**集成测试**：
- `TestCognitiveIntegration_ObserverAssertionPipeline` — 7 种工具全部通过时的覆盖验证
- `TestCognitiveIntegration_MixedSuccessFailure` — 混合成功/失败场景的 FailureContext 生成
- `TestCognitiveIntegration_AssertionPassRate` — 断言通过率计算验证（实测 62.5%）
