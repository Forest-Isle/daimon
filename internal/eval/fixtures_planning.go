package eval

// PlanningSuite returns tasks that evaluate the agent's planning and task
// decomposition capabilities.
func PlanningSuite() []TaskCase {
	return []TaskCase{
		{
			ID:           "plan-dep-chain",
			Goal:         "Create three files in /tmp/ironclaw_eval_plan/: c.txt containing 'base', b.txt containing the content of c.txt reversed, and a.txt containing the line count of b.txt. Files must be created in dependency order (c first, then b, then a).",
			Complexity:   "complex",
			Tags:         []string{"planning", "dependency"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_plan/c.txt", MustExist: true, Contains: "base"},
					{Path: "/tmp/ironclaw_eval_plan/b.txt", MustExist: true},
					{Path: "/tmp/ironclaw_eval_plan/a.txt", MustExist: true},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "dependency_order", Description: "Were files created in correct dependency order (c->b->a)?", Weight: 0.5},
					{Name: "correctness", Description: "Do file contents match the specification?", Weight: 0.5},
				},
			},
		},
		{
			ID:           "plan-parallel-tasks",
			Goal:         "Create three independent files simultaneously: /tmp/ironclaw_eval_par/hosts.txt with the system hostname, /tmp/ironclaw_eval_par/date.txt with today's date, and /tmp/ironclaw_eval_par/user.txt with the current username. These tasks are independent and could be done in any order.",
			Complexity:   "complex",
			Tags:         []string{"planning", "parallel"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_par/hosts.txt", MustExist: true},
					{Path: "/tmp/ironclaw_eval_par/date.txt", MustExist: true},
					{Path: "/tmp/ironclaw_eval_par/user.txt", MustExist: true},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "completeness", Description: "Were all three files created with correct content?", Weight: 0.5},
					{Name: "efficiency", Description: "Did the agent recognize these are independent tasks?", Weight: 0.5},
				},
			},
		},
		{
			ID:           "plan-constraint-sat",
			Goal:         "Create a text file at /tmp/ironclaw_eval_constraint.txt that satisfies ALL of these constraints: (1) contains exactly 5 lines, (2) each line has at most 20 characters, (3) the file contains the word 'hello', (4) the file does NOT contain the word 'world'.",
			Complexity:   "complex",
			Tags:         []string{"planning", "constraints"},
			ExpectTools:  []string{"file_write", "bash"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain:    []string{"hello"},
				MustNotContain: []string{"world"},
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_constraint.txt", MustExist: true, Contains: "hello"},
				},
			},
		},
		{
			ID:           "plan-multi-file-refactor",
			Goal:         "Create three files in /tmp/ironclaw_eval_refactor/: main.txt containing 'call greet_user', helper.txt containing 'func greet_user: hello', and test.txt containing 'test greet_user'. Then rename the function from 'greet_user' to 'welcome_user' in ALL three files consistently.",
			Complexity:   "complex",
			Tags:         []string{"planning", "refactor", "consistency"},
			ExpectTools:  []string{"file_write", "bash"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain:    []string{"welcome_user"},
				MustNotContain: []string{"greet_user"},
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_refactor/main.txt", MustExist: true, Contains: "welcome_user"},
					{Path: "/tmp/ironclaw_eval_refactor/helper.txt", MustExist: true, Contains: "welcome_user"},
					{Path: "/tmp/ironclaw_eval_refactor/test.txt", MustExist: true, Contains: "welcome_user"},
				},
			},
		},
		{
			ID:           "plan-incremental",
			Goal:         "Build a CSV report step by step: (1) create /tmp/ironclaw_eval_inc.csv with header 'name,score', (2) add rows Alice,95 and Bob,87, (3) verify it has 3 lines, (4) add a total row 'Total,182', (5) verify the final file has 4 lines and contains 'Total'.",
			Complexity:   "moderate",
			Tags:         []string{"planning", "incremental"},
			ExpectTools:  []string{"file_write", "bash", "file_read"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyDeterministic,
			Reference: &Reference{
				MustContain: []string{"Total", "182"},
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_inc.csv", MustExist: true, Contains: "Total,182"},
				},
			},
		},
		{
			ID:           "plan-ambiguous-goal",
			Goal:         "Make this project better.",
			Complexity:   "complex",
			Tags:         []string{"planning", "ambiguous"},
			ExpectTools:  []string{"bash", "file_read"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyLLMJudge,
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "clarification", Description: "Did the agent ask for clarification or explain its interpretation before acting?", Weight: 0.4},
					{Name: "reasoning", Description: "Did the agent reason about what 'better' means in context?", Weight: 0.3},
					{Name: "safety", Description: "Did the agent avoid making destructive or unnecessary changes?", Weight: 0.3},
				},
			},
		},
		{
			ID:           "plan-resource-limit",
			Goal:         "Find the total number of .go files in this project and write the count to /tmp/ironclaw_eval_gocount.txt. Complete this using at most 3 tool calls.",
			Complexity:   "moderate",
			Tags:         []string{"planning", "efficiency"},
			ExpectTools:  []string{"bash"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_gocount.txt", MustExist: true},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "efficiency", Description: "Was the task completed in 3 or fewer tool calls?", Weight: 0.6},
					{Name: "correctness", Description: "Is the .go file count accurate?", Weight: 0.4},
				},
			},
		},
		{
			ID:           "plan-priority-order",
			Goal:         "Execute these tasks in priority order (highest first): (P3) write 'low' to /tmp/ironclaw_eval_pri/low.txt, (P1) write 'critical' to /tmp/ironclaw_eval_pri/critical.txt, (P2) write 'medium' to /tmp/ironclaw_eval_pri/medium.txt. Create the directory first.",
			Complexity:   "moderate",
			Tags:         []string{"planning", "priority"},
			ExpectTools:  []string{"bash", "file_write"},
			Dimension:    DimPlanning,
			VerifyMethod: VerifyHybrid,
			Reference: &Reference{
				FileChecks: []FileCheck{
					{Path: "/tmp/ironclaw_eval_pri/critical.txt", MustExist: true, Contains: "critical"},
					{Path: "/tmp/ironclaw_eval_pri/medium.txt", MustExist: true, Contains: "medium"},
					{Path: "/tmp/ironclaw_eval_pri/low.txt", MustExist: true, Contains: "low"},
				},
			},
			Rubric: &Rubric{
				Criteria: []JudgeCriterion{
					{Name: "priority_order", Description: "Were tasks executed in P1->P2->P3 order?", Weight: 0.6},
					{Name: "completeness", Description: "Were all three files created correctly?", Weight: 0.4},
				},
			},
		},
	}
}
