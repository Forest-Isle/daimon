package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/feature"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
	"github.com/Forest-Isle/IronClaw/internal/util"
)

// handleTasks lists running and pending tasks from the task ledger.
func (gw *Gateway) handleTasks(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.tasks.TaskLedger() == nil {
		return "Task ledger not available.", nil
	}

	running := taskledger.TaskStateRunning
	runningTasks, err := gw.tasks.TaskLedger().List(ctx, taskledger.TaskFilter{State: &running})
	if err != nil {
		return "", fmt.Errorf("failed to list tasks: %w", err)
	}

	pending := taskledger.TaskStatePending
	pendingTasks, err := gw.tasks.TaskLedger().List(ctx, taskledger.TaskFilter{State: &pending})
	if err != nil {
		return "", fmt.Errorf("failed to list tasks: %w", err)
	}

	var b strings.Builder
	b.WriteString("**Task Ledger**\n\n")

	if len(runningTasks) == 0 && len(pendingTasks) == 0 {
		b.WriteString("No active tasks.")
	} else {
		if len(runningTasks) > 0 {
			fmt.Fprintf(&b, "Running (%d):\n", len(runningTasks))
			for _, t := range runningTasks {
				age := time.Since(t.CreatedAt).Truncate(time.Second)
				fmt.Fprintf(&b, "  ▶ [%s] %s (%s ago)\n", t.Kind, t.Title, age)
			}
		}
		if len(pendingTasks) > 0 {
			if len(runningTasks) > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "Pending (%d):\n", len(pendingTasks))
			for _, t := range pendingTasks {
				fmt.Fprintf(&b, "  ○ [%s] %s\n", t.Kind, t.Title)
			}
		}
	}

	return b.String(), nil
}

// handleTeam breaks a goal into parallel tasks using the LLM and executes them.
func (gw *Gateway) handleTeam(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	goal := strings.TrimPrefix(msg.Text, "/team ")
	goal = strings.TrimSpace(goal)

	if gw.tasks.TeamCoordinator() == nil {
		return "Team mode is not enabled. Set agent.team.enabled: true in config.", nil
	}

	prompt := fmt.Sprintf(taskledger.TeamPlanPrompt, goal)
	req := agent.CompletionRequest{
		Model:     gw.agent.Model(),
		System:    "You are a task planning assistant. Output only valid JSON.",
		Messages:  []agent.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: gw.Config().LLM.MaxTokens,
	}
	resp, err := gw.provider.Complete(ctx, req)
	if err != nil {
		return fmt.Sprintf("Failed to generate plan: %v", err), nil
	}

	rootID := fmt.Sprintf("team_%d", time.Now().UnixNano())
	rootTask := taskledger.Task{
		ID:    rootID,
		Kind:  taskledger.TaskKindTeamTask,
		State: taskledger.TaskStateRunning,
		Title: util.TruncateStr(goal, 100),
	}
	if err := gw.tasks.TaskLedger().Register(ctx, rootTask); err != nil {
		return fmt.Sprintf("Failed to register root task: %v", err), nil
	}

	tasks, err := taskledger.ParseTaskPlan(resp.Text, rootID)
	if err != nil {
		return fmt.Sprintf("Failed to parse plan: %v", err), nil
	}

	for _, t := range tasks {
		if err := gw.tasks.TeamCoordinator().AddTask(ctx, t); err != nil {
			return fmt.Sprintf("Failed to add task %s: %v", t.ID, err), nil
		}
	}

	result, err := gw.tasks.TeamCoordinator().RunWithExecutor(ctx)
	if err != nil {
		return fmt.Sprintf("Team execution failed: %v", err), nil
	}

	now := time.Now().UTC()
	rootTask.State = taskledger.TaskStateCompleted
	rootTask.CompletedAt = &now
	rootTask.Result = result.Summary
	if err := gw.tasks.TaskLedger().Update(ctx, rootTask); err != nil {
		slog.Warn("gateway: failed to update root task", "err", err)
	}

	return fmt.Sprintf("Team completed: %d tasks done, %d failed", result.TasksCompleted, result.TasksFailed), nil
}

// handleMode processes the /mode command.
func (gw *Gateway) handleMode(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	arg := strings.TrimPrefix(msg.Text, "/mode")
	arg = strings.TrimSpace(arg)

	current := gw.CurrentMode()
	if arg == "" {
		return fmt.Sprintf("Mode: %s", current), nil
	}
	if arg != "simple" && arg != "cognitive" {
		return fmt.Sprintf("Error: unknown mode %q. Valid modes: simple, cognitive", arg), nil
	}
	if arg == current {
		return fmt.Sprintf("Already in %s mode", current), nil
	}
	if err := gw.SetMode(arg); err != nil {
		slog.Warn("gateway: set mode failed", "mode", arg, "err", err)
	}
	return fmt.Sprintf("Mode switched to %s (was: %s)", arg, current), nil
}

// handleFeature processes /feature [list|enable|disable] [name].
func (gw *Gateway) handleFeature(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	args := strings.TrimPrefix(msg.Text, "/feature")
	args = strings.TrimSpace(args)

	if gw.features == nil {
		return "Feature registry not initialized.", nil
	}

	switch {
	case args == "" || args == "list":
		return gw.featureListString(), nil

	case strings.HasPrefix(args, "enable "):
		name := strings.TrimSpace(strings.TrimPrefix(args, "enable "))
		if err := gw.features.Enable(ctx, name); err != nil {
			return "", err
		}
		gw.persistFeatureState()
		reply := fmt.Sprintf("Feature %q enabled.", name)
		if info := gw.findFeatureInfo(name); info != nil && !info.HotReloadable {
			reply += "\nNote: not hot-reloadable — restart IronClaw for full effect."
		}
		return reply, nil

	case strings.HasPrefix(args, "disable "):
		name := strings.TrimSpace(strings.TrimPrefix(args, "disable "))
		if err := gw.features.Disable(ctx, name); err != nil {
			return "", err
		}
		gw.persistFeatureState()
		reply := fmt.Sprintf("Feature %q disabled.", name)
		if info := gw.findFeatureInfo(name); info != nil && !info.HotReloadable {
			reply += "\nNote: not hot-reloadable — restart IronClaw for full effect."
		}
		return reply, nil

	default:
		return "Usage: /feature [list|enable <name>|disable <name>]", nil
	}
}

// featureListString builds a formatted feature list string.
func (gw *Gateway) featureListString() string {
	features := gw.features.List()
	if len(features) == 0 {
		return "No features registered."
	}

	var enabled, disabled []feature.FeatureInfo
	for _, f := range features {
		if f.Enabled {
			enabled = append(enabled, f)
		} else {
			disabled = append(disabled, f)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Features** — %d active · %d inactive\n\n", len(enabled), len(disabled))

	writeGroup := func(items []feature.FeatureInfo) {
		for _, f := range items {
			hot := ""
			if f.HotReloadable {
				hot = " [live]"
			}
			line := fmt.Sprintf("- **%s**%s — %s", f.Name, hot, f.Description)
			if !f.Enabled && f.Reason != "" && f.Reason != "enabled" {
				line += fmt.Sprintf(" *(%s)*", f.Reason)
			}
			b.WriteString(line + "\n")
		}
	}

	if len(enabled) > 0 {
		b.WriteString("**Active**\n\n")
		writeGroup(enabled)
		b.WriteString("\n")
	}
	if len(disabled) > 0 {
		b.WriteString("**Inactive**\n\n")
		writeGroup(disabled)
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	b.WriteString("[live] = hot-reloadable · /feature enable <name> · /feature disable <name>")
	return b.String()
}

// handleConfig shows current effective configuration.
func (gw *Gateway) handleConfig(ctx context.Context, _ channel.Channel, _ channel.InboundMessage) (string, error) {
	var b strings.Builder
	b.WriteString("**Configuration**\n\n")
	cfg := gw.Config()
	fmt.Fprintf(&b, "  Provider:       %s\n", cfg.LLM.Provider)
	fmt.Fprintf(&b, "  Model:          %s\n", gw.agent.Model())
	fmt.Fprintf(&b, "  Max Tokens:     %d\n", cfg.LLM.MaxTokens)
	fmt.Fprintf(&b, "  Agent Mode:     %s\n", gw.currentMode.Load().(string))
	fmt.Fprintf(&b, "  Max Iterations: %d\n", cfg.Agent.MaxIterations)

	if gw.features != nil {
		enabled := 0
		for _, f := range gw.features.List() {
			if f.Enabled {
				enabled++
			}
		}
		fmt.Fprintf(&b, "  Features:       %d enabled\n", enabled)
	}

	return b.String(), nil
}

// handleCompact triggers manual context compression for the current session.
func (gw *Gateway) handleCompact(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.contextMgr == nil {
		return "Context compression is not configured.", nil
	}

	sess, err := gw.sessions.Get(ctx, msg.Channel, msg.ChannelID)
	if err != nil {
		return "", fmt.Errorf("failed to get session: %w", err)
	}

	beforeCount := len(sess.History())

	compressed, err := gw.contextMgr.Compress(ctx, sess, "")
	if err != nil {
		return "", fmt.Errorf("compression failed: %w", err)
	}

	afterCount := len(sess.History())

	if !compressed {
		return fmt.Sprintf("No compression needed (current: %d messages).", beforeCount), nil
	}

	if err := gw.sessions.Persist(ctx, sess); err != nil {
		slog.Warn("gateway: failed to persist after compact", "err", err)
	}

	return fmt.Sprintf("Compressed: %d → %d messages.", beforeCount, afterCount), nil
}

// handleModel shows or switches the current LLM model.
func (gw *Gateway) handleModel(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	args := strings.TrimPrefix(msg.Text, "/model")
	args = strings.TrimSpace(args)

	if args == "" {
		return fmt.Sprintf("Model: %s (provider: %s)", gw.agent.Model(), gw.Config().LLM.Provider), nil
	}

	old := gw.agent.Model()
	gw.agent.SetModel(args)
	if gw.cognitiveLoop != nil {
		gw.cognitiveLoop.SetModel(args)
	}
	return fmt.Sprintf("Model switched: %s → %s", old, args), nil
}

// handleReset resets the session to start a fresh conversation (/new or /start).
func (gw *Gateway) handleReset(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	if err := gw.sessions.Reset(ctx, msg.Channel, msg.ChannelID); err != nil {
		return "", fmt.Errorf("failed to reset session: %w", err)
	}
	return "New conversation started.", nil
}
