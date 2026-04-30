package agent

import (
	"context"
	"time"
)

// NodeType identifies a node in the execution graph.
type NodeType string

const (
	NodePerceive   NodeType = "perceive"
	NodePlan       NodeType = "plan"
	NodeAct        NodeType = "act"
	NodeObserve    NodeType = "observe"
	NodeReflect    NodeType = "reflect"
	NodeReplan     NodeType = "replan"
	NodeSynthesize NodeType = "synthesize"
	NodeTerminate  NodeType = "terminate"
)

// ExecutionPath selects which execution loop a session should follow.
type ExecutionPath string

const (
	PathLightweight ExecutionPath = "lightweight"
	PathStandard    ExecutionPath = "standard"
	PathDeep        ExecutionPath = "deep"
)

// GraphEvent records a single node transition in the execution graph.
type GraphEvent struct {
	ID             string
	SessionID      string
	NodeType       NodeType
	InputSnapshot  string
	OutputSnapshot string
	TransitionedTo NodeType
	Timestamp      time.Time
	ExecutionPath  ExecutionPath
	Metadata       map[string]string
}

// GraphState is an immutable snapshot of graph execution state.
type GraphState struct {
	SessionID     string
	CurrentNode   NodeType
	ExecutionPath ExecutionPath
	Iteration     int
	Events        []GraphEvent
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NodeResult captures the output of a node execution.
type NodeResult struct {
	NextNode        NodeType
	Output          string
	ShouldTerminate bool
	SuggestedPath   ExecutionPath
	Error           error
}

// GraphNode executes a single step in the execution graph.
type GraphNode interface {
	Execute(ctx context.Context, state GraphState) (NodeResult, error)
	NodeType() NodeType
}

// EdgeCondition resolves the next node from a node result.
type EdgeCondition func(NodeResult) NodeType

// ExecutionGraph stores node registrations and routing conditions.
type ExecutionGraph struct {
	nodes map[NodeType]GraphNode
	edges map[NodeType][]EdgeCondition
}

// Register adds or replaces a node handler for its declared node type.
func (g *ExecutionGraph) Register(node GraphNode) {
	if g.nodes == nil {
		g.nodes = make(map[NodeType]GraphNode)
	}

	g.nodes[node.NodeType()] = node
}

// AddEdge appends a routing condition for a source node.
func (g *ExecutionGraph) AddEdge(from NodeType, cond EdgeCondition) {
	if g.edges == nil {
		g.edges = make(map[NodeType][]EdgeCondition)
	}

	g.edges[from] = append(g.edges[from], cond)
}

// Route returns the next node for a given result and source node.
func (g *ExecutionGraph) Route(result NodeResult, from NodeType) NodeType {
	if result.ShouldTerminate {
		return NodeTerminate
	}

	if result.NextNode != "" {
		return result.NextNode
	}

	for _, cond := range g.edges[from] {
		if cond == nil {
			continue
		}

		next := cond(result)
		if next != "" {
			return next
		}
	}

	return NodeTerminate
}
