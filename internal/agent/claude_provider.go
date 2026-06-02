package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/observability"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// APIPromptCacheStats tracks Anthropic API-level prompt caching metrics across calls.
type APIPromptCacheStats struct {
	CacheCreationTokens atomic.Int64
	CacheReadTokens     atomic.Int64
	InputTokens         atomic.Int64
	OutputTokens        atomic.Int64
}

// Snapshot returns a copy of the current cache stats.
func (s *APIPromptCacheStats) Snapshot() (creation, read int64) {
	return s.CacheCreationTokens.Load(), s.CacheReadTokens.Load()
}

// TokenSnapshot returns cumulative input/output token counts.
func (s *APIPromptCacheStats) TokenSnapshot() (input, output int64) {
	return s.InputTokens.Load(), s.OutputTokens.Load()
}

// ClaudeProvider implements Provider using the Anthropic SDK.
type ClaudeProvider struct {
	client     anthropic.Client
	model      string
	cacheStats APIPromptCacheStats
}

// supportsCaching returns true if the current model supports Anthropic prompt caching.
func (c *ClaudeProvider) supportsCaching() bool {
	// All Claude 3+ models on the Anthropic API support prompt caching.
	cachePrefixes := []string{
		"claude-3-", "claude-3.", "claude-sonnet-", "claude-opus-", "claude-haiku-",
	}
	lower := strings.ToLower(c.model)
	for _, prefix := range cachePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// GetCacheStats returns a snapshot of accumulated prompt cache metrics.
func (c *ClaudeProvider) GetCacheStats() (creation, read int64) {
	return c.cacheStats.Snapshot()
}

// GetTokenStats returns cumulative input/output token counts.
func (c *ClaudeProvider) GetTokenStats() (input, output int64) {
	return c.cacheStats.TokenSnapshot()
}

func NewClaudeProvider(apiKey, model, baseURL string) *ClaudeProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := anthropic.NewClient(opts...)
	return &ClaudeProvider{client: client, model: model}
}

func (c *ClaudeProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	ctx, span := observability.StartSpan(ctx, "llm.complete",
		observability.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider", "claude"),
			attribute.String("model", model),
		))
	start := time.Now()
	defer span.End()
	defer func() {
		observability.LLMRequestDuration.Record(ctx, time.Since(start).Milliseconds(),
			metric.WithAttributes(
				attribute.String("provider", "claude"),
				attribute.String("model", model),
				attribute.String("operation", "complete"),
			))
	}()

	params := c.buildParams(req)
	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("claude complete: %w", err)
	}

	// Track prompt cache token usage
	c.trackCacheUsage(resp.Usage)
	recordClaudeTokenMetrics(ctx, model, resp.Usage)

	return c.parseResponse(resp), nil
}

func (c *ClaudeProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	ctx, span := observability.StartSpan(ctx, "llm.complete",
		observability.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider", "claude"),
			attribute.String("model", model),
		))
	start := time.Now()
	params := c.buildParams(req)
	stream := c.client.Messages.NewStreaming(ctx, params)
	return &claudeStreamIterator{
		stream:    stream,
		provider:  c,
		ctx:       ctx,
		span:      span,
		start:     start,
		model:     model,
		finalized: false,
	}, nil
}

func (c *ClaudeProvider) buildParams(req CompletionRequest) anthropic.MessageNewParams {
	messages := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			if m.ToolUseID != "" {
				// Tool result message
				messages = append(messages, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(m.ToolUseID, m.Content, false),
				))
			} else {
				messages = append(messages, anthropic.NewUserMessage(
					anthropic.NewTextBlock(m.Content),
				))
			}
		case "assistant":
			blocks := make([]anthropic.ContentBlockParamUnion, 0)
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolBlocks {
				var input any
				_ = json.Unmarshal([]byte(tc.Input), &input)
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			messages = append(messages, anthropic.NewAssistantMessage(blocks...))
		}
	}

	tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
	if req.ToolChoice != "none" {
		for _, t := range req.Tools {
			schema := anthropic.ToolInputSchemaParam{
				Properties: t.InputSchema["properties"],
			}
			if req, ok := t.InputSchema["required"].([]string); ok {
				schema.Required = req
			}
			tools = append(tools, anthropic.ToolUnionParam{
				OfTool: &anthropic.ToolParam{
					Name:        t.Name,
					Description: anthropic.String(t.Description),
					InputSchema: schema,
				},
			})
		}
	}

	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 8192
	}

	model := req.Model
	if model == "" {
		model = c.model
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  messages,
	}

	if len(tools) > 0 {
		params.Tools = tools
		// Mark the last tool definition with cache_control for prompt caching.
		// This ensures the entire tool definition block is cached.
		if c.supportsCaching() && len(params.Tools) > 0 {
			last := &params.Tools[len(params.Tools)-1]
			if last.OfTool != nil {
				last.OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
		}
	}

	switch req.ToolChoice {
	case "any":
		params.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfAny: &anthropic.ToolChoiceAnyParam{},
		}
	case "":
	case "none":
	default:
		params.ToolChoice = anthropic.ToolChoiceParamOfTool(req.ToolChoice)
	}

	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" && req.ResponseFormat.JSONSchema != nil {
		schema, ok := req.ResponseFormat.JSONSchema.Schema.(map[string]any)
		if ok {
			params.OutputConfig = anthropic.OutputConfigParam{
				Format: anthropic.JSONOutputFormatParam{
					Schema: schema,
				},
			}
		}
	}

	if req.System != "" {
		if c.supportsCaching() {
			if idx := strings.Index(req.System, dynamicContextMarker); idx >= 0 {
				staticPart := req.System[:idx]
				dynamicPart := req.System[idx+len(dynamicContextMarker):]
				staticBlock := anthropic.TextBlockParam{
					Text:         staticPart,
					CacheControl: anthropic.NewCacheControlEphemeralParam(),
				}
				params.System = []anthropic.TextBlockParam{staticBlock}
				if strings.TrimSpace(dynamicPart) != "" {
					params.System = append(params.System, anthropic.TextBlockParam{Text: dynamicPart})
				}
			} else {
				sysBlock := anthropic.TextBlockParam{
					Text:         req.System,
					CacheControl: anthropic.NewCacheControlEphemeralParam(),
				}
				params.System = []anthropic.TextBlockParam{sysBlock}
			}
		} else {
			params.System = []anthropic.TextBlockParam{{Text: req.System}}
		}
	}

	return params
}

func (c *ClaudeProvider) parseResponse(resp *anthropic.Message) *CompletionResponse {
	result := &CompletionResponse{
		StopReason: StopReason(resp.StopReason),
	}

	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			result.Text += v.Text
		case anthropic.ToolUseBlock:
			inputBytes, _ := json.Marshal(v.Input)
			result.ToolCalls = append(result.ToolCalls, ToolUseBlock{
				ID:    v.ID,
				Name:  v.Name,
				Input: string(inputBytes),
			})
		}
	}

	return result
}

// PendingToolBlock represents a tool_use block whose streaming is complete
// (name + input finalized) but the overall model response is still generating.
// Used by speculative execution to launch read-only tools early.
type PendingToolBlock struct {
	ToolUseID string
	ToolName  string
	Input     string
}

// claudeStreamIterator wraps the Anthropic streaming response.
type claudeStreamIterator struct {
	stream            *ssestream.Stream[anthropic.MessageStreamEventUnion]
	done              bool
	accum             anthropic.Message
	provider          *ClaudeProvider // back-reference for tracking cache usage
	pendingToolBlocks []PendingToolBlock
	ctx               context.Context
	span              trace.Span
	start             time.Time
	model             string
	finalized         bool
}

func (it *claudeStreamIterator) Next() (StreamDelta, error) {
	if it.done {
		return StreamDelta{Done: true}, nil
	}

	for it.stream.Next() {
		event := it.stream.Current()
		_ = it.accum.Accumulate(event)

		switch e := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				return StreamDelta{Text: d.Text}, nil
			}
		case anthropic.ContentBlockStopEvent:
			if int(e.Index) < len(it.accum.Content) {
				block := it.accum.Content[e.Index]
				if v, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
					inputBytes, _ := json.Marshal(v.Input)
					it.pendingToolBlocks = append(it.pendingToolBlocks, PendingToolBlock{
						ToolUseID: v.ID,
						ToolName:  v.Name,
						Input:     string(inputBytes),
					})
				}
			}
		case anthropic.MessageStopEvent:
			it.done = true
			// Track cache usage from the accumulated message
			if it.provider != nil {
				it.provider.trackCacheUsage(it.accum.Usage)
			}
			recordClaudeTokenMetrics(it.ctx, it.model, it.accum.Usage)
			resp := parseStreamedMessage(&it.accum)
			delta := StreamDelta{
				Done:       true,
				StopReason: resp.StopReason,
				ToolCalls:  resp.ToolCalls,
			}
			// Keep backward compat: set ToolCall to first if present
			if len(resp.ToolCalls) > 0 {
				delta.ToolCall = &resp.ToolCalls[0]
			}
			it.finish(nil)
			return delta, nil
		}
	}

	if err := it.stream.Err(); err != nil {
		it.done = true
		it.finish(err)
		return StreamDelta{}, fmt.Errorf("claude stream: %w", err)
	}

	// Stream ended — parse accumulated message for tool calls
	it.done = true
	if it.provider != nil {
		it.provider.trackCacheUsage(it.accum.Usage)
	}
	recordClaudeTokenMetrics(it.ctx, it.model, it.accum.Usage)
	resp := parseStreamedMessage(&it.accum)
	delta := StreamDelta{Done: true, StopReason: resp.StopReason, ToolCalls: resp.ToolCalls}
	if len(resp.ToolCalls) > 0 {
		delta.ToolCall = &resp.ToolCalls[0]
	}
	it.finish(nil)
	return delta, nil
}

// PendingToolBlocks returns tool_use blocks that completed during streaming
// and clears the internal buffer. Callers (e.g. speculative executor) can
// start these tools before the model finishes its full response.
func (it *claudeStreamIterator) PendingToolBlocks() []PendingToolBlock {
	blocks := it.pendingToolBlocks
	it.pendingToolBlocks = nil
	return blocks
}

func (it *claudeStreamIterator) Close() {
	if it.stream != nil {
		_ = it.stream.Close()
	}
	it.finish(nil)
}

func (it *claudeStreamIterator) finish(err error) {
	if it.finalized {
		return
	}
	it.finalized = true
	if err != nil && it.span != nil {
		it.span.RecordError(err)
		it.span.SetStatus(codes.Error, err.Error())
	}
	observability.LLMRequestDuration.Record(it.ctx, time.Since(it.start).Milliseconds(),
		metric.WithAttributes(
			attribute.String("provider", "claude"),
			attribute.String("model", it.model),
			attribute.String("operation", "stream"),
		))
	if it.span != nil {
		if it.accum.Usage.InputTokens > 0 || it.accum.Usage.OutputTokens > 0 {
			it.span.SetAttributes(
				attribute.Int("gen_ai.usage.input_tokens", int(it.accum.Usage.InputTokens)),
				attribute.Int("gen_ai.usage.output_tokens", int(it.accum.Usage.OutputTokens)),
			)
		}
		it.span.End()
	}
}

func parseStreamedMessage(msg *anthropic.Message) *CompletionResponse {
	result := &CompletionResponse{
		StopReason: StopReason(msg.StopReason),
	}
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			result.Text += v.Text
		case anthropic.ToolUseBlock:
			inputBytes, _ := json.Marshal(v.Input)
			result.ToolCalls = append(result.ToolCalls, ToolUseBlock{
				ID:    v.ID,
				Name:  v.Name,
				Input: string(inputBytes),
			})
		}
	}
	return result
}

// trackCacheUsage accumulates prompt cache and token usage metrics from an API response.
func (c *ClaudeProvider) trackCacheUsage(usage anthropic.Usage) {
	if usage.InputTokens > 0 {
		c.cacheStats.InputTokens.Add(usage.InputTokens)
	}
	if usage.OutputTokens > 0 {
		c.cacheStats.OutputTokens.Add(usage.OutputTokens)
	}
	if usage.CacheCreationInputTokens > 0 {
		c.cacheStats.CacheCreationTokens.Add(usage.CacheCreationInputTokens)
	}
	if usage.CacheReadInputTokens > 0 {
		c.cacheStats.CacheReadTokens.Add(usage.CacheReadInputTokens)
	}
	if usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 {
		slog.Debug("prompt cache usage",
			"cache_creation_tokens", usage.CacheCreationInputTokens,
			"cache_read_tokens", usage.CacheReadInputTokens,
			"input_tokens", usage.InputTokens,
		)
	}
}

func recordClaudeTokenMetrics(ctx context.Context, model string, usage anthropic.Usage) {
	if usage.InputTokens > 0 {
		observability.LLMTokensTotal.Add(ctx, usage.InputTokens,
			metric.WithAttributes(
				attribute.String("provider", "claude"),
				attribute.String("model", model),
				attribute.String("token_type", "input"),
			))
	}
	if usage.OutputTokens > 0 {
		observability.LLMTokensTotal.Add(ctx, usage.OutputTokens,
			metric.WithAttributes(
				attribute.String("provider", "claude"),
				attribute.String("model", model),
				attribute.String("token_type", "output"),
			))
	}
}
