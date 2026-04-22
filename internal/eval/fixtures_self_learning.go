package eval

import (
	"context"
	"os"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// extractLastToken extracts the last whitespace-separated token from file content.
// This handles cases where commands like wc produce "  5 filename" or "1\t84".
func extractLastToken(data []byte) string {
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// SkillLearningSuite returns tasks that test whether skill synthesis produces
// measurable behavior improvements. Each task uses deterministic verification:
// the correct output is checked in SuccessFunc, independent of LLM reflection.
func SkillLearningSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "skill-bash-arithmetic",
			Goal:         "Read the number in /tmp/sl_arith_in.txt, compute its double, and write ONLY the result number to /tmp/sl_arith_out.txt. The file must contain just the number (e.g. '84'), no extra text or whitespace.",
			Complexity:   "simple",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash", "arithmetic"},
			SetupFunc: func() error {
				return os.WriteFile("/tmp/sl_arith_in.txt", []byte("42\n"), 0o644)
			},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_arith_in.txt")
				os.Remove("/tmp/sl_arith_out.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_arith_out.txt")
				if err != nil {
					return false
				}
				return extractLastToken(data) == "84"
			},
		},
		{
			ID:           "skill-file-append",
			Goal:         "Append the text 'SKILL_TEST_MARKER' to /tmp/sl_append.txt (create it if needed), then print its contents",
			Complexity:   "simple",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "file"},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_append.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_append.txt")
				if err != nil {
					return false
				}
				return strings.Contains(string(data), "SKILL_TEST_MARKER")
			},
		},
		{
			ID:           "skill-dependency-chain",
			Goal:         "Create /tmp/sl_chain_a.txt with content '100', then read it, compute the value multiplied by 3, and write ONLY that result integer to /tmp/sl_chain_b.txt. The file must contain just the number (e.g. '300').",
			Complexity:   "medium",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "dependency_chain"},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_chain_a.txt")
				os.Remove("/tmp/sl_chain_b.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_chain_b.txt")
				if err != nil {
					return false
				}
				return extractLastToken(data) == "300"
			},
		},
		{
			ID:           "skill-count-lines",
			Goal:         "Count the lines in /tmp/sl_count_src.txt and write ONLY the count as a plain integer to /tmp/sl_count_out.txt. No filename, no extra text — just the number.",
			Complexity:   "simple",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash"},
			SetupFunc: func() error {
				return os.WriteFile("/tmp/sl_count_src.txt", []byte("line1\nline2\nline3\nline4\nline5\n"), 0o644)
			},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_count_src.txt")
				os.Remove("/tmp/sl_count_out.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_count_out.txt")
				if err != nil {
					return false
				}
				return extractLastToken(data) == "5"
			},
		},
		{
			ID:           "skill-pipeline-sum",
			Goal:         "Sum all integers in /tmp/sl_sum_in.txt (one per line) and write ONLY the total integer to /tmp/sl_sum_out.txt. The file must contain just the number, nothing else.",
			Complexity:   "medium",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash", "arithmetic"},
			SetupFunc: func() error {
				return os.WriteFile("/tmp/sl_sum_in.txt", []byte("10\n20\n30\n40\n"), 0o644)
			},
			CleanupFunc: func() error {
				os.Remove("/tmp/sl_sum_in.txt")
				os.Remove("/tmp/sl_sum_out.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/sl_sum_out.txt")
				if err != nil {
					return false
				}
				return extractLastToken(data) == "100"
			},
		},
		{
			ID:           "skill-temp-dir-cleanup",
			Goal:         "Create directory /tmp/sl_cleanup_dir/, create 3 files inside it, then delete the directory and all its contents. Confirm deletion by checking it no longer exists.",
			Complexity:   "medium",
			Dimension:    DimSkillLearning,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"skill_learning", "bash", "file"},
			SuccessFunc: func(r *EvalResult) bool {
				_, err := os.Stat("/tmp/sl_cleanup_dir")
				return os.IsNotExist(err)
			},
		},
	}
}

// PreferenceAdherenceSuite returns tasks with explicit behavioral constraints
// embedded in the goal. Each task verifies whether the agent followed the stated
// preference via deterministic SuccessFunc checks on AgentOutput or side effects.
func PreferenceAdherenceSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "pref-concise-answer",
			Goal:         "What is the current time? Reply with only the time value, nothing else. Keep your answer under 20 words total.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "conciseness"},
			SuccessFunc: func(r *EvalResult) bool {
				words := strings.Fields(r.AgentOutput)
				return r.Success && len(words) <= 20
			},
		},
		{
			ID:           "pref-bash-tool-only",
			Goal:         "List the contents of the /tmp directory. You must use the bash tool for this. Do not use any file listing or file reading tools.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "tool_selection"},
			SuccessFunc: func(r *EvalResult) bool {
				for _, t := range r.ToolsUsed {
					if t == "file_list" || t == "file_read" {
						return false
					}
				}
				for _, t := range r.ToolsUsed {
					if t == "bash" {
						return true
					}
				}
				return false
			},
		},
		{
			ID:           "pref-json-output",
			Goal:         `What is 2+2? Reply ONLY with valid JSON in the format: {"answer": <number>}. Do not include any prose, markdown, or explanation.`,
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.TrimSpace(r.AgentOutput)
				return strings.HasPrefix(out, "{") && strings.HasSuffix(out, "}") &&
					strings.Contains(out, `"answer"`) && strings.Contains(out, "4")
			},
		},
		{
			ID:           "pref-numbered-steps",
			Goal:         "Explain how to make coffee in exactly 3 numbered steps. Format each step as '1. ', '2. ', '3. '.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			SuccessFunc: func(r *EvalResult) bool {
				out := r.AgentOutput
				return strings.Contains(out, "1.") && strings.Contains(out, "2.") && strings.Contains(out, "3.")
			},
		},
		{
			ID:           "pref-no-tool-use",
			Goal:         "What is the capital of France? Answer from your knowledge only — do NOT use any tools or bash commands.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "knowledge_only"},
			SuccessFunc: func(r *EvalResult) bool {
				return len(r.ToolsUsed) == 0 &&
					strings.Contains(strings.ToLower(r.AgentOutput), "paris")
			},
		},
		{
			ID:           "pref-single-command-answer",
			Goal:         "Get the hostname of this machine. Use exactly one bash command. Do not run multiple commands.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "efficiency"},
			SuccessFunc: func(r *EvalResult) bool {
				bashCount := 0
				for _, t := range r.ToolsUsed {
					if t == "bash" {
						bashCount++
					}
				}
				return bashCount == 1 && r.Success
			},
		},
		{
			ID:           "pref-uppercase-output",
			Goal:         "Echo the phrase 'hello world' using bash. The output must appear in UPPERCASE in your response.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(r.AgentOutput, "HELLO WORLD")
			},
		},
		{
			ID:           "pref-write-specific-file",
			Goal:         "Write the word 'DONE' to /tmp/pref_test_done.txt. The file must exist with that exact content when you finish.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "file_output"},
			CleanupFunc: func() error {
				os.Remove("/tmp/pref_test_done.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/pref_test_done.txt")
				if err != nil {
					return false
				}
				return strings.TrimSpace(string(data)) == "DONE"
			},
		},
	}
}

// injectMemoryHelper injects memory entries if the runner supports it.
// Returns nil silently when the runner doesn't implement memory injection.
func injectMemoryHelper(ctx context.Context, runner AgentRunner, entries ...memory.Entry) error {
	type memAware interface {
		InjectMemory(ctx context.Context, entries ...memory.Entry) error
	}
	if ma, ok := runner.(memAware); ok {
		return ma.InjectMemory(ctx, entries...)
	}
	return nil
}

// MemoryRetentionSuite returns tasks that test cross-session memory persistence,
// precision recall, multi-hop reasoning, and negative recall (unknown facts).
func MemoryRetentionSuite() []TaskCase {
	return []TaskCase{
		{
			ID:         "mem-ret-basic-recall",
			Goal:       "What is Alice's favorite programming language?",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "recall"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner, memory.Entry{
					ID:      "mem-ret-alice-lang",
					Content: "Alice's favorite programming language is Rust. She has been using it since 2019.",
					Scope:   memory.ScopeUser,
					UserID:  "eval_user",
				})
			},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(strings.ToLower(r.AgentOutput), "rust")
			},
		},
		{
			ID:         "mem-ret-precision-recall",
			Goal:       "What programming language does Bob prefer? (Do not confuse with Alice's preference.)",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "precision"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner,
					memory.Entry{
						ID:      "mem-ret-alice-lang-precision",
						Content: "Alice's favorite programming language is Rust.",
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					},
					memory.Entry{
						ID:      "mem-ret-bob-lang",
						Content: "Bob's preferred programming language is Python. He uses it for data science.",
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					},
				)
			},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.ToLower(r.AgentOutput)
				return strings.Contains(out, "python") && !strings.Contains(out, "rust")
			},
		},
		{
			ID:         "mem-ret-multi-hop",
			Goal:       "What city does the team lead of Project Phoenix live in?",
			Complexity: "medium",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "multi_hop"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner,
					memory.Entry{
						ID:      "mem-ret-phoenix-lead",
						Content: "Project Phoenix team lead is Carol Chen.",
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					},
					memory.Entry{
						ID:      "mem-ret-carol-location",
						Content: "Carol Chen lives in Singapore. She works from home.",
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					},
				)
			},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(strings.ToLower(r.AgentOutput), "singapore")
			},
		},
		{
			ID:         "mem-ret-numeric-fact",
			Goal:       "How many team members does Project Alpha have?",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "numeric"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner, memory.Entry{
					ID:      "mem-ret-alpha-size",
					Content: "Project Alpha has 7 team members: 4 engineers, 2 designers, and 1 product manager.",
					Scope:   memory.ScopeUser,
					UserID:  "eval_user",
				})
			},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(r.AgentOutput, "7")
			},
		},
		{
			ID:         "mem-ret-temporal-fact",
			Goal:       "When does the Q2 planning session start?",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "temporal"},
			SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
				return injectMemoryHelper(ctx, runner, memory.Entry{
					ID:      "mem-ret-q2-planning",
					Content: "The Q2 planning session is scheduled for April 28, 2026 at 10:00 AM CST.",
					Scope:   memory.ScopeUser,
					UserID:  "eval_user",
				})
			},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.ToLower(r.AgentOutput)
				return strings.Contains(out, "april 28") || strings.Contains(out, "apr 28") ||
					strings.Contains(out, "28")
			},
		},
		{
			ID:         "mem-ret-unknown-fact",
			Goal:       "What is David's phone number? If you don't know, say 'I don't have that information'.",
			Complexity: "simple",
			Dimension:  DimMemoryRetention,
			Tags:       []string{"memory_retention", "negative_recall"},
			SuccessFunc: func(r *EvalResult) bool {
				out := strings.ToLower(r.AgentOutput)
				return strings.Contains(out, "don't have") ||
					strings.Contains(out, "do not have") ||
					strings.Contains(out, "no information") ||
					strings.Contains(out, "not available") ||
					strings.Contains(out, "don't know") ||
					strings.Contains(out, "cannot find")
			},
		},
	}
}

// SelfLearningSuite combines all self-learning dimensions into one composite suite.
func SelfLearningSuite() []TaskCase {
	var all []TaskCase
	all = append(all, SkillLearningSuite()...)
	all = append(all, PreferenceAdherenceSuite()...)
	all = append(all, MemoryRetentionSuite()...)
	return all
}

