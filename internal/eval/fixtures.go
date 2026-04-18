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
