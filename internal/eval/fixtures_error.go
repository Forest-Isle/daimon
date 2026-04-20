package eval

// ErrorRecoverySuite returns tasks designed to test the agent's ability to
// recover from errors, retry with different strategies, and handle partial failures.
func ErrorRecoverySuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "err-missing-dep",
			Goal:         "Parse the JSON file /tmp/ironclaw_eval_err.json using jq. If jq is not available, use python or another method to parse it instead. First create the file with content '{\"name\": \"test\", \"value\": 42}', then parse it to extract the 'value' field.",
			Complexity:   "moderate",
			Tags:         []string{"error-recovery", "fallback"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"42"},
			},
		},
		{
			ID:           "err-wrong-path",
			Goal:         "Read the file /tmp/ironclaw_eval_err/data/infro.txt (note: the correct filename is 'info.txt', not 'infro.txt'). First create /tmp/ironclaw_eval_err/data/info.txt with content 'secret data 12345'. Then try to read infro.txt, discover the error, and find and read the correct file.",
			Complexity:   "moderate",
			Tags:         []string{"error-recovery", "self-correction"},
			ExpectTools:  []string{"bash", "file_write", "file_read"},
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"secret data 12345"},
			},
		},
		{
			ID:           "err-permission",
			Goal:         "Write the text 'important config' to /etc/ironclaw_eval_perm.conf. When this fails due to permissions, write it to /tmp/ironclaw_eval_perm.conf instead and report the fallback.",
			Complexity:   "moderate",
			Tags:         []string{"error-recovery", "permission", "fallback"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"important config"},
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_perm.conf", MustExist: true, Contains: "important config"},
				},
			},
		},
		{
			ID:           "err-timeout-retry",
			Goal:         "Run 'sleep 2 && echo done' with a 0.1 second timeout (using the timeout command). When it times out, rerun with a 5 second timeout and confirm 'done' appears in the output.",
			Complexity:   "moderate",
			Tags:         []string{"error-recovery", "retry"},
			ExpectTools:  []string{"bash"},
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"done"},
			},
		},
		{
			ID:           "err-partial-failure",
			Goal:         "Execute three tasks: (1) write 'task1' to /tmp/ironclaw_eval_partial/a.txt, (2) write to /read-only-dir/b.txt (this will fail), (3) write 'task3' to /tmp/ironclaw_eval_partial/c.txt. Task 2 will fail but tasks 1 and 3 must still succeed. Create the directory first.",
			Complexity:   "moderate",
			Tags:         []string{"error-recovery", "partial-failure"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_partial/a.txt", MustExist: true, Contains: "task1"},
					{Path: "/tmp/ironclaw_eval_partial/c.txt", MustExist: true, Contains: "task3"},
				},
			},
		},
		{
			ID:           "err-cascading",
			Goal:         "Step 1: Read the config from /tmp/ironclaw_eval_cascade_cfg.json to get the output directory. Step 2: If the config doesn't exist, create a default config with {\"output_dir\": \"/tmp/ironclaw_eval_cascade_out\"} and use that. Step 3: Write 'result' to output_dir/result.txt.",
			Complexity:   "complex",
			Tags:         []string{"error-recovery", "cascading", "adaptive"},
			ExpectTools:  []string{"bash", "file_write", "file_read"},
			Dimension:    DimErrorRecovery,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				MustContain: []string{"result"},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "recovery_strategy", Description: "Did the agent handle the missing config gracefully by creating a default?", Weight: 0.5},
					{Name: "task_completion", Description: "Was the final result.txt created in the correct location?", Weight: 0.5},
				},
			},
		},
	}
}
