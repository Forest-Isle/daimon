package eval

// PreferenceTasks returns eval tasks that verify preference learning behavior.
// These tasks include explicit UserFeedback signals to simulate user ratings
// and exercise the preference learner's positive/negative reinforcement paths.
//
// Because they do not rely on live LLM calls the fixtures are compatible with
// DryRunner as well as CognitiveAgentRunner.
func PreferenceTasks() []TaskCase {
	return []TaskCase{
		{
			ID:           "pref-tool-bash-preference",
			Goal:         "Run the command 'echo hello' and show the output",
			Complexity:   "simple",
			Tags:         []string{"preference", "tool"},
			ExpectTools:  []string{"bash"},
			UserFeedback: 0.8, // positive feedback reinforces bash tool preference
		},
		{
			ID:           "pref-complexity-simple",
			Goal:         "What is 2 + 2?",
			Complexity:   "simple",
			Tags:         []string{"preference", "complexity"},
			UserFeedback: 1.0,
		},
		{
			ID:           "pref-negative-feedback",
			Goal:         "List all files in /tmp directory",
			Complexity:   "simple",
			Tags:         []string{"preference", "feedback"},
			UserFeedback: -0.5, // negative feedback — tests that negative signals don't reinforce
		},
	}
}
