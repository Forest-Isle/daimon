package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const defaultGraphMaxIterations = 50

// GraphEngine executes a dynamic node graph until it terminates.
type GraphEngine struct {
	graph         *ExecutionGraph
	eventStore    ExecutionEventStore
	maxIterations int
}

// NewGraphEngine creates a graph engine with default iteration limits.
func NewGraphEngine(graph *ExecutionGraph, eventStore ExecutionEventStore) *GraphEngine {
	return &GraphEngine{
		graph:         graph,
		eventStore:    eventStore,
		maxIterations: defaultGraphMaxIterations,
	}
}

// Run executes the graph from the state's current node until termination.
func (e *GraphEngine) Run(ctx context.Context, sessionID string, initialState GraphState) (GraphState, error) {
	if e == nil {
		return GraphState{}, fmt.Errorf("graph engine is nil")
	}
	if e.graph == nil {
		return GraphState{}, fmt.Errorf("execution graph is nil")
	}

	state := initialState
	now := time.Now()
	if state.SessionID == "" {
		state.SessionID = sessionID
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = now
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = now
	}
	if state.CurrentNode == "" {
		state.CurrentNode = NodeAct
	}

	limit := e.maxIterations
	if limit <= 0 {
		limit = defaultGraphMaxIterations
	}

	for state.CurrentNode != NodeTerminate {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		if state.Iteration >= limit {
			return state, fmt.Errorf("graph engine exceeded max iterations: %d", limit)
		}

		currentNode := state.CurrentNode
		node := e.graph.nodes[currentNode]
		if node == nil {
			return state, fmt.Errorf("node not registered: %s", currentNode)
		}

		result, err := node.Execute(ctx, state)
		if err != nil {
			return state, err
		}
		if result.Error != nil {
			return state, result.Error
		}

		if result.SuggestedPath != "" {
			state.ExecutionPath = result.SuggestedPath
		}

		nextNode := e.graph.Route(result, currentNode)
		event := e.newEvent(state.SessionID, currentNode, result, state.ExecutionPath, nextNode)

		if err := e.appendEvent(ctx, event); err != nil {
			return state, err
		}

		state.Events = append(state.Events, event)
		state.Iteration++
		state.CurrentNode = nextNode
		state.UpdatedAt = event.Timestamp

		if result.ShouldTerminate || nextNode == NodeTerminate {
			break
		}
	}

	return state, nil
}

func (e *GraphEngine) selectPath(input string) ExecutionPath {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) < 100 && !strings.Contains(trimmed, "\n") {
		return PathLightweight
	}

	lower := strings.ToLower(trimmed)
	keywords := []string{"plan", "analyze", "implement", "refactor"}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return PathDeep
		}
	}

	return PathStandard
}

func (e *GraphEngine) buildInitialState(sessionID string, input string) GraphState {
	path := e.selectPath(input)
	currentNode := NodeAct
	if path == PathDeep {
		currentNode = NodePerceive
	}

	now := time.Now()
	return GraphState{
		SessionID:     sessionID,
		CurrentNode:   currentNode,
		ExecutionPath: path,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func (e *GraphEngine) recordEvent(ctx context.Context, sessionID string, from NodeType, result NodeResult, path ExecutionPath) {
	if e == nil || e.eventStore == nil {
		return
	}

	nextNode := NodeTerminate
	if e.graph != nil {
		nextNode = e.graph.Route(result, from)
	}

	_ = e.eventStore.Append(ctx, e.newEvent(sessionID, from, result, path, nextNode))
}

func (e *GraphEngine) newEvent(sessionID string, from NodeType, result NodeResult, path ExecutionPath, nextNode NodeType) GraphEvent {
	timestamp := time.Now()
	return GraphEvent{
		ID:             fmt.Sprintf("%d", timestamp.UnixNano()),
		SessionID:      sessionID,
		NodeType:       from,
		InputSnapshot:  string(from),
		OutputSnapshot: result.Output,
		TransitionedTo: nextNode,
		Timestamp:      timestamp,
		ExecutionPath:  path,
	}
}

func (e *GraphEngine) appendEvent(ctx context.Context, event GraphEvent) error {
	if e.eventStore == nil {
		return nil
	}

	return e.eventStore.Append(ctx, event)
}
