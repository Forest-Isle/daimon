package eval

import (
	"context"
	"fmt"
	"math/rand"
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
			_ = os.Remove("/tmp/sl_arith_in.txt")
			_ = os.Remove("/tmp/sl_arith_out.txt")
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
			_ = os.Remove("/tmp/sl_append.txt")
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
			_ = os.Remove("/tmp/sl_chain_a.txt")
			_ = os.Remove("/tmp/sl_chain_b.txt")
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
			_ = os.Remove("/tmp/sl_count_src.txt")
			_ = os.Remove("/tmp/sl_count_out.txt")
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
				_ = os.Remove("/tmp/sl_sum_in.txt")
				_ = os.Remove("/tmp/sl_sum_out.txt")
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
				_ = os.Remove("/tmp/pref_test_done.txt")
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

// memRetToken generates a unique alphanumeric token that cannot appear in source code.
// Format: prefix + hex suffix, e.g. "ZLang_3f7a2c".
func memRetToken(prefix string) string {
	return fmt.Sprintf("%s_%06x", prefix, rand.Uint32()&0xFFFFFF)
}

// MemoryRetentionSuite returns tasks that test cross-session memory persistence,
// precision recall, multi-hop reasoning, and negative recall.
//
// All fact VALUES are generated dynamically at runtime to prevent the agent from
// finding answers by reading the fixture source file. The token is captured via
// closure between SetupWithRunner and SuccessFunc.
func MemoryRetentionSuite() []TaskCase {
	return []TaskCase{
		// Task 1: Basic recall — single injected fact
		func() TaskCase {
			var expected string
			return TaskCase{
				ID:         "mem-ret-basic-recall",
				Goal:       "What is Kira Voss's preferred development framework? Answer from memory.",
				Complexity: "simple",
				Dimension:  DimMemoryRetention,
				Tags:       []string{"memory_retention", "recall"},
				SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
					expected = memRetToken("FW")
					return injectMemoryHelper(ctx, runner, memory.Entry{
						ID:      "mem-ret-kira-fw",
						Content: fmt.Sprintf("Kira Voss's preferred development framework is %s.", expected),
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					})
				},
				SuccessFunc: func(r *EvalResult) bool {
					return expected != "" && strings.Contains(r.AgentOutput, expected)
				},
			}
		}(),

		// Task 2: Precision recall — two similar facts, pick the right one
		func() TaskCase {
			var expectedBob string
			return TaskCase{
				ID:         "mem-ret-precision-recall",
				Goal:       "What tool does Tristan Hale use for deployment? Do not confuse with Kira Voss's framework.",
				Complexity: "simple",
				Dimension:  DimMemoryRetention,
				Tags:       []string{"memory_retention", "precision"},
				SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
					kiraFW := memRetToken("FW")
					expectedBob = memRetToken("DEPLOY")
					return injectMemoryHelper(ctx, runner,
						memory.Entry{
							ID:      "mem-ret-kira-fw-p",
							Content: fmt.Sprintf("Kira Voss's preferred development framework is %s.", kiraFW),
							Scope:   memory.ScopeUser,
							UserID:  "eval_user",
						},
						memory.Entry{
							ID:      "mem-ret-tristan-deploy",
							Content: fmt.Sprintf("Tristan Hale uses %s for all deployment pipelines.", expectedBob),
							Scope:   memory.ScopeUser,
							UserID:  "eval_user",
						},
					)
				},
				SuccessFunc: func(r *EvalResult) bool {
					return expectedBob != "" && strings.Contains(r.AgentOutput, expectedBob)
				},
			}
		}(),

		// Task 3: Multi-hop — combine two separate facts
		func() TaskCase {
			var expectedCity string
			return TaskCase{
				ID:         "mem-ret-multi-hop",
				Goal:       "What city is the lead of Project Nexus based in?",
				Complexity: "medium",
				Dimension:  DimMemoryRetention,
				Tags:       []string{"memory_retention", "multi_hop"},
				SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
					personName := fmt.Sprintf("Lyra%04x Chen", rand.Uint32()&0xFFFF)
					expectedCity = memRetToken("CITY")
					return injectMemoryHelper(ctx, runner,
						memory.Entry{
							ID:      "mem-ret-nexus-lead",
							Content: fmt.Sprintf("Project Nexus is led by %s.", personName),
							Scope:   memory.ScopeUser,
							UserID:  "eval_user",
						},
						memory.Entry{
							ID:      "mem-ret-lyra-city",
							Content: fmt.Sprintf("%s is based in %s and works remotely.", personName, expectedCity),
							Scope:   memory.ScopeUser,
							UserID:  "eval_user",
						},
					)
				},
				SuccessFunc: func(r *EvalResult) bool {
					return expectedCity != "" && strings.Contains(r.AgentOutput, expectedCity)
				},
			}
		}(),

		// Task 4: Numeric fact
		func() TaskCase {
			var expectedCount int
			return TaskCase{
				ID:         "mem-ret-numeric-fact",
				Goal:       "How many engineers are in the Orion squad?",
				Complexity: "simple",
				Dimension:  DimMemoryRetention,
				Tags:       []string{"memory_retention", "numeric"},
				SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
					expectedCount = 5 + int(rand.Uint32()%10) // 5-14
					return injectMemoryHelper(ctx, runner, memory.Entry{
						ID:      "mem-ret-orion-count",
						Content: fmt.Sprintf("The Orion squad currently has %d engineers.", expectedCount),
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					})
				},
				SuccessFunc: func(r *EvalResult) bool {
					return expectedCount > 0 && strings.Contains(r.AgentOutput, fmt.Sprintf("%d", expectedCount))
				},
			}
		}(),

		// Task 5: Temporal fact
		func() TaskCase {
			var expectedDate string
			return TaskCase{
				ID:         "mem-ret-temporal-fact",
				Goal:       "When is the Delta sprint retrospective scheduled?",
				Complexity: "simple",
				Dimension:  DimMemoryRetention,
				Tags:       []string{"memory_retention", "temporal"},
				SetupWithRunner: func(ctx context.Context, runner AgentRunner) error {
					day := 5 + int(rand.Uint32()%20)
					expectedDate = fmt.Sprintf("the %dth", day)
					return injectMemoryHelper(ctx, runner, memory.Entry{
						ID:      "mem-ret-delta-retro",
						Content: fmt.Sprintf("The Delta sprint retrospective is scheduled for %s of next month at 3 PM.", expectedDate),
						Scope:   memory.ScopeUser,
						UserID:  "eval_user",
					})
				},
				SuccessFunc: func(r *EvalResult) bool {
					return expectedDate != "" && strings.Contains(r.AgentOutput, expectedDate)
				},
			}
		}(),

		// Task 6: Negative recall — fact was never stored
		{
			ID:         "mem-ret-unknown-fact",
			Goal:       "What is the access code for the Sigma vault? If you don't know, say you don't have that information.",
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
					strings.Contains(out, "cannot find") ||
					strings.Contains(out, "no record") ||
					strings.Contains(out, "unable to find")
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

