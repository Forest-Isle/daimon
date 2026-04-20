package eval

// Dimension categorizes evaluation tasks into capability areas.
type Dimension string

const (
	DimTaskExecution Dimension = "task_execution"
	DimPlanning      Dimension = "planning"
	DimErrorRecovery Dimension = "error_recovery"
	DimToolSelection Dimension = "tool_selection"
	DimConversation  Dimension = "conversation"
	DimMemory        Dimension = "memory"
	DimKnowledge     Dimension = "knowledge"
	DimMultiAgent    Dimension = "multi_agent"
)

// AllDimensions returns the full list of recognized dimensions.
func AllDimensions() []Dimension {
	return []Dimension{
		DimTaskExecution, DimPlanning, DimErrorRecovery, DimToolSelection,
		DimConversation, DimMemory, DimKnowledge, DimMultiAgent,
	}
}

// DefaultDimension returns DimTaskExecution when dim is empty, otherwise dim.
func DefaultDimension(dim Dimension) Dimension {
	if dim == "" {
		return DimTaskExecution
	}
	return dim
}

// VerifyMethod determines how a task's output is verified.
type VerifyMethod string

const (
	VerifyDeterministic VerifyMethod = "deterministic"
	VerifyLLMJudge      VerifyMethod = "llm_judge"
	VerifyHybrid        VerifyMethod = "hybrid"
)
