# 断言系统 Metadata 桥接与输出格式修正

**日期**: 2026-04-19
**范围**: 修复断言系统与实际工具输出格式的不匹配问题，引入 Observation Metadata 传递通道

## 概述

此前的断言系统存在一个严重的生产环境 Bug：多个断言函数对工具输出的期望格式与实际输出格式不一致，导致断言结果不准确。具体表现为：

- **HTTP 断言在生产环境中始终失败**：`httpAssertions` 期望 JSON `{"status_code":200,"body":"ok"}`，但 HTTP 工具实际输出纯文本 `"HTTP 200 OK\n\nbody"`
- **浏览器搜索断言静默跳过**：`browserSearchAssertions` 期望 `{"results":[...]}` 对象，但工具实际输出裸 JSON 数组
- **浏览器提取断言静默跳过**：`browserExtractAssertions` 期望 `{"content":"..."}` JSON，但工具实际输出原始 Markdown
- **MCP 断言部分失效**：`mcpAssertions` 期望 `{"error":"...","result":...}` JSON 信封，但 MCP 适配器输出纯文本

测试能通过是因为测试用例也使用了相同的错误格式——断言代码和测试代码"错得一致"。

本次改动通过两个机制解决该问题：(1) 在 `Observation` 中引入 `Metadata` 字段传递结构化元数据；(2) 重写所有受影响的断言函数以匹配实际工具输出格式。

## Part A: Observation Metadata 通道

### 问题

`tool.Result` 包含 `Metadata map[string]any`（如 `status_code`、`result_count`、`content_type`），但 `Observation` 结构体没有对应字段，断言函数只能解析 `Output` 字符串。

### 解决方案

```
tool.Result                    Observation                   Assertion
──────────                     ───────────                   ─────────
Metadata: {                    Metadata: {                   metadataInt(obs.Metadata, "status_code")
  "status_code": 200,    ──►     "status_code": 200,   ──►   → 200 ✓
  "content_type": "..."         "content_type": "..."
}                              }
```

在 `cognitive_types.go` 中为 `Observation` 新增 `Metadata map[string]any` 字段，在 `act.go` 的 `executeSubTask` 中将 `result.Metadata` 传递给 `obs.Metadata`。

### 新增辅助函数

```go
func metadataInt(meta map[string]any, key string) (int, bool)
```

从 Metadata 中安全提取整数值，兼容 `int`、`int64`、`float64`（JSON 反序列化的数字类型）。

## Part B: 断言函数修正

### HTTP 断言（关键修复）

**修复前**：尝试 JSON 解析 `{"status_code":200}` → 解析失败 → 返回 `"output is valid JSON": false` → **所有 HTTP 调用被标记为失败**

**修复后**：三层降级策略
1. **Metadata 优先**：`metadataInt(obs.Metadata, "status_code")` — 最可靠
2. **纯文本解析**：`fmt.Sscanf(obs.Output, "HTTP %d ", &statusCode)` — 无 Metadata 时
3. **Legacy JSON**：`json.Unmarshal` 旧格式 — 向后兼容

当三种方式都无法提取 status_code 时，降级为基础错误检查 `"HTTP response received"`。

### 浏览器搜索断言

**修复前**：期望 `{"results":[...]}` 对象格式 → `json.Unmarshal` 对裸数组失败 → 跳过 "search returned results" 检查

**修复后**：三层降级
1. **Metadata**：`metadataInt(obs.Metadata, "result_count")`
2. **裸数组**：`json.Unmarshal` 为 `[]json.RawMessage`
3. **Legacy 对象**：`{"results":[...]}` 格式

### 浏览器提取断言

**修复前**：期望 `{"content":"...","error":""}` JSON → 解析原始 Markdown 失败 → 跳过内容检查

**修复后**：直接检查 `strings.TrimSpace(obs.Output)` 是否非空。工具输出就是 Markdown 内容本身。

### MCP 断言

**修复前**：仅检查 `obs.Error` + 尝试解析 JSON error 字段

**修复后**：保留 `obs.Error` 检查和 JSON error 检测，新增 `"MCP tool produced output"` 断言（`len(strings.TrimSpace(obs.Output)) > 0`），确保无输出的 MCP 调用也能被检测。

### 新增 file_list 断言

`file_list` 此前落入 `genericAssertions`（仅在有 error 时才生成断言）。新增专用 `fileListAssertions`：
- `"file list succeeded"` — 无 error
- `"listing is non-empty"` — 输出非空

## 断言覆盖对比

| 工具 | 修复前断言 | 修复后断言 | 生产环境行为变化 |
|------|-----------|-----------|----------------|
| `http` | ~~JSON 解析失败 → 始终 false~~ | Metadata/文本/JSON 三层降级 | **修复: 不再误报** |
| `browser_search` | ~~对象解析失败 → 跳过结果检查~~ | Metadata/数组/对象三层降级 | **修复: 结果检查生效** |
| `browser_extract` | ~~JSON 解析失败 → 跳过内容检查~~ | 直接检查 Markdown 内容 | **修复: 内容检查生效** |
| `mcp_*` | 仅 error + JSON error 字段 | + 非空输出检查 | **增强: 捕获空响应** |
| `file_list` | 仅在有 error 时生效 | 专用双断言 | **增强: 覆盖正常路径** |
| `bash` | ✓ 无变化 | ✓ 无变化 | — |
| `file_write/edit` | ✓ 无变化 | ✓ 无变化 | — |
| `file_read` | ✓ 无变化 | ✓ 无变化 | — |

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/cognitive_types.go` | 修改 | `Observation` 新增 `Metadata map[string]any` 字段 |
| `internal/agent/act.go` | 修改 | `executeSubTask` 传递 `result.Metadata` → `obs.Metadata` |
| `internal/agent/assertion.go` | 修改 | 重写 `httpAssertions`、`browserSearchAssertions`、`browserExtractAssertions`、`mcpAssertions`；新增 `fileListAssertions`、`metadataInt` |
| `internal/agent/assertion_test.go` | 修改 | 重写 HTTP/浏览器/MCP 测试使用真实输出格式；新增 10 个测试用例 |

## 测试

45 个测试全部通过（含 10 个新增）：

**HTTP（6 个，全部新增/重写）**：
- `TestGenerateAssertions_HTTP_Success_Metadata` — Metadata 路径
- `TestGenerateAssertions_HTTP_Success_PlainText` — 纯文本解析路径
- `TestGenerateAssertions_HTTP_ServerError_Metadata` — 500 via Metadata
- `TestGenerateAssertions_HTTP_ServerError_PlainText` — 500 via 纯文本
- `TestGenerateAssertions_HTTP_LegacyJSON` — Legacy JSON 向后兼容
- `TestGenerateAssertions_HTTP_UnparsableOutput` — 无法解析时降级

**浏览器搜索（4 个，重写）**：
- `TestGenerateAssertions_BrowserSearch_Success_Array` — 裸数组 + Metadata
- `TestGenerateAssertions_BrowserSearch_Success_Metadata` — 纯 Metadata
- `TestGenerateAssertions_BrowserSearch_LegacyFormat` — Legacy 对象格式
- `TestGenerateAssertions_BrowserSearch_Empty` — 空结果

**浏览器提取（3 个，重写）**：
- `TestGenerateAssertions_BrowserExtract_Success` — 原始 Markdown
- `TestGenerateAssertions_BrowserExtract_EmptyContent` — 空内容
- `TestGenerateAssertions_BrowserExtract_Error` — 提取失败

**MCP（4 个，重写/新增）**：
- `TestGenerateAssertions_MCP_Success_PlainText` — 纯文本 + 非空检查
- `TestGenerateAssertions_MCP_Success_EmptyOutput` — 空输出检测
- `TestGenerateAssertions_MCP_ResponseError` — JSON error 字段检测

**file_list（3 个，全部新增）**：
- `TestGenerateAssertions_FileList_Success/Empty/Error`
