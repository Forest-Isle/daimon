package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/memory"
)

func (gw *Gateway) handleMemory(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.memory == nil || gw.memory.Store() == nil {
		return "Memory is not available.", nil
	}
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(msg.Text, "/memory")))
	if len(args) == 0 || args[0] == "list" {
		entries, err := gw.memory.Store().ListByScope(ctx, memory.ScopeUser, "")
		if err != nil {
			return "", fmt.Errorf("list memories: %w", err)
		}
		if len(entries) == 0 {
			return "No user memories found.", nil
		}
		if len(entries) > 20 {
			entries = entries[:20]
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**User Memories** — showing %d\n\n", len(entries))
		for i, e := range entries {
			fmt.Fprintf(&b, "%d. `%s` %s\n", i+1, truncateID(e.ID), compactLine(e.Content, 140))
		}
		return b.String(), nil
	}

	switch args[0] {
	case "search":
		query := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(msg.Text, "/memory")), "search"))
		if query == "" {
			return "Usage: /memory search <query>", nil
		}
		results, err := gw.memory.Store().Search(ctx, memory.SearchQuery{Text: query, Limit: 10})
		if err != nil {
			return "", fmt.Errorf("search memories: %w", err)
		}
		if len(results) == 0 {
			return "No matching memories found.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Memory Search** — %d result(s)\n\n", len(results))
		for i, r := range results {
			fmt.Fprintf(&b, "%d. `%s` %s (%.2f)\n", i+1, truncateID(r.Entry.ID), compactLine(r.Entry.Content, 140), r.Score)
		}
		return b.String(), nil

	case "clear":
		entries, err := gw.memory.Store().ListByScope(ctx, memory.ScopeUser, "")
		if err != nil {
			return "", fmt.Errorf("list memories: %w", err)
		}
		for _, e := range entries {
			if err := gw.memory.Store().Delete(ctx, e.ID); err != nil {
				return "", fmt.Errorf("delete memory %s: %w", e.ID, err)
			}
		}
		return fmt.Sprintf("Cleared %d user memories.", len(entries)), nil
	}

	return "Usage: /memory [list|search|clear] [query]", nil
}

func (gw *Gateway) handleSkills(_ context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.skills == nil || gw.skills.Manager == nil {
		return "Skills are not available.", nil
	}
	args := slashArgs(msg.Text)
	if len(args) == 0 || args[0] == "list" {
		skills := gw.skills.Manager.All()
		if len(skills) == 0 {
			return "No skills loaded.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Skills** — %d loaded\n\n", len(skills))
		for _, s := range skills {
			fmt.Fprintf(&b, "- **%s**", s.Name)
			if s.Version != "" {
				fmt.Fprintf(&b, " (v%s)", s.Version)
			}
			if s.Description != "" {
				fmt.Fprintf(&b, ": %s", s.Description)
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	}
	if args[0] == "read" && len(args) == 2 {
		content, err := gw.skills.Manager.GetContent(args[1])
		if err != nil {
			return "", err
		}
		return content, nil
	}
	return "Usage: /skills [list|read <name>]", nil
}

func (gw *Gateway) handleTasks(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	if gw.taskLedger == nil {
		return "Task ledger is not available.", nil
	}
	entries, err := gw.taskLedger.List(ctx, 20)
	if err != nil {
		return "", fmt.Errorf("list task ledger: %w", err)
	}
	if len(entries) == 0 {
		return "No task ledger entries.", nil
	}

	var b strings.Builder
	b.WriteString("**Task Ledger**\n\n")
	for _, entry := range entries {
		title := firstNonEmptyLine(entry.Metadata.Goal, entry.Title, entry.Description)
		fmt.Fprintf(&b, "- `%s` **%s** [%s] %s", truncateID(entry.ID), entry.State, entry.Kind, compactLine(title, 120))
		if entry.Assignee != "" {
			fmt.Fprintf(&b, " @%s", entry.Assignee)
		}
		if entry.Metadata.NextAction != "" {
			fmt.Fprintf(&b, "\n  next: %s", compactLine(entry.Metadata.NextAction, 120))
		}
		if len(entry.Metadata.Evidence) > 0 {
			fmt.Fprintf(&b, "\n  evidence: %d item(s)", len(entry.Metadata.Evidence))
		}
		if entry.UpdatedAt != "" {
			fmt.Fprintf(&b, "\n  updated: %s", entry.UpdatedAt)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (gw *Gateway) handleResume(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.taskLedger == nil {
		return "Task checkpoints are not available.", nil
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/resume"))
	if sessionID == "" {
		sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
		if err != nil {
			return "", fmt.Errorf("get current session: %w", err)
		}
		sessionID = sess.ID
	}
	cp, err := gw.taskLedger.GetCheckpoint(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Sprintf("No checkpoint found for session %s.", sessionID), nil
		}
		return "", fmt.Errorf("get checkpoint: %w", err)
	}
	observations := "No observations captured."
	if len(cp.Observations) > 0 {
		var lines []string
		for _, obs := range cp.Observations {
			lines = append(lines, "- "+compactLine(obs, 180))
		}
		observations = strings.Join(lines, "\n")
	}
	return fmt.Sprintf("Checkpoint for `%s` from %s\n\nPlan: %s\n\nObservations:\n%s",
		sessionID, cp.CreatedAt, compactLine(cp.PlanJSON, 500), observations), nil
}

func (gw *Gateway) handleTeam(_ context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	args := slashArgs(msg.Text)
	if len(args) > 0 {
		switch args[0] {
		case "list":
			// Fall through to the default agent list below.
		case "status":
			return gw.teamBackgroundStatus(), nil
		case "cancel":
			if len(args) < 2 {
				return "Usage: /team cancel <background_agent_id>", nil
			}
			return gw.teamBackgroundCancel(args[1])
		case "attach", "result":
			if len(args) < 2 {
				return "Usage: /team attach <background_agent_id>", nil
			}
			return gw.teamBackgroundAttach(args[1])
		case "help":
			return "Usage: /team [list|status|attach <id>|cancel <id>]", nil
		default:
			return fmt.Sprintf("Unknown team sub-command: %s. Try /team help.", args[0]), nil
		}
	}

	if gw.multiAgent == nil || gw.multiAgent.AgentMgr == nil {
		return "Agent team is not available.", nil
	}
	agents := gw.multiAgent.AgentMgr.All()
	if len(agents) == 0 {
		return "No sub-agents configured.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Agent Team** — %d available\n\n", len(agents))
	for _, spec := range agents {
		fmt.Fprintf(&b, "- **agent_%s**: %s", spec.Name, spec.Description)
		if spec.ExecutionMode != "" {
			fmt.Fprintf(&b, " [%s]", spec.ExecutionMode)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nUse the corresponding `agent_*` tool during an agent conversation to delegate work.\n")
	b.WriteString("Background controls: `/team status`, `/team attach <id>`, `/team cancel <id>`.")
	return b.String(), nil
}

func (gw *Gateway) teamBackgroundStatus() string {
	bm := gw.backgroundManager()
	if bm == nil {
		return "Background agents are not available."
	}
	statuses := bm.List()
	if len(statuses) == 0 {
		return "No background agents."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Background Agents** — %d active or retained\n\n", len(statuses))
	for _, status := range statuses {
		fmt.Fprintf(&b, "- `%s` **%s**", status.AgentID, status.State)
		if status.AgentName != "" {
			fmt.Fprintf(&b, " agent_%s", status.AgentName)
		}
		if !status.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, " updated %s", status.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (gw *Gateway) teamBackgroundCancel(agentID string) (string, error) {
	bm := gw.backgroundManager()
	if bm == nil {
		return "Background agents are not available.", nil
	}
	if err := bm.Cancel(agentID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cancellation requested for `%s`.", agentID), nil
}

func (gw *Gateway) teamBackgroundAttach(agentID string) (string, error) {
	bm := gw.backgroundManager()
	if bm == nil {
		return "Background agents are not available.", nil
	}
	if result, done := bm.GetResult(agentID); done {
		return formatBackgroundResult(agentID, result), nil
	}
	for _, status := range bm.List() {
		if status.AgentID == agentID {
			return fmt.Sprintf("Background agent `%s` is still %s.", agentID, status.State), nil
		}
	}
	return fmt.Sprintf("Unknown background agent: %s", agentID), nil
}

func (gw *Gateway) backgroundManager() *agent.BackgroundManager {
	if gw.multiAgent == nil {
		return nil
	}
	return gw.multiAgent.BgManager
}

func formatBackgroundResult(agentID string, result *agent.AgentResult) string {
	if result == nil {
		return fmt.Sprintf("Background agent `%s` finished with no result.", agentID)
	}
	if result.Error != nil {
		return fmt.Sprintf("Background agent `%s` failed: %v", agentID, result.Error)
	}
	output := compactLine(result.Output, 1200)
	if output == "" {
		output = "No output."
	}
	return fmt.Sprintf("Background agent `%s` completed.\n\n%s", agentID, output)
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func compactLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 3 && len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func slashArgs(text string) []string {
	fields := strings.Fields(text)
	if len(fields) <= 1 {
		return nil
	}
	return fields[1:]
}

func firstNonEmptyLine(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
