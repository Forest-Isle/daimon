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
			ID:         "evo-ambiguous-path",
			Goal:       "Find the configuration file in this project. It might be in configs/, config/, or the root directory. Read it and report the first three lines.",
			Complexity: "moderate",
			Tags:       []string{"bash", "file_read", "ambiguity", "replan-likely"},
			ExpectTools: []string{"bash", "file_read"},
		},
		{
			ID:         "evo-wrong-tool-recovery",
			Goal:       "Check the disk usage of /tmp. If the percentage used is above 50%, create a report file at /tmp/ironclaw_disk_report.txt with the output; otherwise write 'disk OK' to that file.",
			Complexity: "moderate",
			Tags:       []string{"bash", "file_write", "conditional", "replan-likely"},
			ExpectTools: []string{"bash", "file_write"},
		},
		{
			ID:         "evo-multi-attempt-fix",
			Goal:       "Write a Python script at /tmp/ironclaw_eval_calc.py that computes the factorial of 10 and prints the result. Execute it and verify the output is 3628800.",
			Complexity: "complex",
			Tags:       []string{"file_write", "bash", "verification", "replan-likely"},
			ExpectTools: []string{"file_write", "bash"},
			SuccessFunc: func(r *EvalResult) bool {
				return r.Success && r.AssertionPassRate > 0.8
			},
		},
		{
			ID:         "evo-cascading-deps",
			Goal:       "Create directory /tmp/ironclaw_eval_cascade/, then write three files: a.txt with 'alpha', b.txt with content of a.txt reversed, and c.txt with line count of a.txt and b.txt combined. Verify c.txt contains '2'.",
			Complexity: "complex",
			Tags:       []string{"bash", "file_write", "file_read", "dependency-chain", "replan-likely"},
			ExpectTools: []string{"bash", "file_write", "file_read"},
		},
		{
			ID:         "evo-permission-boundary",
			Goal:       "Try to read /etc/shadow. When denied, explain why it failed and instead read /etc/hostname (or equivalent readable system file) and return its content.",
			Complexity: "moderate",
			Tags:       []string{"bash", "file_read", "permission", "error-recovery", "replan-likely"},
			ExpectTools: []string{"bash"},
		},
		{
			ID:         "evo-iterative-refinement",
			Goal:       "Write a shell function in /tmp/ironclaw_eval_greet.sh that takes a name parameter and prints 'Hello, <name>!'. Source the file and test it with the name 'World'. The output must be exactly 'Hello, World!'.",
			Complexity: "complex",
			Tags:       []string{"file_write", "bash", "precision", "replan-likely"},
			ExpectTools: []string{"file_write", "bash"},
			SuccessFunc: func(r *EvalResult) bool {
				return r.Success && r.ReplanCount <= 2
			},
		},
	}
}

// AllSuites returns a map of all available named suites.
func AllSuites() map[string]func() []TaskCase {
	return map[string]func() []TaskCase{
		"builtin":   BuiltinSuite,
		"evolution": EvolutionSuite,
	}
}
