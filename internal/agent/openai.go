package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

const defaultOpenAIURL = "https://api.openai.com/v1"

// OpenAIProvider implements Provider for any OpenAI-compatible chat completions
// API (OpenAI, Ollama, vLLM, LiteLLM, OpenRouter, etc.).
// Uses only net/http — no external SDK dependency.
type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
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
		client:  &http.Client{},
	}
}

// ── OpenAI request/response types ──

type oaiRequest struct {
	Model       string        `json:"model"`
	Messages    []oaiMessage  `json:"messages"`
	Tools       []oaiTool     `json:"tools,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
}

type oaiMessage struct {
	Role       string          `json:"role"`
	Content    any             `json:"content,omitempty"`
	ToolCalls  []oaiToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
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

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
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
		return nil, fmt.Errorf("openai: API error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}

	return p.parseChoice(resp.Choices[0]), nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	oaiReq := p.buildRequest(req, true)

	body, err := p.doRequest(ctx, oaiReq)
	if err != nil {
		return nil, err
	}

	return &openaiStreamIterator{
		reader: bufio.NewReader(body),
		body:   body,
	}, nil
}

// ── Request building ──

func (p *OpenAIProvider) buildRequest(req CompletionRequest, stream bool) oaiRequest {
	oai := oaiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    stream,
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
		return nil, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(body))
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
				return it.buildFinalDelta(), nil
			}
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
			continue
		}
		if len(chunk.Choices) == 0 {
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
			return it.buildFinalDelta(), nil
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
