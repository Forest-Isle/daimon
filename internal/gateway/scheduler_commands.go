package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/channel/scheduler"
)

// handleSchedule dispatches /schedule sub-commands.
func (gw *Gateway) handleSchedule(ctx context.Context, ch channel.Channel, msg channel.InboundMessage) (string, error) {
	if gw.scheduler == nil || gw.scheduler.Channel == nil {
		return "Scheduler is not available.", nil
	}

	sc := gw.scheduler.Channel
	args := strings.TrimPrefix(msg.Text, "/schedule")
	args = strings.TrimSpace(args)

	if args == "" || args == "help" {
		return `**Schedule Commands**
- /schedule list — list all tasks
- /schedule add <cron> <prompt> — add a task
- /schedule remove <id> — remove a task
- /schedule enable <id> — enable a disabled task
- /schedule disable <id> — disable a task
- /schedule run <id> — run a task immediately

Cron examples: "@every 1h", "0 9 * * *", "@daily"`, nil
	}

	parts := splitScheduleArgs(args)
	if len(parts) == 0 {
		return "Usage: /schedule <list|add|remove|enable|disable|run>", nil
	}

	switch parts[0] {
	case "list":
		return gw.scheduleList(ctx, sc)
	case "add":
		return gw.scheduleAdd(ctx, sc, msg, parts[1:])
	case "remove":
		return gw.scheduleRemove(ctx, sc, parts[1:])
	case "enable":
		return gw.scheduleEnable(ctx, sc, parts[1:], true)
	case "disable":
		return gw.scheduleEnable(ctx, sc, parts[1:], false)
	case "run":
		return gw.scheduleRun(ctx, sc, parts[1:])
	default:
		return fmt.Sprintf("Unknown sub-command: %s. Try /schedule help.", parts[0]), nil
	}
}

func (gw *Gateway) scheduleList(ctx context.Context, sc *scheduler.SchedulerChannel) (string, error) {
	tasks, err := sc.ListTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}
	if len(tasks) == 0 {
		return "No scheduled tasks.", nil
	}

	var b strings.Builder
	b.WriteString("**Scheduled Tasks**\n\n")
	for _, t := range tasks {
		status := "✅"
		if !t.Enabled {
			status = "⏸️"
		}
		fmt.Fprintf(&b, "%s **%s** — `%s`\n", status, truncateTaskID(t.ID), t.CronExpr)
		fmt.Fprintf(&b, "  %s\n", t.Prompt)
		if t.LastRunAt != "" {
			fmt.Fprintf(&b, "  Last: %s (%s)\n", t.LastRunAt, t.LastStatus)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (gw *Gateway) scheduleAdd(ctx context.Context, sc *scheduler.SchedulerChannel, msg channel.InboundMessage, args []string) (string, error) {
	if len(args) < 2 {
		return "Usage: /schedule add <cron_expr> <prompt>", nil
	}

	cronExpr := args[0]
	prompt := strings.Join(args[1:], " ")

	t, err := sc.AddTask(ctx, prompt, cronExpr, msg.Channel, msg.ChannelID)
	if err != nil {
		return "", fmt.Errorf("add task: %w", err)
	}

	return fmt.Sprintf("Task added: **%s** (`%s`): %s", truncateTaskID(t.ID), t.CronExpr, t.Prompt), nil
}

func (gw *Gateway) scheduleRemove(ctx context.Context, sc *scheduler.SchedulerChannel, args []string) (string, error) {
	if len(args) < 1 {
		return "Usage: /schedule remove <id>", nil
	}
	if err := sc.RemoveTask(ctx, args[0]); err != nil {
		return "", fmt.Errorf("remove task: %w", err)
	}
	return fmt.Sprintf("Task removed: %s", args[0]), nil
}

func (gw *Gateway) scheduleEnable(ctx context.Context, sc *scheduler.SchedulerChannel, args []string, enable bool) (string, error) {
	action := "enable"
	if !enable {
		action = "disable"
	}
	if len(args) < 1 {
		return fmt.Sprintf("Usage: /schedule %s <id>", action), nil
	}
	if err := sc.SetEnabled(ctx, args[0], enable); err != nil {
		return "", fmt.Errorf("%s task: %w", action, err)
	}
	state := "enabled"
	if !enable {
		state = "disabled"
	}
	return fmt.Sprintf("Task %s now %s.", args[0], state), nil
}

func (gw *Gateway) scheduleRun(ctx context.Context, sc *scheduler.SchedulerChannel, args []string) (string, error) {
	if len(args) < 1 {
		return "Usage: /schedule run <id>", nil
	}
	if err := sc.RunOnce(ctx, args[0]); err != nil {
		return "", fmt.Errorf("run task: %w", err)
	}
	return fmt.Sprintf("Task %s triggered. Check back for results.", args[0]), nil
}

// splitScheduleArgs splits a string by spaces, respecting quoted strings.
func splitScheduleArgs(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func truncateTaskID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
