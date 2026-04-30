package agent

import (
	"context"
	"strings"
)

// NodeFunc adapts a function to the GraphNode interface.
type NodeFunc func(ctx context.Context, state GraphState) (NodeResult, error)

// FuncNode is a reusable GraphNode adapter.
type FuncNode struct {
	nodeType NodeType
	fn       NodeFunc
}

// NewFuncNode creates a function-backed graph node.
func NewFuncNode(nt NodeType, fn NodeFunc) *FuncNode {
	return &FuncNode{
		nodeType: nt,
		fn:       fn,
	}
}

// Execute runs the adapted node function.
func (n *FuncNode) Execute(ctx context.Context, state GraphState) (NodeResult, error) {
	if n == nil || n.fn == nil {
		return NodeResult{ShouldTerminate: false}, nil
	}

	return n.fn(ctx, state)
}

// NodeType returns the node's declared type.
func (n *FuncNode) NodeType() NodeType {
	if n == nil {
		return ""
	}

	return n.nodeType
}

func NewPerceiveNode() *FuncNode {
	return NewFuncNode(NodePerceive, func(ctx context.Context, state GraphState) (NodeResult, error) {
		return NodeResult{ShouldTerminate: false}, nil
	})
}

func NewPlanNode() *FuncNode {
	return NewFuncNode(NodePlan, func(ctx context.Context, state GraphState) (NodeResult, error) {
		return NodeResult{ShouldTerminate: false}, nil
	})
}

func NewActNode() *FuncNode {
	return NewFuncNode(NodeAct, func(ctx context.Context, state GraphState) (NodeResult, error) {
		return NodeResult{ShouldTerminate: false}, nil
	})
}

func NewObserveNode() *FuncNode {
	return NewFuncNode(NodeObserve, func(ctx context.Context, state GraphState) (NodeResult, error) {
		return NodeResult{ShouldTerminate: false}, nil
	})
}

func NewReflectNode() *FuncNode {
	return NewFuncNode(NodeReflect, func(ctx context.Context, state GraphState) (NodeResult, error) {
		return NodeResult{ShouldTerminate: false}, nil
	})
}

func NewReplanNode() *FuncNode {
	return NewFuncNode(NodeReplan, func(ctx context.Context, state GraphState) (NodeResult, error) {
		return NodeResult{ShouldTerminate: false}, nil
	})
}

// BuildDefaultGraph constructs the default execution graph and routing edges.
func BuildDefaultGraph() *ExecutionGraph {
	graph := &ExecutionGraph{}

	graph.Register(NewPerceiveNode())
	graph.Register(NewPlanNode())
	graph.Register(NewActNode())
	graph.Register(NewObserveNode())
	graph.Register(NewReflectNode())
	graph.Register(NewReplanNode())

	graph.AddEdge(NodePerceive, func(result NodeResult) NodeType {
		return NodePlan
	})
	graph.AddEdge(NodePlan, func(result NodeResult) NodeType {
		return NodeAct
	})
	graph.AddEdge(NodeAct, func(result NodeResult) NodeType {
		if !result.ShouldTerminate {
			return NodeObserve
		}
		return NodeTerminate
	})
	graph.AddEdge(NodeObserve, func(result NodeResult) NodeType {
		if hasFailureSignal(result.Output) {
			return NodeReflect
		}
		return ""
	})
	graph.AddEdge(NodeObserve, func(result NodeResult) NodeType {
		return NodeAct
	})
	graph.AddEdge(NodeReflect, func(result NodeResult) NodeType {
		if needsReplanSignal(result.Output) {
			return NodeReplan
		}
		return ""
	})
	graph.AddEdge(NodeReflect, func(result NodeResult) NodeType {
		return NodeTerminate
	})
	graph.AddEdge(NodeReplan, func(result NodeResult) NodeType {
		return NodePlan
	})

	return graph
}

func hasFailureSignal(output string) bool {
	lower := strings.ToLower(output)
	signals := []string{"failure", "failures", "error", "denied"}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	return false
}

func needsReplanSignal(output string) bool {
	lower := strings.ToLower(output)
	signals := []string{"replan", "needs_replan", "need replan", "adjust"}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	return false
}
