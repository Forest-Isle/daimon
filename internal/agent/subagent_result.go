package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/mind"
)

type SubAgentStatus string

const (
	StatusSuccess    SubAgentStatus = "success"
	StatusError      SubAgentStatus = "error"
	StatusTimeout    SubAgentStatus = "timeout"
	StatusBackground SubAgentStatus = "background"
)

type SubAgentResult struct {
	AgentName string         `json:"agent_name"`
	Status    SubAgentStatus `json:"status"`
	// EpisodeStatus is the faithful Outcome.Status (done|blocked|handed_off|failed)
	// when the sub-agent ran as an episode; "" for the legacy LinearLoop path. The
	// coarse Status above projects it (done→success, else→error) so existing
	// consumers that key on StatusError are unchanged; EpisodeStatus preserves the
	// distinction for callers that want it (§4.3).
	EpisodeStatus string        `json:"episode_status,omitempty"`
	Summary       string        `json:"summary"`
	Output        string        `json:"output"`
	Artifacts     []string      `json:"artifacts,omitempty"`
	Duration      time.Duration `json:"duration"`
	TokensUsed    int           `json:"tokens_used"`
	Error         string        `json:"error,omitempty"`
}

// coarseStatusForEpisode projects a faithful episode Outcome.Status onto the
// binary SubAgentResult.Status. Only "failed" is a hard failure (StatusError) —
// it trips the parent's circuit breaker and returns an error. "done", "blocked",
// and "handed_off" are non-failures (StatusSuccess): blocked (needs user input)
// and handed_off (budget hit, continues via a follow-up episode) are legitimate
// non-completions, surfaced to the parent through EpisodeStatus rather than the
// error/breaker channel so they do not trip the breaker or return a blank error.
// (Distinct blocked/handed_off statuses are a deferred follow-up.)
func coarseStatusForEpisode(episodeStatus string) SubAgentStatus {
	if episodeStatus == "failed" {
		return StatusError
	}
	return StatusSuccess
}

const subagentOutputInstruction = `

When you have completed the task, output your final response as a JSON block:

` + "```json" + `
{
  "status": "success",
  "summary": "One paragraph summary of what was accomplished",
  "artifacts": ["file1.txt", "https://example.com/result"]
}
` + "```" + `

status must be "success" or "error".
`

var (
	resultBlockRe = regexp.MustCompile(`(?s)<result>\s*(.*?)\s*</result>`)
	statusRe      = regexp.MustCompile(`(?s)<status>\s*(.*?)\s*</status>`)
	summaryRe     = regexp.MustCompile(`(?s)<summary>\s*(.*?)\s*</summary>`)
	artifactsRe   = regexp.MustCompile(`(?s)<artifacts>\s*(.*?)\s*</artifacts>`)
	// markdown fence patterns that LLMs often wrap structured output in
	mdFencePatterns = []*regexp.Regexp{
		regexp.MustCompile("(?s)`{3,}(?:xml|json)?\\s*\\n?(.*?)\\n?`{3,}"),
		regexp.MustCompile("(?s)~~~(?:xml|json)?\\s*\\n?(.*?)\\n?~~~"),
	}
	// jsonBlockRe extracts a JSON object from text, handling nested braces
	subAgentJSONRe = regexp.MustCompile(`(?s)\{\s*"status"[\s\S]*"artifacts"\s*:\s*\[[\s\S]*?\][\s\S]*?\}`)
)

func extractStructuredResult(raw string) *SubAgentResult {
	// Stage 0: strip markdown code fences
	cleaned := raw
	for _, pattern := range mdFencePatterns {
		if m := pattern.FindStringSubmatch(cleaned); len(m) >= 2 {
			cleaned = strings.TrimSpace(m[1])
			break
		}
	}

	// Stage 1: try JSON parsing (primary format — much more reliable than XML)
	if result := extractJSONResult(cleaned); result != nil {
		return result
	}

	// Stage 2: fall back to legacy XML regex parsing
	if result := extractXMLResult(cleaned); result != nil {
		return result
	}

	// Stage 3: try JSON in the original raw text (without fence stripping)
	if cleaned != raw {
		if result := extractJSONResult(raw); result != nil {
			return result
		}
	}

	return nil
}

// extractJSONResult attempts to parse a JSON-format sub-agent result.
func extractJSONResult(text string) *SubAgentResult {
	m := subAgentJSONRe.FindString(text)
	if m == "" {
		return nil
	}
	var parsed struct {
		Status    string   `json:"status"`
		Summary   string   `json:"summary"`
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(m), &parsed); err != nil {
		return nil
	}
	if parsed.Status == "" {
		return nil
	}
	status := StatusSuccess
	if parsed.Status == "error" {
		status = StatusError
	}
	return &SubAgentResult{
		Status:    status,
		Summary:   parsed.Summary,
		Artifacts: parsed.Artifacts,
	}
}

// extractXMLResult parses legacy <result> XML format.
func extractXMLResult(cleaned string) *SubAgentResult {
	block := resultBlockRe.FindStringSubmatch(cleaned)
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

func summarizeWithLLM(ctx context.Context, provider mind.Provider, model string, agentName string, rawOutput string) *SubAgentResult {
	truncated := rawOutput
	if len(truncated) > 4000 {
		truncated = truncated[:4000] + "\n...(truncated)"
	}

	prompt := fmt.Sprintf(
		"Summarize this agent output into JSON with fields: status (\"success\" or \"error\"), summary (1 paragraph), artifacts (array of file paths or URLs, empty array if none).\n\nAgent: %s\nOutput:\n%s",
		agentName, truncated)

	req := mind.CompletionRequest{
		Model:     model,
		System:    "You extract structured summaries from agent outputs. Respond with JSON only.",
		Messages:  []mind.CompletionMessage{{Role: "user", Content: prompt}},
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
	if r.EpisodeStatus != "" && r.EpisodeStatus != "done" {
		// Surface a non-done episode status (blocked/handed_off/failed) so the parent
		// model sees the sub-agent did not fully complete, not just a coarse "error".
		fmt.Fprintf(&sb, "Episode status: %s\n", r.EpisodeStatus)
	}
	fmt.Fprintf(&sb, "Summary: %s\n", r.Summary)
	if len(r.Artifacts) > 0 {
		fmt.Fprintf(&sb, "Artifacts: %s\n", strings.Join(r.Artifacts, ", "))
	}
	if r.Error != "" {
		fmt.Fprintf(&sb, "Error: %s\n", r.Error)
	}
	return sb.String()
}
