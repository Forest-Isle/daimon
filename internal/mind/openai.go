package mind

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

	ierrors "github.com/Forest-Isle/daimon/internal/errors"
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
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:       10,
				IdleConnTimeout:    90 * time.Second,
				DisableCompression: false,
			},
		},
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
	Index    *int            `json:"index,omitempty"`
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
	oaiReq := p.buildRequest(req, false)

	body, err := p.doRequest(ctx, oaiReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	var resp oaiResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, ierrors.Wrap(fmt.Errorf("%s", resp.Error.Message), ierrors.KindUnavailable, "openai: API error")
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}

	p.trackUsage(&resp)
	out := p.parseChoice(resp.Choices[0])
	out.Usage = usageFromOpenAI(resp.Usage)
	return out, nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	oaiReq := p.buildRequest(req, true)

	body, err := p.doRequest(ctx, oaiReq)
	if err != nil {
		return nil, err
	}

	return &openaiStreamIterator{
		reader:   bufio.NewReader(body),
		body:     body,
		provider: p,
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
			// A user message carrying a ToolUseID is a tool result in the
			// internal Anthropic-shaped convention emitted by BuildMessages
			// (context.go). OpenAI requires tool results as role:"tool" with a
			// tool_call_id; a plain user message here triggers HTTP 400 on the
			// next turn. This mirrors claude_provider.go's NewToolResultBlock path.
			if msg.ToolUseID != "" {
				oai.Messages = append(oai.Messages, oaiMessage{
					Role:       "tool",
					Content:    msg.Content,
					ToolCallID: msg.ToolUseID,
				})
			} else {
				oai.Messages = append(oai.Messages, oaiMessage{
					Role:    "user",
					Content: msg.Content,
				})
			}
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
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("HTTP %d: failed to read error body: %w", resp.StatusCode, readErr)
		}
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
	case "stop", "":
		resp.StopReason = StopEndTurn
	case "content_filter":
		resp.StopReason = StopAbnormal
	default:
		slog.Warn("openai: unrecognized finish_reason", "finish_reason", choice.FinishReason)
		resp.StopReason = StopAbnormal
	}

	return resp
}

// ── Streaming ──

type openaiStreamIterator struct {
	reader     *bufio.Reader
	body       io.ReadCloser
	mu         sync.Mutex
	toolCalls  map[int]*oaiToolCall // index → accumulated call
	maxToolIdx int                  // highest tool-call index seen, for id-less fallback
	textBuf    strings.Builder
	done       bool
	stopReason StopReason
	provider   *OpenAIProvider // back-reference for tracking cache usage
	finalized  bool
	// usage holds per-call token counts if a usage chunk (stream_options.include_usage)
	// is seen. OpenAI emits that chunk AFTER finish_reason, and the iterator finalizes
	// at finish_reason without draining further (so a stalled server cannot delay the
	// Done signal) — so streamed OpenAI usage is best-effort and typically zero here.
	// The non-streamed Complete path and the active Anthropic provider both report
	// usage fully; the cost ledger treats a zero Usage as "unknown", never as free.
	usage Usage
	// pendingDone defers the Done delta by one Next() call when finish_reason arrived
	// in the same chunk as content: the text is returned first, then the final delta
	// (carrying the recorded stop reason) on the next call.
	pendingDone bool
}

func (it *openaiStreamIterator) Next() (StreamDelta, error) {
	it.mu.Lock()
	defer it.mu.Unlock()

	if it.done {
		return StreamDelta{Done: true, StopReason: it.stopReason}, io.EOF
	}
	// A finish_reason shared a chunk with content: emit the deferred Done delta now.
	if it.pendingDone {
		it.pendingDone = false
		it.done = true
		delta := it.buildFinalDelta()
		it.finish(nil)
		return delta, nil
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
			// Usage-only chunk (stream_options.include_usage). OpenAI emits it AFTER
			// finish_reason, and the iterator finalizes at finish_reason without
			// draining, so this is reached only if a server sends usage before its
			// terminal chunk — best-effort capture, harmless when absent.
			if chunk.Usage != nil {
				it.usage = usageFromOpenAI(chunk.Usage)
				if it.provider != nil {
					it.provider.trackUsage(&chunk)
				}
			}
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// Record a terminal finish_reason FIRST, before any early return, so it is
		// never lost when a server packs finish_reason into the same chunk as content
		// or a tool-call fragment. The Done delta is emitted as soon as this chunk is
		// fully processed (below) — the iterator does NOT wait for the trailing
		// usage chunk, so a server that stalls after finish_reason cannot delay the
		// Done signal.
		finishing := choice.FinishReason != ""
		if finishing {
			it.stopReason = finishReasonToStop(choice.FinishReason)
		}

		// Accumulate text
		text := contentString(delta.Content)
		if text != "" {
			it.textBuf.WriteString(text)
			if finishing {
				// Emit the text now; the Done delta (carrying the stop reason) is
				// returned on the next call via the pendingDone fast-path.
				it.pendingDone = true
			}
			return StreamDelta{Text: text}, nil
		}

		// Accumulate tool calls, keyed by the OpenAI delta "index". Argument
		// fragments arrive in later chunks carrying only the index (no id/name),
		// so routing by index — not insertion order — is required for correct
		// reconstruction of parallel tool calls.
		for _, tc := range delta.ToolCalls {
			if it.toolCalls == nil {
				it.toolCalls = make(map[int]*oaiToolCall)
			}
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			} else if len(it.toolCalls) > 0 {
				// No index provided (older/non-conforming servers): fall back to
				// the most recently seen call so single-tool streams still work.
				idx = it.maxToolIdx
			}
			existing, ok := it.toolCalls[idx]
			if !ok {
				existing = &oaiToolCall{}
				it.toolCalls[idx] = existing
				if idx > it.maxToolIdx {
					it.maxToolIdx = idx
				}
			}
			if tc.ID != "" {
				existing.ID = tc.ID
			}
			if tc.Type != "" {
				existing.Type = tc.Type
			}
			if tc.Function.Name != "" {
				existing.Function.Name = tc.Function.Name
			}
			existing.Function.Arguments += tc.Function.Arguments
		}

		// A finish_reason chunk with no text (the common case: tool_calls finish, or
		// a separate empty stop chunk) finalizes here without draining further.
		if finishing {
			it.done = true
			fd := it.buildFinalDelta()
			it.finish(nil)
			return fd, nil
		}
	}
}

// finishReasonToStop maps an OpenAI finish_reason onto the neutral StopReason.
func finishReasonToStop(reason string) StopReason {
	switch reason {
	case "tool_calls", "function_call":
		return StopToolUse
	case "length":
		return StopMaxToken
	case "stop", "":
		return StopEndTurn
	case "content_filter":
		return StopAbnormal
	default:
		slog.Warn("openai stream: unrecognized finish_reason", "finish_reason", reason)
		return StopAbnormal
	}
}

func (it *openaiStreamIterator) buildFinalDelta() StreamDelta {
	d := StreamDelta{
		Done:       true,
		StopReason: it.stopReason,
		Usage:      it.usage,
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

func (it *openaiStreamIterator) finish(_ error) {
	if it.finalized {
		return
	}
	it.finalized = true
}

// usageFromOpenAI maps OpenAI per-response usage onto the provider-neutral Usage.
// OpenAI's prompt_tokens INCLUDES cached tokens, whereas Usage.InputTokens means
// fresh, full-rate input exclusive of cache reads (matching the Anthropic
// mapping). So cached tokens are split out: InputTokens = prompt − cached,
// CacheReadTokens = cached. OpenAI reports no separate cache-creation count.
// The cumulative trackUsage path below records the same exclusive split, so
// per-call and cumulative input accounting agree with each other and with the
// Anthropic provider (whose input_tokens already excludes cache reads).
func usageFromOpenAI(u *oaiUsage) Usage {
	if u == nil {
		return Usage{}
	}
	// Clamp against malformed OpenAI-compatible servers: prompt first (so a
	// negative prompt can't drag cached negative), then cached into [0, prompt]
	// so InputTokens (= prompt − cached) is never negative and cached never
	// exceeds total input; output must be nonnegative.
	prompt := u.PromptTokens
	if prompt < 0 {
		prompt = 0
	}
	cached := 0
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	}
	if cached < 0 {
		cached = 0
	}
	if cached > prompt {
		cached = prompt
	}
	output := u.CompletionTokens
	if output < 0 {
		output = 0
	}
	return Usage{
		InputTokens:     prompt - cached,
		OutputTokens:    output,
		CacheReadTokens: cached,
	}
}

// trackUsage records token and cache metrics from an OpenAI API response.
func (p *OpenAIProvider) trackUsage(resp *oaiResponse) {
	if resp.Usage == nil || p.cacheMetrics == nil {
		return
	}
	// Record fresh (uncached) input separately from cache reads, matching
	// CacheMetrics' contract (TotalInputTokens + TotalCacheReadTokens = total
	// input) and the Anthropic provider. OpenAI's prompt_tokens includes cached
	// tokens, so reuse the same exclusive split as the per-call mapping rather
	// than double-counting cache reads into input.
	usage := usageFromOpenAI(resp.Usage)
	p.cacheMetrics.Record(
		int64(usage.InputTokens),
		int64(usage.OutputTokens),
		int64(usage.CacheReadTokens),
		0, // OpenAI doesn't report cache creation separately
	)
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
