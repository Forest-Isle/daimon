package eval

// MemorySuite returns tasks that evaluate the agent's memory system —
// storage, retrieval, relevance, and cross-session persistence.
// These tasks use SetupFunc to pre-populate memory data.
func MemorySuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "mem-store-recall",
			Goal:         "I previously told you that my cat's name is Muffin. What is my cat's name?",
			Complexity:   "simple",
			Tags:         []string{"memory", "recall"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain: []string{"Muffin"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "accuracy", Description: "Did the agent correctly recall the cat's name as Muffin?", Weight: 1.0},
				},
			},
		},
		{
			ID:           "mem-relevance",
			Goal:         "I have told you several things: I like coffee, my dog is named Rex, I work at Google, I drive a Tesla, and my birthday is March 15. What pet do I have?",
			Complexity:   "moderate",
			Tags:         []string{"memory", "relevance"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain:    []string{"Rex"},
				MustNotContain: []string{"coffee", "Tesla", "Google", "March"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "precision", Description: "Did the agent return only pet-related info (Rex) without dumping all memories?", Weight: 0.6},
					{Name: "accuracy", Description: "Did it correctly identify Rex as the pet?", Weight: 0.4},
				},
			},
		},
		{
			ID:           "mem-update",
			Goal:         "I used to live in New York but I recently moved to San Francisco. Where do I live now?",
			Complexity:   "moderate",
			Tags:         []string{"memory", "update"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain: []string{"San Francisco"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "recency", Description: "Did the agent return the updated location (San Francisco) not the old one?", Weight: 0.7},
					{Name: "acknowledgment", Description: "Did it acknowledge the move/update?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "mem-profile-use",
			Goal:         "Suggest a text editor for me to use. I've previously told you I prefer terminal-based tools and use vim keybindings.",
			Complexity:   "moderate",
			Tags:         []string{"memory", "profile", "personalization"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "personalization", Description: "Does the suggestion reflect terminal-based preference (e.g., neovim, helix, kakoune)?", Weight: 0.5},
					{Name: "vim_awareness", Description: "Does it consider vim keybinding preference?", Weight: 0.3},
					{Name: "reasoning", Description: "Is the recommendation well-reasoned?", Weight: 0.2},
				},
			},
		},
		{
			ID:           "mem-no-hallucinate",
			Goal:         "What is my favorite movie? (Note: I have never told you about my movie preferences.)",
			Complexity:   "simple",
			Tags:         []string{"memory", "hallucination"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "honesty", Description: "Did the agent admit it doesn't know rather than making up an answer?", Weight: 0.7},
					{Name: "no_fabrication", Description: "Did it avoid fabricating a specific movie title as if from memory?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "mem-cross-session",
			Goal:         "In our previous conversation, I mentioned I'm learning Go and working on a web server project. Based on that, what resources would you recommend?",
			Complexity:   "moderate",
			Tags:         []string{"memory", "cross-session"},
			Dimension:    DimMemory,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "context_awareness", Description: "Does the agent attempt to use cross-session memory or acknowledge the prior conversation?", Weight: 0.4},
					{Name: "relevance", Description: "Are the recommendations relevant to Go web server development?", Weight: 0.4},
					{Name: "honesty", Description: "If memory is unavailable, does it say so rather than pretending?", Weight: 0.2},
				},
			},
		},
	}
}
