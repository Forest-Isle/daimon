package eval

// ToolSelectionSuite returns tasks that evaluate whether the agent selects
// the most appropriate tool for each situation.
func ToolSelectionSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "tool-read-vs-bash",
			Goal:         "Read the contents of /tmp/ironclaw_eval_toolsel.txt. First create it with 'tool selection test content'.",
			Complexity:   "simple",
			Tags:         []string{"tool-selection", "file_read"},
			ExpectTools:  []string{"file_write", "file_read"},
			Dimension:    DimToolSelection,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "tool_choice", Description: "Did the agent use file_read (preferred) rather than bash cat to read the file?", Weight: 0.7},
					{Name: "correctness", Description: "Was the file content returned correctly?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "tool-search-method",
			Goal:         "Find all TODO comments in the Go source files of this project. Report how many you found.",
			Complexity:   "moderate",
			Tags:         []string{"tool-selection", "search", "efficiency"},
			ExpectTools:  []string{"bash"},
			Dimension:    DimToolSelection,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "tool_choice", Description: "Did the agent use an efficient search method (grep/rg) rather than reading files one by one?", Weight: 0.6},
					{Name: "completeness", Description: "Did the agent report a count or list of TODOs found?", Weight: 0.4},
				},
			},
		},
		{
			ID:           "tool-write-vs-edit",
			Goal:         "First write a 10-line file at /tmp/ironclaw_eval_edit.txt with lines 'line1' through 'line10'. Then change only 'line5' to 'MODIFIED'. Do not rewrite the entire file.",
			Complexity:   "moderate",
			Tags:         []string{"tool-selection", "precision"},
			ExpectTools:  []string{"file_write", "bash"},
			Dimension:    DimToolSelection,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_edit.txt", MustExist: true, Contains: "MODIFIED"},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "precision", Description: "Did the agent use a targeted edit (sed/file_edit) rather than rewriting the full file?", Weight: 0.6},
					{Name: "correctness", Description: "Does the file contain MODIFIED on line 5 and all other lines intact?", Weight: 0.4},
				},
			},
		},
		{
			ID:           "tool-overkill",
			Goal:         "Print 'hello world' to the console.",
			Complexity:   "simple",
			Tags:         []string{"tool-selection", "simplicity"},
			ExpectTools:  []string{"bash"},
			Dimension:    DimToolSelection,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "simplicity", Description: "Did the agent use a single simple tool call (e.g., one bash echo) without unnecessary complexity?", Weight: 0.7},
					{Name: "correctness", Description: "Was 'hello world' printed?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "tool-http-vs-browser",
			Goal:         "Fetch the JSON data from https://httpbin.org/get and extract the 'origin' field from the response.",
			Complexity:   "moderate",
			Tags:         []string{"tool-selection", "http"},
			ExpectTools:  []string{"http"},
			Dimension:    DimToolSelection,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "tool_choice", Description: "Did the agent use http tool (or curl) rather than browser_extract for this API endpoint?", Weight: 0.6},
					{Name: "correctness", Description: "Was the origin IP address extracted from the response?", Weight: 0.4},
				},
			},
		},
		{
			ID:           "tool-composite",
			Goal:         "Create a backup workflow: (1) use file_write to create /tmp/ironclaw_eval_backup/data.txt with 'original data', (2) use bash to copy it to data.bak, (3) use file_read to verify the backup exists and matches.",
			Complexity:   "moderate",
			Tags:         []string{"tool-selection", "composite"},
			ExpectTools:  []string{"file_write", "bash", "file_read"},
			Dimension:    DimToolSelection,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_backup/data.txt", MustExist: true, Contains: "original data"},
					{Path: "/tmp/ironclaw_eval_backup/data.bak", MustExist: true, Contains: "original data"},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "tool_variety", Description: "Did the agent use all three expected tools (file_write, bash, file_read)?", Weight: 0.5},
					{Name: "correctness", Description: "Do both files exist with matching content?", Weight: 0.5},
				},
			},
		},
	}
}
