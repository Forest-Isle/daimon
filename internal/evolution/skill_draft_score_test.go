package evolution

import "testing"

func TestScoreSkillDraftMarkdown_GoodHeuristic(t *testing.T) {
	const content = `
---
name: auto_test
description: Task workflow distilled from 3 successful episodes (avg reward 0.86) using tools: a, b.
status: draft
auto_generated: true
source: evolution
---

# auto_test

## What this captures

This draft was synthesized from **repeated successful runs** of a multi-tool workflow.

## User goal (most recent session that matched this pattern)

> Fix CI

## When to use

Apply when the task requires working with these tools together.

## Suggested procedure

1. Clarify the deliverable and constraints from the user request.
2. Follow a phase that matches this collapsed tool flow (duplicates removed):

   file_read → bash → file_write

3. Verify results before declaring done.

## Notes from prior reflections

- note

## Evidence (automation)

- **Occurrences:** 3
`
	score, _, fails := ScoreSkillDraftMarkdown(content)
	if score < 0.95 {
		t.Fatalf("good draft score=%f, fails=%v", score, fails)
	}
}

func TestScoreSkillDraftMarkdown_RepeatedBashFails(t *testing.T) {
	content := "---\nname: bad\n---\n## x\n`bash → bash → bash → bash`\n"
	score, _, _ := ScoreSkillDraftMarkdown(content)
	if score > 0.5 {
		t.Fatalf("expected low score for bash spam, got %f", score)
	}
}
