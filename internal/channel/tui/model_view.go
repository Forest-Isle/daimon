package tui

import (
	"fmt"
	"strings"
	"time"
)

// View renders the full TUI.
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var b strings.Builder

	// Header — headerStyle has Padding(0,1), so content width = m.width - 2
	left := fmt.Sprintf(" IronClaw %s  [%s]", m.version, m.agentMode)
	header := headerStyle.Width(m.width).Render(left)
	b.WriteString(header)
	b.WriteString("\n")

	// Chat viewport
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// Approval / Reflection overlay or Input area
	switch m.mode {
	case modeApproval:
		b.WriteString(m.renderApprovalDialog())
	case modeFeedback:
		b.WriteString(m.renderFeedbackDialog())
	case modeReflection:
		b.WriteString(m.renderReflectionDialog())
	default:
		// Show suggestions if available
		if m.showingSuggestions && len(m.suggestions) > 0 {
			b.WriteString(m.renderSuggestions())
			b.WriteString("\n")
		}

		// Stats panel (above input when visible)
		if m.showStats {
			b.WriteString(m.renderStatsPanel())
			b.WriteString("\n")
		}

		// Input box with styled border
		b.WriteString(inputBoxStyle.Width(m.width - 2).Render(m.textarea.View()))
	}

	// Status bar (always visible at the bottom)
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// renderStatusBar renders the compact one-line status bar below the input.
func (m Model) renderStatusBar() string {
	var parts []string

	// Model identifier (shown once metrics arrive)
	if m.metrics.model != "" {
		parts = append(parts, statusPhaseStyle.Render(m.metrics.model))
	}

	// Tool status
	if m.activeTool != "" {
		parts = append(parts, statusToolRunningStyle.Render("⏳ "+m.activeTool+"..."))
	} else if m.lastTool != "" {
		if m.lastToolOK {
			parts = append(parts, statusToolOKStyle.Render(fmt.Sprintf("✓ %s %dms", m.lastTool, m.lastToolMs)))
		} else {
			parts = append(parts, statusToolFailStyle.Render(fmt.Sprintf("✗ %s %dms", m.lastTool, m.lastToolMs)))
		}
	}

	// Context utilization
	if m.metrics.utilization > 0 {
		pct := int(m.metrics.utilization * 100)
		style := statusDimStyle
		if pct >= 90 {
			style = statusToolFailStyle
		} else if pct >= 70 {
			style = statusToolRunningStyle
		}
		parts = append(parts, style.Render(fmt.Sprintf("ctx:%d%%", pct)))
	}

	// Token usage
	totalTokens := m.metrics.inputTokens + m.metrics.outputTokens
	if totalTokens > 0 {
		parts = append(parts, statusDimStyle.Render(
			fmt.Sprintf("tok:%s", formatTokenCount(totalTokens))))
	}

	// Iteration
	if m.metrics.maxIter > 0 {
		parts = append(parts, statusDimStyle.Render(
			fmt.Sprintf("i%d/%d", m.metrics.iteration+1, m.metrics.maxIter)))
	}

	// Tool count
	if m.toolCount > 0 {
		parts = append(parts, statusDimStyle.Render(fmt.Sprintf("tools:%d", m.toolCount)))
	}

	// Hint
	parts = append(parts, statusDimStyle.Render("/stats"))

	line := strings.Join(parts, statusDimStyle.Render("  │  "))
	return statusBarStyle.Width(m.width).Render(line)
}

// renderChat renders the message history.
func (m Model) renderChat() string {
	var b strings.Builder
	for _, msg := range m.messages {
		ts := timestampStyle.Render(msg.timestamp.Format("15:04"))
		switch msg.role {
		case "user":
			label := userLabelStyle.Render("You")
			// Wrap user input text
			wrappedContent := wrapText(msg.content, m.width-20) // Reserve space for timestamp and label
			_, _ = fmt.Fprintf(&b, "%s %s: %s\n\n", ts, label, wrappedContent)
		case "agent":
			label := agentLabelStyle.Render("Agent")
			rendered := renderMarkdown(msg.content)
			_, _ = fmt.Fprintf(&b, "%s %s:\n%s\n", ts, label, rendered)
		case "system":
			wrappedContent := wrapText(msg.content, m.width-10) // Reserve space for timestamp
			_, _ = fmt.Fprintf(&b, "%s %s\n\n", ts, systemStyle.Render(wrappedContent))
		}
	}

	// Show streaming text
	if m.streamingID != "" && m.streamingText != "" {
		ts := timestampStyle.Render(time.Now().Format("15:04"))
		label := agentLabelStyle.Render("Agent")
		indicator := streamingStyle.Render(" ▊")
		_, _ = fmt.Fprintf(&b, "%s %s:\n%s%s\n", ts, label, m.streamingText, indicator)
	}

	return b.String()
}

// renderApprovalDialog renders the tool approval overlay.
func (m Model) renderApprovalDialog() string {
	input := m.approvalInput
	if len(input) > 200 {
		input = input[:200] + "..."
	}
	content := fmt.Sprintf(
		"%s %s\n\n%s\n\n%s",
		approvalToolStyle.Render("Tool:"),
		approvalToolStyle.Render(m.approvalTool),
		input,
		approvalHintStyle.Render("[y] Approve  [n] Deny  [a] Always approve"),
	)
	return approvalBoxStyle.Width(m.width - 4).Render(content)
}

// renderFeedbackDialog renders the feedback rating overlay.
func (m Model) renderFeedbackDialog() string {
	content := fmt.Sprintf(
		"%s\n\n%s",
		approvalToolStyle.Render("👍 Was this response helpful?"),
		approvalHintStyle.Render("[y] Yes (+1.0)  [n] No (-1.0)"),
	)
	return feedbackBoxStyle.Width(m.width - 4).Render(content)
}

// renderReflectionDialog renders the replan decision overlay.
func (m Model) renderReflectionDialog() string {
	content := fmt.Sprintf(
		"🤔 Low confidence plan (%.0f%%)\nReason: %s\n\n%s",
		m.reflectConfidence*100,
		m.reflectReason,
		approvalHintStyle.Render("[1/c] Continue  [2/a] Adjust  [3/x] Abort"),
	)
	return reflectionBoxStyle.Width(m.width - 4).Render(content)
}

// renderSuggestions renders the autocomplete suggestion list.
func (m Model) renderSuggestions() string {
	if len(m.suggestions) == 0 {
		return ""
	}

	var b strings.Builder
	maxDisplay := 5 // Show max 5 suggestions at a time
	totalSuggestions := len(m.suggestions)

	// Calculate the visible window based on selected index
	startIdx := 0
	endIdx := totalSuggestions
	if totalSuggestions > maxDisplay {
		// Center the selected item in the visible window
		startIdx = m.selectedSuggestion - maxDisplay/2
		if startIdx < 0 {
			startIdx = 0
		}
		endIdx = startIdx + maxDisplay
		if endIdx > totalSuggestions {
			endIdx = totalSuggestions
			startIdx = endIdx - maxDisplay
			if startIdx < 0 {
				startIdx = 0
			}
		}
	}

	// Header changes based on whether we're completing a command or an argument
	isArgCompletion := len(m.suggestions) > 0 && m.suggestions[0].ArgValue != ""
	header := "Commands:"
	if isArgCompletion {
		header = "Arguments:"
	}
	b.WriteString(suggestionHeaderStyle.Render(header))
	b.WriteString("\n")

	// Show indicator if there are items above
	if startIdx > 0 {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↑ %d more above", startIdx)))
		b.WriteString("\n")
	}

	// Render visible suggestions
	for i := startIdx; i < endIdx; i++ {
		suggestion := m.suggestions[i]
		isSelected := i == m.selectedSuggestion

		// For arg completions show the arg value + full completion line as hint.
		// For command completions show the command name + description.
		var primary, secondary string
		if suggestion.ArgValue != "" {
			primary = suggestion.ArgValue
			secondary = suggestion.DisplayText
		} else {
			primary = suggestion.Command.Name
			secondary = suggestion.Command.Description
		}

		var line string
		if isSelected {
			line = selectedSuggestionStyle.Render(fmt.Sprintf("▶ %-20s  %s", primary, secondary))
		} else {
			line = suggestionStyle.Render(fmt.Sprintf("  %-20s  %s", primary, secondary))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Show indicator if there are items below
	if endIdx < totalSuggestions {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↓ %d more below", totalSuggestions-endIdx)))
		b.WriteString("\n")
	}

	b.WriteString(suggestionHintStyle.Render("  [↑↓] Navigate  [Tab] Accept  [Enter] Execute  [Esc] Dismiss"))

	return suggestionBoxStyle.Width(m.width - 4).Render(b.String())
}

// formatTokenCount formats a token count with k-suffix for large values.
func formatTokenCount(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
