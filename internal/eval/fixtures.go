package eval

// BuiltinSuite returns a set of deterministic evaluation tasks covering
// the main tool categories. These tasks are designed to be repeatable and
// produce measurable results.
func BuiltinSuite() []TaskCase {
	return []TaskCase{
		{
			ID:          "bash-echo",
			Goal:        "Run 'echo hello world' and return the output",
			Complexity:  "simple",
			Tags:        []string{"bash", "simple"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "bash-multi-step",
			Goal:        "Create a temp directory, write a file inside it with 'hello' content, then read the file and verify its content",
			Complexity:  "moderate",
			Tags:        []string{"bash", "file", "multi-step"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "file-write-read",
			Goal:        "Write 'test content 12345' to /tmp/ironclaw_eval_test.txt, then read it back and confirm the content matches",
			Complexity:  "moderate",
			Tags:        []string{"file_write", "file_read"},
			ExpectTools: []string{"file_write", "file_read"},
		},
		{
			ID:          "bash-error-recovery",
			Goal:        "Try to list the contents of /nonexistent_dir_12345. When it fails, list the current directory instead and return the result",
			Complexity:  "moderate",
			Tags:        []string{"bash", "error-handling"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "bash-pipeline",
			Goal:        "Use bash to create a file /tmp/ironclaw_eval_nums.txt with numbers 1 through 10 (one per line), then use bash to count the lines and sum all numbers",
			Complexity:  "moderate",
			Tags:        []string{"bash", "pipeline"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "multi-tool-compose",
			Goal:        "Write a JSON file at /tmp/ironclaw_eval_config.json with fields {\"name\": \"test\", \"version\": 1}, then use bash with 'cat' to read and verify it contains 'test'",
			Complexity:  "complex",
			Tags:        []string{"file_write", "bash", "composition"},
			ExpectTools: []string{"file_write", "bash"},
		},
		{
			ID:          "bash-script-gen",
			Goal:        "Write a bash script at /tmp/ironclaw_eval_script.sh that prints the current date and hostname, then execute it and return the output",
			Complexity:  "complex",
			Tags:        []string{"file_write", "bash", "code-gen"},
			ExpectTools: []string{"file_write", "bash"},
		},
		{
			ID:          "file-edit-flow",
			Goal:        "Write 'line1\\nline2\\nline3' to /tmp/ironclaw_eval_edit.txt, then read it, then use bash to replace 'line2' with 'MODIFIED' using sed, then read the file again to verify",
			Complexity:  "complex",
			Tags:        []string{"file_write", "file_read", "bash", "multi-step"},
			ExpectTools: []string{"file_write", "bash"},
		},
	}
}

// EvolutionSuite returns tasks designed to stress-test the cognitive loop's
// replan and error-recovery capabilities. These tasks are intentionally tricky:
// they involve ambiguous instructions, deliberate error conditions, and
// multi-step dependencies that often trigger replanning. Running this suite
// across evolution cycles measures whether the strategy optimizer is improving
// the agent's replan efficiency.
func EvolutionSuite() []TaskCase {
	return []TaskCase{
		{
			ID:          "evo-ambiguous-path",
			Goal:        "Find the configuration file in this project. It might be in configs/, config/, or the root directory. Read it and report the first three lines.",
			Complexity:  "moderate",
			Tags:        []string{"bash", "file_read", "ambiguity", "replan-likely"},
			ExpectTools: []string{"bash", "file_read"},
		},
		{
			ID:          "evo-wrong-tool-recovery",
			Goal:        "Check the disk usage of /tmp. If the percentage used is above 50%, create a report file at /tmp/ironclaw_disk_report.txt with the output; otherwise write 'disk OK' to that file.",
			Complexity:  "moderate",
			Tags:        []string{"bash", "file_write", "conditional", "replan-likely"},
			ExpectTools: []string{"bash", "file_write"},
		},
		{
			ID:          "evo-multi-attempt-fix",
			Goal:        "Write a Python script at /tmp/ironclaw_eval_calc.py that computes the factorial of 10 and prints the result. Execute it and verify the output is 3628800.",
			Complexity:  "complex",
			Tags:        []string{"file_write", "bash", "verification", "replan-likely"},
			ExpectTools: []string{"file_write", "bash"},
			SuccessFunc: func(r *EvalResult) bool {
				return r.Success && r.AssertionPassRate > 0.8
			},
		},
		{
			ID:          "evo-cascading-deps",
			Goal:        "Create directory /tmp/ironclaw_eval_cascade/, then write three files: a.txt with 'alpha', b.txt with content of a.txt reversed, and c.txt with line count of a.txt and b.txt combined. Verify c.txt contains '2'.",
			Complexity:  "complex",
			Tags:        []string{"bash", "file_write", "file_read", "dependency-chain", "replan-likely"},
			ExpectTools: []string{"bash", "file_write", "file_read"},
		},
		{
			ID:          "evo-permission-boundary",
			Goal:        "Try to read /etc/shadow. When denied, explain why it failed and instead read /etc/hostname (or equivalent readable system file) and return its content.",
			Complexity:  "moderate",
			Tags:        []string{"bash", "file_read", "permission", "error-recovery", "replan-likely"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "evo-iterative-refinement",
			Goal:        "Write a shell function in /tmp/ironclaw_eval_greet.sh that takes a name parameter and prints 'Hello, <name>!'. Source the file and test it with the name 'World'. The output must be exactly 'Hello, World!'.",
			Complexity:  "complex",
			Tags:        []string{"file_write", "bash", "precision", "replan-likely"},
			ExpectTools: []string{"file_write", "bash"},
			SuccessFunc: func(r *EvalResult) bool {
				return r.Success && r.ReplanCount <= 2
			},
		},
	}
}

// WorkloadSuite returns 20+ diverse tasks designed to generate evolution
// pressure — trajectory data for the optimizer, preference learner, and skill
// synthesizer. Tasks do NOT require SuccessFunc; they just need to produce
// realistic, varied tool-usage patterns across five categories.
func WorkloadSuite() []TaskCase {
	return []TaskCase{
		// ── Simple bash warmup ──────────────────────────────────────────
		{
			ID:          "wl-echo-env",
			Goal:        "Print the current USER and HOME environment variables",
			Complexity:  "simple",
			Tags:        []string{"workload", "bash", "warmup"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "wl-date-hostname",
			Goal:        "Print the current date, uptime, and hostname on separate lines",
			Complexity:  "simple",
			Tags:        []string{"workload", "bash", "warmup"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "wl-list-tmp",
			Goal:        "List all files in /tmp sorted by modification time (newest first) and count them",
			Complexity:  "simple",
			Tags:        []string{"workload", "bash", "warmup"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "wl-disk-free",
			Goal:        "Show disk usage summary for the root filesystem in human-readable format",
			Complexity:  "simple",
			Tags:        []string{"workload", "bash", "warmup"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "wl-process-count",
			Goal:        "Count the number of currently running processes and report the top 3 by CPU usage",
			Complexity:  "simple",
			Tags:        []string{"workload", "bash", "warmup"},
			ExpectTools: []string{"bash"},
		},

		// ── File manipulation chains ───────────────────────────────────
		{
			ID:          "wl-file-roundtrip",
			Goal:        "Write 'alpha\\nbeta\\ngamma' to /tmp/ironclaw_wl_rt.txt, read it back, append 'delta', then verify the file has exactly 4 lines",
			Complexity:  "moderate",
			Tags:        []string{"workload", "file", "chain", "pattern-detection"},
			ExpectTools: []string{"file_write", "file_read", "bash"},
		},
		{
			ID:          "wl-json-transform",
			Goal:        "Write a JSON file /tmp/ironclaw_wl_input.json with {\"items\": [1,2,3]}, then use bash to transform it into a newline-delimited list in /tmp/ironclaw_wl_output.txt and verify the output",
			Complexity:  "moderate",
			Tags:        []string{"workload", "file", "chain", "pattern-detection"},
			ExpectTools: []string{"file_write", "bash", "file_read"},
		},
		{
			ID:          "wl-config-edit-cycle",
			Goal:        "Write a YAML config to /tmp/ironclaw_wl_cfg.yaml with 'debug: false' and 'port: 8080'. Read it, change debug to true, write it back, then verify the change took effect",
			Complexity:  "moderate",
			Tags:        []string{"workload", "file", "chain", "pattern-detection"},
			ExpectTools: []string{"file_write", "file_read", "bash"},
		},
		{
			ID:          "wl-log-rotation",
			Goal:        "Create /tmp/ironclaw_wl_app.log with 20 lines of 'log entry N'. Copy it to /tmp/ironclaw_wl_app.log.1, truncate the original, write 'rotated' to the original, then verify both files exist with expected content",
			Complexity:  "moderate",
			Tags:        []string{"workload", "file", "chain", "pattern-detection"},
			ExpectTools: []string{"file_write", "bash", "file_read"},
		},
		{
			ID:          "wl-csv-build-verify",
			Goal:        "Write a CSV header 'name,score' to /tmp/ironclaw_wl_scores.csv, append rows Alice,95 and Bob,87, then read it and verify it has exactly 3 lines",
			Complexity:  "moderate",
			Tags:        []string{"workload", "file", "chain", "pattern-detection"},
			ExpectTools: []string{"file_write", "bash", "file_read"},
		},

		// ── Deliberate failure + recovery ──────────────────────────────
		{
			ID:          "wl-missing-dir-recovery",
			Goal:        "Try to write a file to /tmp/ironclaw_wl_nodir/deep/nested/file.txt. When the directory doesn't exist, create the full path and then write the file successfully",
			Complexity:  "moderate",
			Tags:        []string{"workload", "failure-recovery", "replan-pressure"},
			ExpectTools: []string{"bash", "file_write"},
		},
		{
			ID:          "wl-bad-json-fix",
			Goal:        "Write invalid JSON '{name: broken}' to /tmp/ironclaw_wl_bad.json, try to parse it with bash (python -m json.tool), observe the error, then fix the JSON to valid format and parse again",
			Complexity:  "moderate",
			Tags:        []string{"workload", "failure-recovery", "replan-pressure"},
			ExpectTools: []string{"file_write", "bash"},
		},
		{
			ID:          "wl-permission-fallback",
			Goal:        "Attempt to write to /etc/ironclaw_wl_test.conf. When permission is denied, write to /tmp/ironclaw_wl_test.conf instead and report the fallback",
			Complexity:  "moderate",
			Tags:        []string{"workload", "failure-recovery", "replan-pressure"},
			ExpectTools: []string{"bash", "file_write"},
		},
		{
			ID:          "wl-script-syntax-fix",
			Goal:        "Write a Python script at /tmp/ironclaw_wl_buggy.py with a deliberate syntax error (missing colon after if), run it, observe the SyntaxError, fix the script, and run it successfully",
			Complexity:  "complex",
			Tags:        []string{"workload", "failure-recovery", "replan-pressure"},
			ExpectTools: []string{"file_write", "bash"},
		},
		{
			ID:          "wl-timeout-retry",
			Goal:        "Run 'sleep 0.5 && echo done' with a 0.1s timeout using bash timeout command. When it times out, rerun with a 2s timeout and confirm 'done' appears",
			Complexity:  "moderate",
			Tags:        []string{"workload", "failure-recovery", "replan-pressure"},
			ExpectTools: []string{"bash"},
		},

		// ── Multi-tool compositions ────────────────────────────────────
		{
			ID:          "wl-report-pipeline",
			Goal:        "Create directory /tmp/ironclaw_wl_report/, write 3 data files (metrics.txt, errors.txt, summary.txt) with sample content, then use bash to concatenate them into report.txt and count total lines",
			Complexity:  "complex",
			Tags:        []string{"workload", "multi-tool", "composition"},
			ExpectTools: []string{"bash", "file_write", "file_read"},
		},
		{
			ID:          "wl-env-config-gen",
			Goal:        "Read the current environment variables PATH, HOME, and SHELL using bash, then write a JSON config to /tmp/ironclaw_wl_env.json containing these values, read it back and verify all three keys exist",
			Complexity:  "complex",
			Tags:        []string{"workload", "multi-tool", "composition"},
			ExpectTools: []string{"bash", "file_write", "file_read"},
		},
		{
			ID:          "wl-backup-restore",
			Goal:        "Write 'important data v1' to /tmp/ironclaw_wl_data.txt, copy it to /tmp/ironclaw_wl_data.bak using bash, overwrite original with 'corrupted', then restore from backup and verify the content matches the original",
			Complexity:  "complex",
			Tags:        []string{"workload", "multi-tool", "composition"},
			ExpectTools: []string{"file_write", "bash", "file_read"},
		},
		{
			ID:          "wl-script-gen-test",
			Goal:        "Write a bash script at /tmp/ironclaw_wl_calc.sh that accepts two arguments, prints their sum, make it executable, run it with args 17 and 25, and verify the output is 42",
			Complexity:  "complex",
			Tags:        []string{"workload", "multi-tool", "composition"},
			ExpectTools: []string{"file_write", "bash"},
		},

		// ── Ambiguous / underspecified ─────────────────────────────────
		{
			ID:          "wl-find-something",
			Goal:        "Find and read the main configuration file for this project — it could be YAML, TOML, JSON, or .env, in the root or a config subdirectory",
			Complexity:  "moderate",
			Tags:        []string{"workload", "ambiguous", "planning"},
			ExpectTools: []string{"bash", "file_read"},
		},
		{
			ID:          "wl-clean-tmp",
			Goal:        "Clean up temporary files in /tmp that start with 'ironclaw_wl_'. Be careful not to remove anything important.",
			Complexity:  "moderate",
			Tags:        []string{"workload", "ambiguous", "planning"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:          "wl-summarize-project",
			Goal:        "Figure out what kind of project this is and give a one-paragraph summary including the language, build system, and main entry point",
			Complexity:  "complex",
			Tags:        []string{"workload", "ambiguous", "planning"},
			ExpectTools: []string{"bash", "file_read"},
		},
	}
}

// FullSuite returns all tasks across all dimensions for comprehensive evaluation.
func FullSuite() []TaskCase {
	var all []TaskCase
	all = append(all, BuiltinSuite()...)
	all = append(all, PlanningSuite()...)
	all = append(all, ErrorRecoverySuite()...)
	all = append(all, ToolSelectionSuite()...)
	all = append(all, ConversationSuite()...)
	all = append(all, MemorySuite()...)
	all = append(all, KnowledgeSuite()...)
	all = append(all, MultiAgentSuite()...)
	// Self-learning dimensions
	all = append(all, SkillLearningSuite()...)
	all = append(all, PreferenceAdherenceSuite()...)
	all = append(all, MemoryRetentionSuite()...)
	all = append(all, SkillEvolutionSuite()...)
	return all
}

// AllSuites returns a map of all available named suites.
func AllSuites() map[string]func() []TaskCase {
	return map[string]func() []TaskCase{
		"builtin":              BuiltinSuite,
		"evolution":            EvolutionSuite,
		"workload":             WorkloadSuite,
		"planning":             PlanningSuite,
		"error_recovery":       ErrorRecoverySuite,
		"tool_selection":       ToolSelectionSuite,
		"conversation":         ConversationSuite,
		"memory":               MemorySuite,
		"knowledge":            KnowledgeSuite,
		"multi_agent":          MultiAgentSuite,
		"skill_learning":       SkillLearningSuite,
		"preference_adherence": PreferenceAdherenceSuite,
		"memory_retention":     MemoryRetentionSuite,
		"skill_evolution":      SkillEvolutionSuite,
		"self_learning":        SelfLearningSuite,
		"full":                 FullSuite,
	}
}
