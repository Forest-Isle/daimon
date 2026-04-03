package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// ClaudeProvider implements Provider using the Anthropic SDK.
type ClaudeProvider struct {
	client anthropic.Client
	model  string
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
	params := c.buildParams(req)
	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("claude complete: %w", err)
	}
	return c.parseResponse(resp), nil
}

func (c *ClaudeProvider) Stream(ctx context.Context, req CompletionRequest) (StreamIterator, error) {
	params := c.buildParams(req)
	stream := c.client.Messages.NewStreaming(ctx, params)
	return &claudeStreamIterator{stream: stream}, nil
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
	}

	if req.System != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.System},
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

// claudeStreamIterator wraps the Anthropic streaming response.
type claudeStreamIterator struct {
	stream *ssestream.Stream[anthropic.MessageStreamEventUnion]
	done   bool
	accum  anthropic.Message
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
		case anthropic.MessageStopEvent:
			it.done = true
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
			return delta, nil
		}
	}

	if err := it.stream.Err(); err != nil {
		return StreamDelta{}, err
	}

	// Stream ended — parse accumulated message for tool calls
	it.done = true
	resp := parseStreamedMessage(&it.accum)
	delta := StreamDelta{Done: true, StopReason: resp.StopReason, ToolCalls: resp.ToolCalls}
	if len(resp.ToolCalls) > 0 {
		delta.ToolCall = &resp.ToolCalls[0]
	}
	return delta, nil
}

func (it *claudeStreamIterator) Close() {
	if it.stream != nil {
		it.stream.Close()
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
