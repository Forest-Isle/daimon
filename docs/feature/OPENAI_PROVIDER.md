# OpenAI 兼容 Provider

**日期**: 2026-04-19
**范围**: 新增 `OpenAIProvider`，支持任何 OpenAI-compatible Chat Completions API（OpenAI、Ollama、vLLM、LiteLLM、OpenRouter 等）

## 概述

此前 IronClaw 仅支持 Anthropic Claude 作为 LLM 后端。用户无法使用 GPT-4o、本地 Ollama 模型、或企业私有的 OpenAI-compatible 端点。

本次改动新增 `OpenAIProvider`，实现完整的 `agent.Provider` 接口（`Complete` + `Stream`），使用纯 `net/http` 实现——零外部 SDK 依赖，保持 IronClaw 的单二进制部署哲学。

## 设计决策

### 为什么不引入 OpenAI Go SDK？

| 方案 | 优点 | 缺点 |
|------|------|------|
| `sashabaranov/go-openai` SDK | 类型齐全，社区维护 | 新增外部依赖；版本锁定风险；SDK 类型需要二次转换 |
| `net/http` 裸实现 | 零依赖；完全控制请求/响应映射；易于适配非标准 API | 需要手写类型定义和 SSE 解析 |

选择 `net/http` 裸实现，原因：
1. **单二进制哲学**：IronClaw 追求最小依赖（CGO 仅因 SQLite 不可避免）
2. **兼容性优先**：需要兼容 Ollama、vLLM 等非标准实现，SDK 可能引入不必要的严格验证
3. **请求/响应已有抽象层**：`CompletionRequest` / `CompletionResponse` 已定义，映射逻辑不复杂

## 实现细节

### 请求映射

```
CompletionRequest                         oaiRequest
──────────────────                        ──────────
System: "..."           ──►  Messages[0]: {role: "system", content: "..."}
Messages[]:                  Messages[1..N]:
  {Role: "user"}        ──►    {role: "user", content: "..."}
  {Role: "assistant"}   ──►    {role: "assistant", content/tool_calls}
  {Role: "tool_result"} ──►    {role: "tool", tool_call_id: "..."}
Tools[]:                     Tools[]:
  {Name, Description,   ──►    {type: "function", function: {name, description,
   InputSchema}                   parameters}}
```

关键差异处理：
- **Claude `tool_result`** → **OpenAI `tool`**：角色名不同，OpenAI 使用 `tool` 而非 `tool_result`
- **工具调用**：Claude 用 `content` blocks 内嵌工具调用，OpenAI 用独立的 `tool_calls` 数组
- **停止原因**：Claude 的 `end_turn`/`tool_use`/`max_tokens` 映射到 OpenAI 的 `stop`/`tool_calls`/`length`

### 响应解析

```go
switch choice.FinishReason {
case "tool_calls", "function_call":  →  StopToolUse
case "length":                        →  StopMaxToken
default:                              →  StopEndTurn
}
```

同时处理 `tool_calls`（新 API）和 `function_call`（旧 API），确保对老版本端点的兼容。

### SSE 流式传输

OpenAI 的流式响应使用 Server-Sent Events (SSE) 协议。实现了 `openaiStreamIterator`：

```
HTTP 响应流 ──► bufio.Reader ──► 逐行读取 SSE
                                   │
                ┌──────────────────┼──────────────────┐
                ▼                  ▼                   ▼
         "data: [DONE]"    "data: {...}"         空行/注释
         → buildFinalDelta   → json.Unmarshal      → skip
                              → 累积 text/toolCalls
```

**工具调用流式累积**：OpenAI 会将工具调用参数分片发送。`openaiStreamIterator` 维护一个 `map[int]*oaiToolCall`，按 index 累积参数片段：

1. 首个 chunk 包含 `tc.ID`（创建新条目）
2. 后续 chunk 不含 ID（追加到最近条目的 Arguments）
3. `buildFinalDelta` 合并所有累积的工具调用

### 认证

```go
if p.apiKey != "" {
    httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
}
```

API Key 为空时跳过 Authorization 头，支持 Ollama 等无需认证的本地端点。

### 错误处理

```go
if resp.StatusCode >= 400 {
    body, _ := io.ReadAll(resp.Body)
    return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(body))
}
```

HTTP 层和 API 层错误分开处理：HTTP 4xx/5xx 立即返回完整响应体供调试；200 但包含 `error` 字段的响应单独处理。

## 配置

### ironclaw.yaml

```yaml
llm:
  provider: claude  # "claude" (默认) | "openai" | "openai-compatible"
  api_key: "${ANTHROPIC_API_KEY}"
  model: claude-sonnet-4-20250514
  max_tokens: 8192
```

### OpenAI 直连

```yaml
llm:
  provider: openai
  api_key: "${OPENAI_API_KEY}"
  model: gpt-4o
  max_tokens: 8192
```

### Ollama 本地模型

```yaml
llm:
  provider: openai-compatible
  api_key: ""                          # Ollama 不需要 API key
  base_url: "http://localhost:11434/v1"
  model: llama3.1
```

### vLLM / LiteLLM / OpenRouter 等

```yaml
llm:
  provider: openai-compatible
  api_key: "${YOUR_API_KEY}"
  base_url: "https://your-endpoint.example.com/v1"
  model: your-model-name
```

## Gateway 集成

`init_agent.go` 中通过 `cfg.LLM.Provider` 选择 Provider：

```go
switch gw.cfg.LLM.Provider {
case "openai", "openai-compatible":
    provider = agent.NewOpenAIProvider(gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL)
default:
    provider = agent.NewClaudeProvider(gw.cfg.LLM.APIKey, gw.cfg.LLM.Model, gw.cfg.LLM.BaseURL)
}
```

- `"openai"` 和 `"openai-compatible"` 使用同一个 Provider
- 未设置或设为 `"claude"` 时使用 ClaudeProvider（向后兼容）
- RetryProvider 包装层独立于具体 Provider，两种后端均可使用重试

## 涉及文件

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/agent/openai.go` | 新增 | OpenAIProvider 实现（408 行），含完整的 Complete + Stream + SSE 解析 |
| `internal/agent/openai_test.go` | 新增 | 8 个测试用例，使用 httptest 模拟 API |
| `internal/gateway/init_agent.go` | 修改 | Provider 选择 switch 语句 |
| `configs/ironclaw.example.yaml` | 修改 | 新增 provider 选项文档和 OpenAI/Ollama 示例 |

## 测试

8 个测试用例（全部使用 `httptest.NewServer` 模拟 API，不需要真实凭证）：

| 测试 | 覆盖点 |
|------|--------|
| `TestOpenAIProvider_Complete_TextResponse` | 文本补全 + Auth 头验证 + 模型传递 + 停止原因映射 |
| `TestOpenAIProvider_Complete_ToolCalls` | 工具调用响应解析 + StopToolUse 映射 |
| `TestOpenAIProvider_Complete_APIError` | HTTP 401 错误处理 |
| `TestOpenAIProvider_BuildRequest_MessageMapping` | 完整消息类型映射（system/user/assistant/tool_result） |
| `TestOpenAIProvider_Stream_TextOnly` | SSE 文本流解析 + 多 chunk 拼接 + [DONE] 处理 |
| `TestOpenAIProvider_Stream_ToolCalls` | SSE 工具调用流累积 + 参数分片合并 |
| `TestOpenAIProvider_DefaultBaseURL` | 空 baseURL 默认值 |
| `TestContentString` | contentString 辅助函数（nil/string/其他类型） |

## 兼容性矩阵

| 端点 | provider 值 | base_url | api_key | 备注 |
|------|------------|----------|---------|------|
| OpenAI | `openai` | (默认) | 必需 | 官方 API |
| Azure OpenAI | `openai-compatible` | Azure 端点 | 必需 | 需自行构造 base_url |
| Ollama | `openai-compatible` | `http://localhost:11434/v1` | 空 | 本地，零成本 |
| vLLM | `openai-compatible` | vLLM serve 地址 | 可选 | GPU 推理 |
| LiteLLM | `openai-compatible` | Proxy 地址 | 必需 | 多模型网关 |
| OpenRouter | `openai-compatible` | `https://openrouter.ai/api/v1` | 必需 | 模型聚合 |
