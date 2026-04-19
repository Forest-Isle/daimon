package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

func summarizeWithLLM(ctx context.Context, provider Provider, model string, agentName string, rawOutput string) *SubAgentResult {
	truncated := rawOutput
	if len(truncated) > 4000 {
		truncated = truncated[:4000] + "\n...(truncated)"
	}

	prompt := fmt.Sprintf(
		"Summarize this agent output into JSON with fields: status (\"success\" or \"error\"), summary (1 paragraph), artifacts (array of file paths or URLs, empty array if none).\n\nAgent: %s\nOutput:\n%s",
		agentName, truncated)

	req := CompletionRequest{
		Model:     model,
		System:    "You extract structured summaries from agent outputs. Respond with JSON only.",
		Messages:  []CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 256,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		slog.Warn("subagent: LLM summarization failed", "agent", agentName, "error", err)
		return nil
	}

	var parsed struct {
		Status    string   `json:"status"`
		Summary   string   `json:"summary"`
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
		slog.Warn("subagent: failed to parse LLM summary JSON", "agent", agentName, "error", err)
		return nil
	}

	status := StatusSuccess
	if parsed.Status == "error" {
		status = StatusError
	}

	return &SubAgentResult{
		AgentName: agentName,
		Status:    status,
		Summary:   parsed.Summary,
		Artifacts: parsed.Artifacts,
	}
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
