package eval

import (
	"context"
	"os"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// SkillLearningSuite returns tasks that test whether skill synthesis produces
// measurable behavior improvements. Each task uses deterministic verification:
// the correct output is checked in SuccessFunc, independent of LLM reflection.
func SkillLearningSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "skill-bash-arithmetic",
			Goal:         "Read the number stored in /tmp/sl_arith_in.txt, double it, write the result to /tmp/sl_arith_out.txt",
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
				return strings.TrimSpace(string(data)) == "84"
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
			Goal:         "Create /tmp/sl_chain_a.txt with content '100', then read it, multiply by 3, write result to /tmp/sl_chain_b.txt",
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
				return strings.TrimSpace(string(data)) == "300"
			},
		},
		{
			ID:           "skill-count-lines",
			Goal:         "Count the number of lines in /tmp/sl_count_src.txt and write just the number to /tmp/sl_count_out.txt",
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
				return strings.TrimSpace(string(data)) == "5"
			},
		},
		{
			ID:           "skill-pipeline-sum",
			Goal:         "Sum all numbers in /tmp/sl_sum_in.txt (one per line) and write the total to /tmp/sl_sum_out.txt",
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
				return strings.TrimSpace(string(data)) == "100"
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
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "conciseness", Description: "Score 1.0 if the response contains only a time value (e.g. '14:32' or '2:32 PM') with minimal surrounding text (≤20 words total). Score 0.5 if the time is present but with verbose explanation. Score 0.0 if time is missing or response exceeds 20 words.", Weight: 1.0},
			}},
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
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "tool_selection", Description: "Score 1.0 if the agent used only the bash tool and no file_list or file_read tools. Score 0.5 if bash was used but other file tools were also used. Score 0.0 if bash was not used at all.", Weight: 1.0},
			}},
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
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "json_format", Description: `Score 1.0 if the response is ONLY valid JSON in the exact format {"answer": 4} with no surrounding text, markdown, or prose. Score 0.5 if the JSON is present but wrapped in markdown code fences or has extra explanation. Score 0.0 if the JSON format is wrong or missing.`, Weight: 1.0},
			}},
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
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "numbered_steps", Description: "Score 1.0 if the response contains exactly 3 numbered steps using the '1. ', '2. ', '3. ' format with no additional steps. Score 0.5 if numbered steps are present but format is slightly off or there are more/fewer steps. Score 0.0 if not numbered or no steps.", Weight: 1.0},
			}},
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
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "knowledge_only", Description: "Score 1.0 if the answer 'Paris' is given using only knowledge (no tool calls made). Score 0.5 if Paris is mentioned but a tool was also used. Score 0.0 if Paris is not mentioned or no answer given.", Weight: 1.0},
			}},
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
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "single_command", Description: "Score 1.0 if exactly one bash command was used and the hostname is shown. Score 0.5 if the hostname is shown but more than one bash command was used. Score 0.0 if no hostname is shown.", Weight: 1.0},
			}},
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
			Goal:         "Use bash to output the phrase 'hello world' in ALL CAPS (uppercase). Use a command like: echo 'hello world' | tr '[:lower:]' '[:upper:]' — the final output in your response must show 'HELLO WORLD'.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "output_format"},
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "uppercase_output", Description: "Score 1.0 if 'HELLO WORLD' appears in the response in all uppercase. Score 0.5 if the phrase appears in mixed case (e.g. 'Hello World'). Score 0.0 if neither 'hello world' nor 'HELLO WORLD' is present.", Weight: 1.0},
			}},
			SuccessFunc: func(r *EvalResult) bool {
				return strings.Contains(r.AgentOutput, "HELLO WORLD")
			},
		},
		{
			ID:           "pref-write-specific-file",
			Goal:         "Write the word 'DONE' to /tmp/pref_test_done.txt using bash: echo 'DONE' > /tmp/pref_test_done.txt. The file must exist and contain 'DONE' when you finish.",
			Complexity:   "simple",
			Dimension:    DimPreferenceAdherence,
			VerifyMethod: VerifyDeterministic,
			Tags:         []string{"preference_adherence", "file_output"},
			Rubric: &Rubric{Criteria: []JudgeCriterion{
				{Name: "file_write", Description: "Score 1.0 if the file /tmp/pref_test_done.txt was created and contains 'DONE'. Score 0.5 if the agent attempted to write the file but the content is incorrect. Score 0.0 if no file was written.", Weight: 1.0},
			}},
			CleanupFunc: func() error {
				os.Remove("/tmp/pref_test_done.txt")
				return nil
			},
			SuccessFunc: func(r *EvalResult) bool {
				data, err := os.ReadFile("/tmp/pref_test_done.txt")
				if err != nil {
					return false
				}
				return strings.Contains(string(data), "DONE")
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

