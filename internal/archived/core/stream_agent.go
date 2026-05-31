package core

import (
	"context"
	"fmt"
	"time"
)

// StreamResult holds the streaming agent's final outcome.
type StreamResult struct {
	Text       string
	StopReason StopReason
}

// StreamAgent wraps Agent with a streaming LLM path. Text chunks are
// emitted on the EventSink as EventLLMChunk during generation. Tool calls
// are buffered until the stream completes, then executed in the same
// batch/sequential logic as Agent.
//
// This is intentionally a separate type rather than a mode flag — the
// non-streaming Agent remains the simplest possible loop, useful for
// batch evaluation and testing.
type StreamAgent struct {
	a *Agent
}

// NewStreamAgent creates a streaming agent from an existing Agent.
func NewStreamAgent(a *Agent) *StreamAgent { return &StreamAgent{a: a} }

// Run executes the agentic loop with streaming LLM calls. EventLLMChunk
// events are emitted for every text delta. When the provider does not
// support streaming (Stream returns an error), the agent falls back to
// Complete and emits a single LLMResponse.
func (s *StreamAgent) Run(ctx context.Context, prompt string) (StreamResult, error) {
	if err := s.a.memory.Append(ctx, Message{Role: RoleUser, Content: prompt}); err != nil {
		return StreamResult{}, fmt.Errorf("memory.Append user: %w", err)
	}

	s.a.cfg.Sink.Emit(Event{Kind: EventStart, Time: time.Now(), Payload: map[string]any{"prompt": prompt}})
	s.a.cfg.Sink.Emit(Event{Kind: EventMessage, Time: time.Now(), Payload: Message{Role: RoleUser, Content: prompt}})

	var (
		lastText string
		stop     StopReason
	)
	for turn := 1; turn <= s.a.cfg.MaxTurns; turn++ {
		history, err := s.a.memory.Snapshot(ctx)
		if err != nil {
			return StreamResult{}, fmt.Errorf("memory.Snapshot: %w", err)
		}

		req := LLMRequest{
			Model:     s.a.cfg.Model,
			System:    s.a.cfg.System,
			Messages:  history,
			Tools:     s.a.tools.Schemas(),
			MaxTokens: s.a.cfg.MaxTokens,
		}

		s.a.cfg.Sink.Emit(Event{Kind: EventLLMRequest, Time: time.Now(), Turn: turn, Payload: req})

		// Try streaming first; fall back to Complete.
		resp, err := s.streamOrComplete(ctx, req, turn)
		if err != nil {
			return StreamResult{}, fmt.Errorf("provider: %w", err)
		}

		am := Message{Role: RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls}
		if err := s.a.memory.Append(ctx, am); err != nil {
			return StreamResult{}, fmt.Errorf("memory.Append assistant: %w", err)
		}
		s.a.cfg.Sink.Emit(Event{Kind: EventMessage, Time: time.Now(), Turn: turn, Payload: am})

		if resp.Text != "" {
			lastText = resp.Text
		}
		stop = resp.StopReason

		if len(resp.ToolCalls) == 0 || stop == StopEndTurn {
			s.a.cfg.Sink.Emit(Event{Kind: EventFinish, Time: time.Now(), Turn: turn, Payload: resp})
			return StreamResult{Text: lastText, StopReason: stop}, nil
		}

		results, err := s.a.runToolBatch(ctx, turn, resp.ToolCalls)
		if err != nil {
			return StreamResult{}, fmt.Errorf("tool batch: %w", err)
		}
		for _, r := range results {
			tm := Message{Role: RoleTool, ToolUseID: r.UseID, Content: toolResultPayload(r)}
			if err := s.a.memory.Append(ctx, tm); err != nil {
				return StreamResult{}, fmt.Errorf("memory.Append tool: %w", err)
			}
			s.a.cfg.Sink.Emit(Event{Kind: EventMessage, Time: time.Now(), Turn: turn, Payload: tm})
		}
	}

	s.a.cfg.Sink.Emit(Event{Kind: EventFinish, Time: time.Now(), Payload: "max_turns"})
	return StreamResult{Text: lastText, StopReason: StopMaxTurns}, nil
}

// streamOrComplete attempts to stream the request. If the provider's Stream
// returns an error, it falls back to Complete (which all providers support).
func (s *StreamAgent) streamOrComplete(ctx context.Context, req LLMRequest, turn int) (*LLMResponse, error) {
	stream, err := s.a.provider.Stream(ctx, req)
	if err != nil {
		// Fallback — emit the response as a single event.
		s.a.cfg.Sink.Emit(Event{
			Kind:    EventError,
			Time:    time.Now(),
			Turn:    turn,
			Payload: "stream not supported, falling back to Complete",
		})
		resp, compErr := s.a.provider.Complete(ctx, req)
		if compErr != nil {
			return nil, compErr
		}
		s.a.cfg.Sink.Emit(Event{Kind: EventLLMResponse, Time: time.Now(), Turn: turn, Payload: resp})
		return resp, nil
	}
	defer stream.Close()

	return s.consumeStream(ctx, stream, turn)
}

// consumeStream reads chunks from a Stream into an LLMResponse, emitting
// text deltas on the bus as they arrive.
func (s *StreamAgent) consumeStream(ctx context.Context, stream Stream, turn int) (*LLMResponse, error) {
	var (
		text      string
		toolCalls []ToolCall
		stop      StopReason
	)
	for {
		chunk, err := stream.Next(ctx)
		if err != nil {
			// Stream errors are non-fatal — return what we have.
			if text != "" || len(toolCalls) > 0 {
				return &LLMResponse{Text: text, ToolCalls: toolCalls, StopReason: stop}, nil
			}
			return nil, err
		}

		if chunk.Text != "" {
			text += chunk.Text
			s.a.cfg.Sink.Emit(Event{
				Kind:    EventLLMChunk,
				Time:    time.Now(),
				Turn:    turn,
				Payload: LLMChunk{Text: chunk.Text, Done: false},
			})
		}
		if chunk.ToolCall != nil {
			toolCalls = append(toolCalls, *chunk.ToolCall)
			s.a.cfg.Sink.Emit(Event{
				Kind:    EventToolRequest,
				Time:    time.Now(),
				Turn:    turn,
				Payload: chunk.ToolCall,
			})
		}
		if chunk.Done {
			stop = chunk.Stop
			break
		}
	}

	resp := &LLMResponse{Text: text, ToolCalls: toolCalls, StopReason: stop}
	s.a.cfg.Sink.Emit(Event{Kind: EventLLMResponse, Time: time.Now(), Turn: turn, Payload: resp})
	return resp, nil
}

// Ensure StreamAgent satisfies a nominal Runner interface.
var _ interface {
	Run(context.Context, string) (StreamResult, error)
} = (*StreamAgent)(nil)

