package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	ierrors "github.com/Forest-Isle/IronClaw/internal/errors"
	"github.com/Forest-Isle/IronClaw/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const defaultOpenAIURL = "https://api.openai.com/v1"

// OpenAIProvider implements Provider for any OpenAI-compatible chat completions
// API (OpenAI, Ollama, vLLM, LiteLLM, OpenRouter, etc.).
// Uses only net/http — no external SDK dependency.
type OpenAIProvider struct {
	apiKey       string
	model        string
	baseURL      string
	client       *http.Client
	cacheMetrics *CacheMetrics
}

// NewOpenAIProvider creates a provider targeting an OpenAI-compatible endpoint.
// baseURL defaults to "https://api.openai.com/v1" if empty.
func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = defaultOpenAIURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAIProvider{
		apiKey:       apiKey,
		model:        model,
		baseURL:      baseURL,
		client:       &http.Client{},
		cacheMetrics: NewCacheMetrics(100),
	}
}

// ── OpenAI request/response types ──

type oaiRequest struct {
	Model          string             `json:"model"`
	Messages       []oaiMessage       `json:"messages"`
	Tools          []oaiTool          `json:"tools,omitempty"`
	ToolChoice     any                `json:"tool_choice,omitempty"`
	ResponseFormat *oaiResponseFormat `json:"response_format,omitempty"`
	MaxTokens      int                `json:"max_tokens,omitempty"`
	Stream         bool               `json:"stream,omitempty"`
	StreamOptions  *oaiStreamOptions  `json:"stream_options,omitempty"`
	Temperature    *float64           `json:"temperature,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiToolCallFunc `json:"function"`
}

type oaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiResponseFormat struct {
	Type       string         `json:"type"`
	JSONSchema *oaiJSONSchema `json:"json_schema,omitempty"`
}

type oaiJSONSchema struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict bool   `json:"strict,omitempty"`
}

type oaiUsage struct {
	PromptTokens        int                     `json:"prompt_tokens"`
	CompletionTokens    int                     `json:"completion_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	PromptTokensDetails *oaiPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

type oaiPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage,omitempty"`
	Error   *oaiError   `json:"error,omitempty"`
}

type oaiChoice struct {
	Message      oaiMessage `json:"message"`
	Delta        oaiMessage `json:"delta"`
	FinishReason string     `json:"finish_reason"`
}

type oaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// ── Provider interface implementation ──

func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	ctx, span := observability.StartSpan(ctx, "llm.complete",
		observability.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider", "openai"),
			attribute.String("model", model),
		))
	start := time.Now()
	defer span.End()
	defer func() {
		observability.LLMRequestDuration.Record(ctx, time.Since(start).Milliseconds(),
			metric.WithAttributes(
				attribute.String("provider", "openai"),
				attribute.String("model", model),
				attribute.String("operation", "complete"),
			))
	}()

	oaiReq := p.buildRequest(req, false)

	body, err := p.doRequest(ctx, oaiReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	defer func() { _ = body.Close() }()

	var resp oaiResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	if resp.Error != nil {
		apiErr := ierrors.Wrap(fmt.Errorf("%s", resp.Error.Message), ierrors.KindUnavailable, "openai: API error")
		span.RecordError(apiErr)
		span.SetStatus(codes.Error, apiErr.Error())
		return nil, apiErr
	}
	if len(resp.Choices) == 0 {
		noChoicesErr := fmt.Errorf("openai: no choices in response")
		span.RecordError(noChoicesErr)
		span.SetStatus(codes.Error, noChoicesErr.Error())
		return nil, fmt.Errorf("openai: no choices in response")
	}

	p.trackUsage(&resp)
	recordOpenAITokenMetrics(ctx, model, resp.Usage)
	if resp.Usage != nil {
		span.SetAttributes(
			attribute.Int("gen_ai.usage.input_tokens", resp.Usage.PromptTokens),
			attribute.Int("gen_ai.usage.output_tokens", resp.Usage.CompletionTokens),
		)
	}
	return p.parseChoice(resp.Choices[0]), nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	ctx, span := observability.StartSpan(ctx, "llm.complete",
		observability.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("provider", "openai"),
			attribute.String("model", model),
		))
	start := time.Now()
	oaiReq := p.buildRequest(req, true)

	body, err := p.doRequest(ctx, oaiReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		observability.LLMRequestDuration.Record(ctx, time.Since(start).Milliseconds(),
			metric.WithAttributes(
				attribute.String("provider", "openai"),
				attribute.String("model", model),
				attribute.String("operation", "stream"),
			))
		span.End()
		return nil, err
	}

	return &openaiStreamIterator{
		reader:   bufio.NewReader(body),
		body:     body,
		provider: p,
		ctx:      ctx,
		span:     span,
		start:    start,
		model:    model,
	}, nil
}

// ── Request building ──

func (p *OpenAIProvider) buildRequest(req CompletionRequest, stream bool) oaiRequest {
	oai := oaiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    stream,
	}
	if stream {
		oai.StreamOptions = &oaiStreamOptions{IncludeUsage: true}
	}
	if oai.Model == "" {
		oai.Model = p.model
	}

	// System message
	if req.System != "" {
		oai.Messages = append(oai.Messages, oaiMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	// Conversation messages
	for _, msg := range req.Messages {
		switch msg.Role {
		case "user":
			oai.Messages = append(oai.Messages, oaiMessage{
				Role:    "user",
				Content: msg.Content,
			})
		case "assistant":
			am := oaiMessage{Role: "assistant"}
			if msg.Content != "" {
				am.Content = msg.Content
			}
			for _, tb := range msg.ToolBlocks {
				am.ToolCalls = append(am.ToolCalls, oaiToolCall{
					ID:   tb.ID,
					Type: "function",
					Function: oaiToolCallFunc{
						Name:      tb.Name,
						Arguments: tb.Input,
					},
				})
			}
			if am.Content != nil || len(am.ToolCalls) > 0 {
				oai.Messages = append(oai.Messages, am)
			}
		case "tool_result":
			oai.Messages = append(oai.Messages, oaiMessage{
				Role:       "tool",
				Content:    msg.Content,
				ToolCallID: msg.ToolUseID,
			})
		}
	}

	// Tools → OpenAI functions
	if req.ToolChoice != "none" {
		for _, t := range req.Tools {
			oai.Tools = append(oai.Tools, oaiTool{
				Type: "function",
				Function: oaiFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}

	switch req.ToolChoice {
	case "any":
		oai.ToolChoice = "required"
	case "none":
		oai.ToolChoice = "none"
	case "":
	default:
		oai.ToolChoice = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": req.ToolChoice,
			},
		}
	}

	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case "json_object":
			oai.ResponseFormat = &oaiResponseFormat{Type: "json_object"}
		case "json_schema":
			if req.ResponseFormat.JSONSchema == nil {
				break
			}
			oai.ResponseFormat = &oaiResponseFormat{
				Type: "json_schema",
				JSONSchema: &oaiJSONSchema{
					Name:   req.ResponseFormat.JSONSchema.Name,
					Schema: req.ResponseFormat.JSONSchema.Schema,
					Strict: req.ResponseFormat.JSONSchema.Strict,
				},
			}
		}
	}

	return oai
}

func (p *OpenAIProvider) doRequest(ctx context.Context, oaiReq oaiRequest) (io.ReadCloser, error) {
	payload, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		switch resp.StatusCode {
		case 429:
			return nil, ierrors.Wrap(err, ierrors.KindUnavailable, "openai: rate limited")
		case 400:
			return nil, ierrors.Wrap(err, ierrors.KindInvalidInput, "openai: bad request")
		default:
			return nil, ierrors.Wrap(err, ierrors.KindUnavailable, "openai: API error")
		}
	}

	return resp.Body, nil
}

func (p *OpenAIProvider) parseChoice(choice oaiChoice) *CompletionResponse {
	resp := &CompletionResponse{
		Text: contentString(choice.Message.Content),
	}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolUseBlock{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: tc.Function.Arguments,
		})
	}

	switch choice.FinishReason {
	case "tool_calls", "function_call":
		resp.StopReason = StopToolUse
	case "length":
		resp.StopReason = StopMaxToken
	default:
		resp.StopReason = StopEndTurn
	}

	return resp
}

// ── Streaming ──

type openaiStreamIterator struct {
	reader     *bufio.Reader
	body       io.ReadCloser
	mu         sync.Mutex
	toolCalls  map[int]*oaiToolCall // index → accumulated call
	textBuf    strings.Builder
	done       bool
	stopReason StopReason
	provider   *OpenAIProvider // back-reference for tracking cache usage
	ctx        context.Context
	span       trace.Span
	start      time.Time
	model      string
	finalized  bool
}

func (it *openaiStreamIterator) Next() (StreamDelta, error) {
	it.mu.Lock()
	defer it.mu.Unlock()

	if it.done {
		return StreamDelta{Done: true, StopReason: it.stopReason}, io.EOF
	}

	for {
		line, err := it.reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if err != nil {
			if err == io.EOF {
				it.done = true
				delta := it.buildFinalDelta()
				it.finish(nil)
				return delta, nil
			}
			it.finish(err)
			return StreamDelta{}, fmt.Errorf("openai stream: %w", err)
		}

		if line == "" || line == ":" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			it.done = true
			return it.buildFinalDelta(), nil
		}

		var chunk oaiResponse
		if jsonErr := json.Unmarshal([]byte(data), &chunk); jsonErr != nil {
			slog.Debug("openai stream: malformed JSON chunk", "err", jsonErr, "data_len", len(data))
			continue
		}
		if len(chunk.Choices) == 0 {
			// Usage-only chunk (final streaming chunk with stream_options.include_usage)
			if chunk.Usage != nil {
				if it.provider != nil {
					it.provider.trackUsage(&chunk)
				}
				recordOpenAITokenMetrics(it.ctx, it.model, chunk.Usage)
				if it.span != nil {
					it.span.SetAttributes(
						attribute.Int("gen_ai.usage.input_tokens", chunk.Usage.PromptTokens),
						attribute.Int("gen_ai.usage.output_tokens", chunk.Usage.CompletionTokens),
					)
				}
			}
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// Accumulate text
		text := contentString(delta.Content)
		if text != "" {
			it.textBuf.WriteString(text)
			return StreamDelta{Text: text}, nil
		}

		// Accumulate tool calls
		for _, tc := range delta.ToolCalls {
			idx := 0
			if tc.ID != "" {
				if it.toolCalls == nil {
					it.toolCalls = make(map[int]*oaiToolCall)
				}
				idx = len(it.toolCalls)
				it.toolCalls[idx] = &oaiToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: oaiToolCallFunc{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			} else if len(it.toolCalls) > 0 {
				idx = len(it.toolCalls) - 1
				if existing, ok := it.toolCalls[idx]; ok {
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}

		if choice.FinishReason != "" {
			switch choice.FinishReason {
			case "tool_calls", "function_call":
				it.stopReason = StopToolUse
			case "length":
				it.stopReason = StopMaxToken
			default:
				it.stopReason = StopEndTurn
			}
			it.done = true
			delta := it.buildFinalDelta()
			it.finish(nil)
			return delta, nil
		}
	}
}

func (it *openaiStreamIterator) buildFinalDelta() StreamDelta {
	d := StreamDelta{
		Done:       true,
		StopReason: it.stopReason,
	}

	var calls []ToolUseBlock
	for i := 0; i < len(it.toolCalls); i++ {
		tc := it.toolCalls[i]
		calls = append(calls, ToolUseBlock{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: tc.Function.Arguments,
		})
	}
	d.ToolCalls = calls
	if len(calls) > 0 {
		d.ToolCall = &calls[0]
	}
	if it.stopReason == "" {
		it.stopReason = StopEndTurn
	}
	d.StopReason = it.stopReason

	return d
}

func (it *openaiStreamIterator) Close() {
	if it.body != nil {
		_ = it.body.Close()
	}
	it.finish(nil)
}

func (it *openaiStreamIterator) finish(err error) {
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
			attribute.String("provider", "openai"),
			attribute.String("model", it.model),
			attribute.String("operation", "stream"),
		))
	if it.span != nil {
		it.span.End()
	}
}

// trackUsage records token and cache metrics from an OpenAI API response.
func (p *OpenAIProvider) trackUsage(resp *oaiResponse) {
	if resp.Usage == nil || p.cacheMetrics == nil {
		return
	}
	u := resp.Usage
	var cachedTokens int64
	if u.PromptTokensDetails != nil {
		cachedTokens = int64(u.PromptTokensDetails.CachedTokens)
	}
	p.cacheMetrics.Record(
		int64(u.PromptTokens),
		int64(u.CompletionTokens),
		cachedTokens,
		0, // OpenAI doesn't report cache creation separately
	)
}

func recordOpenAITokenMetrics(ctx context.Context, model string, usage *oaiUsage) {
	if usage == nil {
		return
	}
	if usage.PromptTokens > 0 {
		observability.LLMTokensTotal.Add(ctx, int64(usage.PromptTokens),
			metric.WithAttributes(
				attribute.String("provider", "openai"),
				attribute.String("model", model),
				attribute.String("token_type", "input"),
			))
	}
	if usage.CompletionTokens > 0 {
		observability.LLMTokensTotal.Add(ctx, int64(usage.CompletionTokens),
			metric.WithAttributes(
				attribute.String("provider", "openai"),
				attribute.String("model", model),
				attribute.String("token_type", "output"),
			))
	}
}

// GetCacheStats returns cumulative cache creation and read tokens.
func (p *OpenAIProvider) GetCacheStats() (creation, read int64) {
	if p.cacheMetrics == nil {
		return 0, 0
	}
	snap := p.cacheMetrics.Snapshot()
	return snap.TotalCacheCreationTokens, snap.TotalCacheReadTokens
}

// GetTokenStats returns cumulative input and output token counts.
func (p *OpenAIProvider) GetTokenStats() (input, output int64) {
	if p.cacheMetrics == nil {
		return 0, 0
	}
	snap := p.cacheMetrics.Snapshot()
	return snap.TotalInputTokens, snap.TotalOutputTokens
}

// CacheMetricsSnapshot returns a full snapshot of cache performance metrics.
func (p *OpenAIProvider) CacheMetricsSnapshot() CacheMetricsSnapshot {
	if p.cacheMetrics == nil {
		return CacheMetricsSnapshot{}
	}
	return p.cacheMetrics.Snapshot()
}

// contentString extracts a string from an `any` content field which may be
// a plain string or nil.
func contentString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
