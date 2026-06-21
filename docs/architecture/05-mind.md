# 05 · mind — 模型层

> 包路径 `internal/mind` · 蓝图 §4.7

## 职责

可热插拔的认知引擎。这是宪法第 2 条「换脑无感」的物理边界——**模型 ID 只出现在 `mind` 包和配置里**，包外任何代码不依赖特定模型的行为特征。判据：换 Cognition 模型不触碰 mind 包外任何代码，回放评测回归数为零。

`mind` 由 `agent` 包拆出（provider/cognition 契约迁入），单向零环：`episode`/`attention` 只见 `Provider` 接口，不见 Claude/OpenAI 具体类型。

## 核心类型

### Provider — LLM 后端接口

```go
// internal/mind/provider.go
type Provider interface {
    Complete(ctx, req CompletionRequest) (*CompletionResponse, error)
    Stream(ctx, req CompletionRequest) (StreamIterator, error)
    Capabilities() Caps   // 能力协商，cheap & pure，可逐请求调
}
```

两个实现：

| 实现 | 文件 | 后端 |
|---|---|---|
| `ClaudeProvider` | `claude_provider.go` | Anthropic SDK，prompt 缓存（ephemeral `cache_control`）、extended thinking |
| `OpenAIProvider` | `openai.go` | 纯 HTTP，OpenAI-compatible（OpenAI / Ollama / vLLM 等），SSE 流解码 |

### 单一构造点

```go
func NewProviderFromConfig(llm config.LLMConfig) Provider {
    // provider == "openai" / "openai-compatible" → OpenAIProvider，否则 ClaudeProvider
    // retry.max_retries > 0 → 包 RetryProvider
}
```

`gateway` 运行时与离线工具（`daimon replay --against`）共享此构造点——加一个后端是一处改动。

### 请求 / 响应类型

```go
type CompletionRequest struct {
    Model, System  string
    Messages       []CompletionMessage
    Tools          []ToolDefinition
    MaxTokens      int
    ToolChoice     string          // "none" 用于 salvage JSON-only 提取
    ResponseFormat *ResponseFormat // {"type":"json_object"}
    ThinkingBudget int             // >0 启用 extended thinking，0 禁用（零行为变更）
}

type CompletionResponse struct {
    Text       string
    ToolCalls  []ToolUseBlock
    StopReason StopReason
    Thinking   string  // extended thinking 块
    Signature  string  // 必须逐字保留并在下次请求回放（API 校验签名）
    Usage      Usage
}

type StreamDelta struct {
    Text, Thinking, Signature string
    ToolCall  *ToolUseBlock   // 首个完成的 tool_use（兼容用）
    ToolCalls []ToolUseBlock  // 最终消息的全部 tool_use
    Done      bool
    StopReason StopReason
    Usage     Usage           // 仅最终 delta（Done）置位
}
```

### StopReason

```go
StopEndTurn  = "end_turn"
StopToolUse  = "tool_use"
StopMaxToken = "max_tokens"
StopAbnormal = "abnormal"  // 内容过滤或无法识别的 finish reason，不得当成功完成
```

### Usage — 成本核算（best-effort）

```go
type Usage struct { InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens int }
func (u *Usage) Add(other Usage)  // 累计多步情节的每次调用
```

零值意味着 provider 没报 usage（旧后端，或流式响应缺 usage 块）——调用方把 0 当"未知"而非"免费"。Usage 永不影响控制流，不准确或缺失只影响成本台账记什么，不改变代理做什么。

## 缓存协商（消灭魔法注释）

```go
type Caps struct {
    CacheBreakpoints int  // provider honor 的 caller-placed 缓存边界数
}
```

`Caps.CacheBreakpoints` 由 Provider 声明：
- Anthropic：1（honor ephemeral `cache_control` 块）。
- OpenAI：0（无 caller-placed 缓存，缓存自动或缺失）。

Composer 按声明放置边界。**0 时 composer 绝不插入缓存边界标记**，否则会作为字面文本泄漏进 prompt——这正是历史上 `<!-- CACHE_BOUNDARY -->` 魔法注释硬编码在 Anthropic 路径的问题，现已升级为 provider 能力协商。

## 重试与熔断

```go
type RetryProvider struct { inner Provider; cfg config.RetryConfig; cb *CircuitBreaker }
```

`retry.go` + `circuit_breaker.go`：

- **可重试分类**：429 / 500 / 502 / 503 / 529 可重试；400 / 401 / 403 / 404 不可。
- **指数退避**：BaseDelay → MaxDelay。
- **熔断器**：连续 N 次失败（默认阈值）→ Open，停发请求；openTimeout 后 HalfOpen 探测恢复。
- `Capabilities()` 透传 inner 的 caps。

## 影子脑（Shadow）

蓝图 §4.7 的换脑评测器：配置候选模型后，attention 判为 Cognize 的事件按采样率复制给影子，**影子用同一 Composer 组装、推理、行动全部 dry-run**（行动层短路为记录模式），周报对比真脑/影子的 Outcome 质量（replay 评分）与成本。

现状（as-built）：
- **action dry-run record-only** 已落（影子推理不产生副作用）。
- **影子周报每千 token 质量分** 已落——经 `replay --against` + `quality_per_1k_tok`，§4.7 验收 2/3 满足。
- LIVE 采样影子（增量 2）为可选项，非验收必需，需真 provider 验证；thinking 跨 provider 统一透传暂缓。

换脑 = 改配置一行 + 回放回归为零。详见 [11-replay.md](11-replay.md)。

## thinking 通道

`CompletionMessage` / `CompletionResponse` / `StreamDelta` 都带 `Thinking` + `Signature` 字段。Claude extended thinking 块可透传并随 assistant turn 逐字回放（API 校验签名）。`ThinkingBudget > 0` 启用，0 禁用（零行为变更）。

## 跨包接缝

- **← episode**：`streamCompletion` 调 `provider.Stream`；salvage 调 `provider.Complete`。
- **← attention**：`LLMModelRouter` 持 `Provider` 做分诊调用。
- **← gateway**：`completerAdapter` / `costRecorderAdapter` / `routeCostAdapter` 把 Provider 桥接到 sleep summarizer 与经济台账。
- **→ config**：`NewProviderFromConfig` 读 `LLMConfig`（provider/model/apiKey/baseURL/retry）。

## 设计取舍 / 不变量映射

| 设计 | 对应 | 为什么 |
|---|---|---|
| 模型 ID 只在 mind + config | 宪法 2「换脑无感」 | 换模型不触碰包外代码 |
| 能力协商替代硬编码 | 宪法 2 | 缓存正确性靠 provider 声明而非提示词魔法注释位置 |
| Usage 永不影响控制流 | 宪法 5（核算）| 成本不准只影响台账，不改代理行为 |
| 单一构造点 | 可维护 | 加后端一处改动；运行时与离线工具共享 |

下一篇：[06-world.md](06-world.md) — 世界模型。
