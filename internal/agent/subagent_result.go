package agent

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type SubAgentStatus string

const (
	StatusSuccess    SubAgentStatus = "success"
	StatusError      SubAgentStatus = "error"
	StatusTimeout    SubAgentStatus = "timeout"
	StatusBackground SubAgentStatus = "background"
)

type SubAgentResult struct {
	AgentName  string         `json:"agent_name"`
	Status     SubAgentStatus `json:"status"`
	Summary    string         `json:"summary"`
	Output     string         `json:"output"`
	Artifacts  []string       `json:"artifacts,omitempty"`
	Duration   time.Duration  `json:"duration"`
	TokensUsed int            `json:"tokens_used"`
	Error      string         `json:"error,omitempty"`
}

const subagentOutputInstruction = `

When you have completed the task, output your final response in this format:

<result>
<status>success|error</status>
<summary>One paragraph summary of what was accomplished</summary>
<artifacts>Comma-separated list of file paths, URLs, or key outputs (if any)</artifacts>
</result>
`

var (
	resultBlockRe = regexp.MustCompile(`(?s)<result>\s*(.*?)\s*</result>`)
	statusRe      = regexp.MustCompile(`(?s)<status>\s*(.*?)\s*</status>`)
	summaryRe     = regexp.MustCompile(`(?s)<summary>\s*(.*?)\s*</summary>`)
	artifactsRe   = regexp.MustCompile(`(?s)<artifacts>\s*(.*?)\s*</artifacts>`)
)

func extractStructuredResult(raw string) *SubAgentResult {
	block := resultBlockRe.FindStringSubmatch(raw)
	if len(block) < 2 {
		return nil
	}
	inner := block[1]

	result := &SubAgentResult{}

	if m := statusRe.FindStringSubmatch(inner); len(m) >= 2 {
		s := strings.TrimSpace(m[1])
		switch SubAgentStatus(s) {
		case StatusSuccess, StatusError:
			result.Status = SubAgentStatus(s)
		default:
			result.Status = StatusSuccess
		}
	} else {
		return nil
	}

	if m := summaryRe.FindStringSubmatch(inner); len(m) >= 2 {
		result.Summary = strings.TrimSpace(m[1])
	}

	if m := artifactsRe.FindStringSubmatch(inner); len(m) >= 2 {
		rawArtifacts := strings.TrimSpace(m[1])
		if rawArtifacts != "" {
			for _, a := range strings.Split(rawArtifacts, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					result.Artifacts = append(result.Artifacts, a)
				}
			}
		}
	}

	return result
}

func formatResultForParent(r *SubAgentResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Agent: %s | Status: %s | Duration: %s\n", r.AgentName, r.Status, r.Duration.Round(time.Millisecond))
	fmt.Fprintf(&sb, "Summary: %s\n", r.Summary)
	if len(r.Artifacts) > 0 {
		fmt.Fprintf(&sb, "Artifacts: %s\n", strings.Join(r.Artifacts, ", "))
	}
	if r.Error != "" {
		fmt.Fprintf(&sb, "Error: %s\n", r.Error)
	}
	return sb.String()
}
