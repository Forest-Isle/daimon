# Structured Output & Tool Choice Forcing

**日期**: 2026-05-15
**范围**: 为 CompletionRequest 新增 `ToolChoice` 和 `ResponseFormat` 字段，Claude 和 OpenAI provider 均支持。

## 概述

结构化输出和工具选择强制是现代 LLM agent 的基础能力，直接影响可靠性和成本：

- **Tool Choice** 控制 LLM 是否/如何调用工具：`auto`（默认）、`any`（强制调用工具）、`none`（纯文本）、或指定具体工具名
- **Response Format** 约束 LLM 输出格式：`json_object`（合法 JSON）、`json_schema`（符合特定 schema 的 JSON）

此前 IronClaw 的 CompletionRequest 仅支持基本的 system/messages/tools 参数，无法精确控制 LLM 行为。本次改动为 provider 层添加完整支持。

## 架构

### 新增类型 (`provider.go`)

```go
// CompletionRequest 新增字段
type CompletionRequest struct {
    // ... existing fields ...
    ToolChoice     string          `json:"tool_choice,omitempty"`
    ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type ResponseFormat struct {
    Type       string      `json:"type"`        // "json_object" | "json_schema"
    JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

type JSONSchema struct {
    Name   string `json:"name"`    // schema 名称
    Schema any    `json:"schema"`  // JSON Schema 对象
    Strict bool   `json:"strict,omitempty"`
}
```

### Provider 实现

#### Anthropic/Claude (`stream.go`)

**Tool Choice 映射**：

| ToolChoice 值 | Anthropic API 参数 |
|--------------|-------------------|
| `""` (空) | 不设 tool_choice（默认 auto） |
| `"any"` | `tool_choice: {type: "any"}` |
| `"none"` | 移除 tools 数组 |
| `"tool_name"` | `tool_choice: {type: "tool", name: "tool_name"}` |

**Response Format 映射**：

Anthropic 的结构化输出通过 `output_config` 暴露（非顶层 `response_format`）：

```go
if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
    msg.Request.OutputConfig = &anthropic.OutputConfig{
        Format: &anthropic.OutputFormat{
            Type: "json_schema",
            JSONSchema: &anthropic.JSONSchema{
                Name:   req.ResponseFormat.JSONSchema.Name,
                Schema: req.ResponseFormat.JSONSchema.Schema,
            },
        },
    }
}
```

`json_object` 类型在 Anthropic API 中通过 system prompt 指令实现（`"You must respond with valid JSON only."`）。

#### OpenAI (`openai.go`)

**Tool Choice 映射**：

| ToolChoice 值 | OpenAI API 参数 |
|--------------|----------------|
| `""` (空) | 不设 tool_choice（默认 auto） |
| `"any"` | `tool_choice: "required"` |
| `"none"` | `tool_choice: "none"` + 移除 tools |
| `"tool_name"` | `tool_choice: {type: "function", function: {name: "tool_name"}}` |

**Response Format 映射**：

```go
switch req.ResponseFormat.Type {
case "json_object":
    body["response_format"] = map[string]string{"type": "json_object"}
case "json_schema":
    body["response_format"] = map[string]any{
        "type": "json_schema",
        "json_schema": map[string]any{
            "name":   req.ResponseFormat.JSONSchema.Name,
            "schema": req.ResponseFormat.JSONSchema.Schema,
            "strict": req.ResponseFormat.JSONSchema.Strict,
        },
    }
}
```

### 安全防护

- `ToolChoice == "none"` 时，tools 数组被完全移除——确保 LLM 无法调用工具
- `json_schema` 但未提供 schema 时，`Execute` 方法在访问前检查 nil 指针
- 空字符串（默认）保持完全向后兼容——不发送任何额外参数

## 使用场景

| 场景 | ToolChoice | ResponseFormat |
|------|-----------|----------------|
| 自由对话 | `""` (默认) | nil |
| 强制使用工具 | `"any"` | nil |
| 纯文本分析 | `"none"` | `json_object` |
| 结构化数据提取 | `"any"` | `json_schema` |
| 强制特定工具 | `"file_read"` | nil |

## 文件

| 文件 | 改动 |
|------|------|
| `internal/agent/provider.go` | +ToolChoice +ResponseFormat +JSONSchema 类型 |
| `internal/agent/stream.go` | Anthropic tool_choice + output_config 映射 |
| `internal/agent/openai.go` | OpenAI tool_choice + response_format 映射 |
