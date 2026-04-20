package eval

// KnowledgeSuite returns tasks that evaluate the agent's knowledge base
// and RAG capabilities — document ingestion, retrieval accuracy, and citation.
func KnowledgeSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "kb-ingest-query",
			Goal:         "I have a document that says: 'IronClaw supports two agent modes: simple mode uses a linear loop, cognitive mode uses a 5-phase loop (PERCEIVE, PLAN, ACT, OBSERVE, REFLECT).' What are the two agent modes and their differences?",
			Complexity:   "moderate",
			Tags:         []string{"knowledge", "retrieval"},
			Dimension:    DimKnowledge,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain: []string{"simple", "cognitive"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "accuracy", Description: "Does it correctly describe both modes?", Weight: 0.5},
					{Name: "detail", Description: "Does it mention the 5 phases of cognitive mode?", Weight: 0.3},
					{Name: "source_awareness", Description: "Does it reference or acknowledge the document source?", Weight: 0.2},
				},
			},
		},
		{
			ID:           "kb-multi-doc",
			Goal:         "Based on project docs: Doc A says 'The memory system uses file-first storage with SQLite as auxiliary index.' Doc B says 'Memories with strength < 0.3 are auto-archived by a background task every 24h.' How does the memory system store data and when are memories archived?",
			Complexity:   "complex",
			Tags:         []string{"knowledge", "multi-source"},
			Dimension:    DimKnowledge,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain: []string{"file", "SQLite", "0.3"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "multi_source", Description: "Does the answer synthesize information from both documents?", Weight: 0.4},
					{Name: "accuracy", Description: "Are the storage mechanism and archival threshold correct?", Weight: 0.4},
					{Name: "coherence", Description: "Is the answer well-organized rather than just concatenating doc excerpts?", Weight: 0.2},
				},
			},
		},
		{
			ID:           "kb-no-answer",
			Goal:         "According to the project documentation, what is IronClaw's pricing model? (Note: no pricing documentation exists.)",
			Complexity:   "simple",
			Tags:         []string{"knowledge", "no-answer"},
			Dimension:    DimKnowledge,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "honesty", Description: "Did the agent say it couldn't find pricing information rather than making something up?", Weight: 0.7},
					{Name: "no_fabrication", Description: "Did it avoid inventing pricing details?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "kb-update",
			Goal:         "The documentation previously said max_iterations was 10, but it was recently updated to 20. What is the current max_iterations value?",
			Complexity:   "moderate",
			Tags:         []string{"knowledge", "update"},
			Dimension:    DimKnowledge,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain: []string{"20"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "recency", Description: "Does the agent return the updated value (20) rather than the old value (10)?", Weight: 0.7},
					{Name: "awareness", Description: "Does it acknowledge that the value was recently changed?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "kb-citation",
			Goal:         "Explain how IronClaw's context compression works. Cite your sources.",
			Complexity:   "complex",
			Tags:         []string{"knowledge", "citation"},
			Dimension:    DimKnowledge,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "accuracy", Description: "Is the explanation of context compression technically correct?", Weight: 0.4},
					{Name: "citation", Description: "Does the agent cite specific files or documentation as sources?", Weight: 0.4},
					{Name: "depth", Description: "Does it mention the 5-layer compression pipeline?", Weight: 0.2},
				},
			},
		},
		{
			ID:           "kb-graph-traverse",
			Goal:         "In IronClaw's architecture, what is the relationship between the StrategyOptimizer and the PreferenceLearner? How do they interact?",
			Complexity:   "complex",
			Tags:         []string{"knowledge", "relationships"},
			Dimension:    DimKnowledge,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "relationship", Description: "Does it correctly describe both components and how they relate?", Weight: 0.4},
					{Name: "accuracy", Description: "Are the described interactions technically correct?", Weight: 0.4},
					{Name: "depth", Description: "Does it go beyond surface-level description?", Weight: 0.2},
				},
			},
		},
	}
}
