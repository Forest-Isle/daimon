package agent

// ContextBudget defines per-complexity limits for context items injected into the cognitive state.
type ContextBudget struct {
	MemoryLimit           int
	KBLimit               int
	IncludeGraph          bool
	IncludeProjectContext bool
	IncludeGitState       bool
}

// ContextBudgetAllocator allocates context limits based on task complexity.
type ContextBudgetAllocator struct{}

// NewContextBudgetAllocator creates a new ContextBudgetAllocator.
func NewContextBudgetAllocator() *ContextBudgetAllocator {
	return &ContextBudgetAllocator{}
}

// Allocate returns the context budget for the given task complexity.
func (a *ContextBudgetAllocator) Allocate(complexity TaskComplexity) ContextBudget {
	switch complexity {
	case ComplexitySimple:
		return ContextBudget{
			MemoryLimit:           3,
			KBLimit:               0,
			IncludeGraph:          false,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	case ComplexityComplex:
		return ContextBudget{
			MemoryLimit:           10,
			KBLimit:               5,
			IncludeGraph:          true,
			IncludeProjectContext: true,
			IncludeGitState:       true,
		}
	default:
		return ContextBudget{
			MemoryLimit:           5,
			KBLimit:               3,
			IncludeGraph:          false,
			IncludeProjectContext: true,
			IncludeGitState:       false,
		}
	}
}

// Apply truncates and prunes the cognitive state according to the budget
// derived from state.Goal.Complexity.
func (a *ContextBudgetAllocator) Apply(state *CognitiveState) {
	budget := a.Allocate(state.Goal.Complexity)

	if len(state.RelevantMemories) > budget.MemoryLimit {
		state.RelevantMemories = state.RelevantMemories[:budget.MemoryLimit]
	}

	if len(state.KnowledgeContext) > budget.KBLimit {
		state.KnowledgeContext = state.KnowledgeContext[:budget.KBLimit]
	}

	if !budget.IncludeGraph {
		state.GraphContext = nil
	}

	if !budget.IncludeProjectContext {
		state.ProjectCtx = nil
	}

	if !budget.IncludeGitState {
		state.GitState = nil
	}
}
