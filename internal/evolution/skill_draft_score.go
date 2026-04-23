package evolution

import (
	"regexp"
	"strings"
)

// ScoreSkillDraftMarkdown scores a generated SKILL.md body (0–1) using
// heuristics aligned with the post-refactor synthesizer: task context,
// structured sections, and rejection of "bash → bash → bash" spam.
func ScoreSkillDraftMarkdown(content string) (score float64, passed []string, failed []string) {
	if strings.TrimSpace(content) == "" {
		return 0, nil, []string{"empty content"}
	}
	lower := strings.ToLower(content)
	const w = 1.0 / 6.0
	var p float64

	add := func(name string, ok bool) {
		if ok {
			p += w
			passed = append(passed, name)
		} else {
			failed = append(failed, name)
		}
	}

	add("yaml_front_matter", strings.HasPrefix(strings.TrimSpace(content), "---") && strings.Contains(content, "name:"))
	add("auto_mark", strings.Contains(lower, "source: evolution") || strings.Contains(lower, "auto_generated: true") ||
		strings.Contains(lower, "evolution_confidence:"))
	add("section_task_narrative", strings.Contains(lower, "## what this captures") || strings.Contains(lower, "## when to use") ||
		strings.Contains(content, "Task workflow distilled"))
	add("section_procedure", strings.Contains(lower, "## suggested procedure") || strings.Contains(lower, "## procedure") ||
		strings.Contains(strings.ToLower(content), "clarify the deliverable"))
	add("user_goal_block", strings.Contains(lower, "user goal") || strings.Contains(lower, "most recent session"))
	if !repeatedBashFlowSpam(content) {
		p += w
		passed = append(passed, "no_repeated_bash_spam")
	} else {
		failed = append(failed, "repeated_bash_in_flow")
	}
	if p > 1.0 {
		p = 1.0
	}
	return p, passed, failed
}

var bashRepeatFlow = regexp.MustCompile(`(?i)bash(\s*[-→>]\s*bash){2,}`)

func repeatedBashFlowSpam(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "`") {
			continue
		}
		inner := strings.Trim(trim, "`")
		if bashRepeatFlow.MatchString(inner) {
			return true
		}
	}
	return false
}
