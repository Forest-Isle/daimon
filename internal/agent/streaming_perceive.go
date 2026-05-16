package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// StreamingPerceiver wraps the existing Perceiver with streaming behavior.
type StreamingPerceiver struct {
	inner *Perceiver
}

func NewStreamingPerceiver(inner *Perceiver) *StreamingPerceiver {
	return &StreamingPerceiver{inner: inner}
}

// Stream runs PERCEIVE and sends context chunks as they become available.
func (sp *StreamingPerceiver) Stream(
	ctx context.Context,
	state *CognitiveState,
	out chan<- *ContextChunk,
) error {
	if state == nil {
		return fmt.Errorf("nil cognitive state")
	}

	type sourceData struct {
		name     string
		priority int
		content  string
	}

	var sources []sourceData
	if len(state.RelevantMemories) > 0 {
		var sb strings.Builder
		for _, m := range state.RelevantMemories {
			sb.WriteString("- ")
			sb.WriteString(m.Entry.Content)
			sb.WriteString("\n")
		}
		sources = append(sources, sourceData{name: "memory", priority: 10, content: sb.String()})
	}
	if len(state.KnowledgeContext) > 0 {
		sources = append(sources, sourceData{name: "knowledge", priority: 8, content: strings.Join(state.KnowledgeContext, "\n\n")})
	}
	if len(state.GraphContext) > 0 {
		sources = append(sources, sourceData{name: "graph", priority: 7, content: strings.Join(state.GraphContext, "\n")})
	}
	if state.ProjectCtx != nil && state.ProjectCtx.RawContent != "" {
		sources = append(sources, sourceData{name: "project", priority: 5, content: state.ProjectCtx.RawContent})
	}
	if state.GitState != nil && state.GitState.RawContent != "" {
		sources = append(sources, sourceData{name: "git", priority: 3, content: state.GitState.RawContent})
	}
	if state.UserProfile != "" {
		sources = append(sources, sourceData{name: "profile", priority: 2, content: state.UserProfile})
	}

	var wg sync.WaitGroup
	for _, src := range sources {
		src := src
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case out <- &ContextChunk{
				Source:   src.name,
				Content:  src.content,
				Priority: src.priority,
				IsLast:   true,
			}:
			}
		}()
	}

	wg.Wait()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case out <- &ContextChunk{Source: "perceive", IsLast: true}:
	}

	return nil
}
