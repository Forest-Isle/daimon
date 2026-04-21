package eval

// EvolutionTasks returns eval tasks designed to exercise the evolution subsystem.
// They use different complexity levels to verify routing decisions and capture
// per-complexity skill synthesis patterns.
func EvolutionTasks() []TaskCase {
	return []TaskCase{
		{
			ID:         "evo-simple-routing",
			Goal:       "What is the capital of France?",
			Complexity: "simple",
			Tags:       []string{"evolution", "routing", "simple"},
		},
		{
			ID:         "evo-moderate-routing",
			Goal:       "Explain the differences between BFS and DFS graph traversal algorithms",
			Complexity: "moderate",
			Tags:       []string{"evolution", "routing", "moderate"},
		},
		{
			ID:         "evo-complex-routing",
			Goal:       "Design a system architecture for a distributed key-value store with eventual consistency",
			Complexity: "complex",
			Tags:       []string{"evolution", "routing", "complex"},
		},
		{
			ID:         "evo-skill-synthesis-trigger",
			Goal:       "Write a Go function that implements binary search on a sorted slice of integers",
			Complexity: "moderate",
			Tags:       []string{"evolution", "skill", "synthesis"},
			SuccessFunc: func(r *EvalResult) bool {
				// Success if agent output contains actual content (wrote a non-trivial response)
				return r.AgentOutput != "" && len(r.AgentOutput) > 50
			},
		},
	}
}
