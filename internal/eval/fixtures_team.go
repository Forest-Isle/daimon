package eval

// MultiAgentSuite returns tasks that evaluate multi-agent collaboration —
// task decomposition, parallel execution, failure isolation, and result merging.
func MultiAgentSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "team-split-merge",
			Goal:         "Split this task between two workers: Worker A writes 'part-a' to /tmp/ironclaw_eval_team/a.txt, Worker B writes 'part-b' to /tmp/ironclaw_eval_team/b.txt. Then merge both parts into /tmp/ironclaw_eval_team/merged.txt. Create the directory first.",
			Complexity:   "complex",
			Tags:         []string{"multi-agent", "split-merge"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimMultiAgent,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_team/a.txt", MustExist: true, Contains: "part-a"},
					{Path: "/tmp/ironclaw_eval_team/b.txt", MustExist: true, Contains: "part-b"},
					{Path: "/tmp/ironclaw_eval_team/merged.txt", MustExist: true, Contains: "part-a"},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "decomposition", Description: "Did the agent attempt to split work into sub-tasks?", Weight: 0.4},
					{Name: "merge_quality", Description: "Does merged.txt contain content from both parts?", Weight: 0.3},
					{Name: "completeness", Description: "Were all three files created correctly?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "team-specialist",
			Goal:         "Complete two specialized tasks: (1) A 'coder' writes a Python script at /tmp/ironclaw_eval_spec/calc.py that defines a function add(a,b) returning a+b. (2) A 'tester' writes a test at /tmp/ironclaw_eval_spec/test_calc.py that imports calc and asserts add(2,3)==5. Then run the test.",
			Complexity:   "complex",
			Tags:         []string{"multi-agent", "specialist"},
			ExpectTools:  []string{"file_write", "bash"},
			Dimension:    DimMultiAgent,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_spec/calc.py", MustExist: true, Contains: "def add"},
					{Path: "/tmp/ironclaw_eval_spec/test_calc.py", MustExist: true, Contains: "add(2"},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "role_separation", Description: "Did the agent treat these as separate specialist roles?", Weight: 0.3},
					{Name: "interface_design", Description: "Do the two files have a clean interface (import works)?", Weight: 0.4},
					{Name: "test_passes", Description: "Does the test actually pass when run?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "team-failure-isolate",
			Goal:         "Run two independent tasks: Task A writes 'success-a' to /tmp/ironclaw_eval_iso/a.txt. Task B tries to write to /root/ironclaw_eval_iso/b.txt (will fail due to permissions). Task A's result must not be affected by Task B's failure.",
			Complexity:   "moderate",
			Tags:         []string{"multi-agent", "failure-isolation"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimMultiAgent,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_iso/a.txt", MustExist: true, Contains: "success-a"},
				},
			},
		},
		{
			ID:           "team-dependency",
			Goal:         "Execute in order: Step 1 writes '42' to /tmp/ironclaw_eval_dep/input.txt. Step 2 reads that file and writes 'result: <value*2>' to /tmp/ironclaw_eval_dep/output.txt (so it should be 'result: 84'). Step 2 depends on Step 1's output.",
			Complexity:   "moderate",
			Tags:         []string{"multi-agent", "dependency"},
			ExpectTools:  []string{"file_write", "file_read", "bash"},
			Dimension:    DimMultiAgent,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"84"},
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_dep/output.txt", MustExist: true, Contains: "84"},
				},
			},
		},
		{
			ID:           "team-parallel-efficiency",
			Goal:         "Create three independent files as fast as possible: /tmp/ironclaw_eval_eff/f1.txt with 'data1', /tmp/ironclaw_eval_eff/f2.txt with 'data2', /tmp/ironclaw_eval_eff/f3.txt with 'data3'. Since these are independent, they could be done in parallel. Create the directory first.",
			Complexity:   "moderate",
			Tags:         []string{"multi-agent", "parallel"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimMultiAgent,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_eff/f1.txt", MustExist: true, Contains: "data1"},
					{Path: "/tmp/ironclaw_eval_eff/f2.txt", MustExist: true, Contains: "data2"},
					{Path: "/tmp/ironclaw_eval_eff/f3.txt", MustExist: true, Contains: "data3"},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "parallel_awareness", Description: "Did the agent recognize these are independent and could be parallelized?", Weight: 0.5},
					{Name: "completeness", Description: "Were all three files created correctly?", Weight: 0.5},
				},
			},
		},
		{
			ID:           "team-result-conflict",
			Goal:         "Two workers estimate the number of lines in the Go source files of this project. Worker A uses 'find . -name \"*.go\" | xargs wc -l'. Worker B uses 'rg --files -g \"*.go\" | wc -l' to count files, then estimates 50 lines per file. If they disagree, explain which is more accurate and why.",
			Complexity:   "complex",
			Tags:         []string{"multi-agent", "conflict-resolution"},
			ExpectTools:  []string{"bash"},
			Dimension:    DimMultiAgent,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "both_methods", Description: "Did the agent attempt both estimation methods?", Weight: 0.3},
					{Name: "conflict_resolution", Description: "Did it identify Worker A's method as more accurate (exact count vs estimate)?", Weight: 0.4},
					{Name: "reasoning", Description: "Was the reasoning about why they differ clear and correct?", Weight: 0.3},
				},
			},
		},
	}
}
