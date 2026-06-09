package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// View renders the full TUI.
func (m Model) View() string {
	if !m.ready {
		return "Initializing…"
	}

	var b strings.Builder

	// Header
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Welcome screen or chat viewport
	if len(m.messages) == 0 && m.streamingID == "" && !m.waitingForResponse {
		b.WriteString(m.renderWelcome())
	} else {
		b.WriteString(m.viewport.View())
	}

	// Typing indicator between chat and input
	if m.waitingForResponse && m.streamingID == "" {
		b.WriteString("\n")
		b.WriteString(m.renderTypingIndicator())
	}

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
		if m.showingSuggestions && len(m.suggestions) > 0 {
			b.WriteString(m.renderSuggestions())
			b.WriteString("\n")
		}

		if m.showHelpPanel {
			b.WriteString(m.renderHelpPanel())
			b.WriteString("\n")
		}

		if m.showModelPanel {
			b.WriteString(m.renderModelPanel())
			b.WriteString("\n")
		}

		if m.showStats {
			b.WriteString(m.renderStatsPanel())
			b.WriteString("\n")
		}

		b.WriteString(inputBoxStyle.Width(m.width - 2).Render(m.textarea.View()))
	}

	// Status bar
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// renderHeader renders the top bar with session context.
func (m Model) renderHeader() string {
	left := fmt.Sprintf(" IronClaw %s ", m.version)

	// Right side: mode + CWD
	var rightParts []string
	rightParts = append(rightParts, headerLabelStyle.Render("mode:")+" "+m.agentMode)
	if m.cwd != "" {
		shortCwd := shortenPath(m.cwd, 30)
		rightParts = append(rightParts, headerLabelStyle.Render(shortCwd))
	}
	right := strings.Join(rightParts, "  ")

	// Calculate spacing
	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	spacer := m.width - leftLen - rightLen - 2 // -2 for padding
	if spacer < 1 {
		spacer = 1
	}

	return headerStyle.Width(m.width).Render(left + strings.Repeat(" ", spacer) + right)
}

// renderWelcome renders the branded welcome screen.
func (m Model) renderWelcome() string {
	logo := welcomeTitleStyle.Render("🦾  IronClaw")

	subtitle := welcomeSubtitleStyle.Render("Local-first AI Agent Runtime")

	shortcuts := []struct{ key, desc string }{
		{"/help", "Show available commands"},
		{"/mode", "Switch agent mode (simple / cognitive)"},
		{"/stats", "Toggle metrics panel"},
		{"/clear", "Clear conversation history"},
		{"/quit", "Exit IronClaw"},
	}

	var hintLines string
	for _, s := range shortcuts {
		hintLines += fmt.Sprintf("  %s  %s\n",
			welcomeKeyStyle.Render(s.key),
			welcomeHintStyle.Render(s.desc))
	}

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		logo,
		subtitle,
		"",
		hintLines,
	)

	// Center vertically by padding top
	availableHeight := m.viewport.Height
	contentHeight := lipgloss.Height(content)
	topPad := (availableHeight - contentHeight) / 2
	if topPad < 0 {
		topPad = 0
	}

	return strings.Repeat("\n", topPad) + welcomeBoxStyle.Render(content)
}

// renderChat renders the message history with visual distinction.
func (m Model) renderChat() string {
	var b strings.Builder
	for i, msg := range m.messages {
		if i > 0 {
			b.WriteString("\n")
		}
		ts := timestampStyle.Render(msg.timestamp.Format("15:04"))
		contentWidth := m.width - 10 // account for timestamp + bar + padding

		switch msg.role {
		case "user":
			bar := userBarStyle.Render("▌")
			label := userLabelStyle.Render(m.username)
			wrapped := wrapText(msg.content, contentWidth)
			b.WriteString(fmt.Sprintf("%s %s %s\n%s  %s",
				ts, bar, label, bar, wrapped))

		case "agent":
			bar := agentBarStyle.Render("▌")
			label := agentLabelStyle.Render("IronClaw")
			// Full markdown rendering for agent messages
			rendered := renderMarkdown(msg.content)
			// Prefix each line with bar accent
			indentedRendered := indentWithBar(rendered, bar)
			b.WriteString(fmt.Sprintf("%s %s %s\n%s",
				ts, bar, label, indentedRendered))

		case "system":
			bar := systemBarStyle.Render("·")
			wrapped := wrapText(msg.content, contentWidth)
			b.WriteString(fmt.Sprintf("%s %s %s", ts, bar, systemStyle.Render(wrapped)))
		}
	}

	// Streaming text
	if m.streamingID != "" && m.streamingText != "" {
		b.WriteString("\n")
		ts := timestampStyle.Render(time.Now().Format("15:04"))
		bar := agentBarStyle.Render("▌")
		label := agentLabelStyle.Render("IronClaw")
		indicator := streamingStyle.Render(" ▊")
		rendered := renderMarkdown(m.streamingText)
		indentedRendered := indentWithBar(rendered, bar)
		b.WriteString(fmt.Sprintf("%s %s %s\n%s%s",
			ts, bar, label, indentedRendered, indicator))
	}

	return b.String()
}

// renderTypingIndicator renders the animated "waiting" dots.
func (m Model) renderTypingIndicator() string {
	dots := []string{"○", "○", "○"}
	dots[m.typingTick] = typingDotActiveStyle.Render("●")
	for i := range dots {
		if i != m.typingTick {
			dots[i] = typingDotInactiveStyle.Render(dots[i])
		}
	}
	return "  " + agentLabelStyle.Render("IronClaw") + " " +
		strings.Join(dots, " ") + "  " + statusDimStyle.Render("thinking…")
}

// renderStatusBar renders the compact one-line status bar below the input.
func (m Model) renderStatusBar() string {
	var parts []string

	// Tool status with icon
	if m.activeTool != "" {
		parts = append(parts, statusToolRunningStyle.Render("⏳ "+m.activeTool))
	} else if m.lastTool != "" {
		if m.lastToolOK {
			parts = append(parts, statusToolOKStyle.Render(
				fmt.Sprintf("✓ %s (%dms)", m.lastTool, m.lastToolMs)))
		} else {
			parts = append(parts, statusToolFailStyle.Render(
				fmt.Sprintf("✗ %s (%dms)", m.lastTool, m.lastToolMs)))
		}
	}

	// Context utilization with visual bar
	if m.metrics.utilization > 0 {
		pct := int(m.metrics.utilization * 100)
		bar := renderMiniBar(m.metrics.utilization, 10)
		style := statusDimStyle
		if pct >= 90 {
			style = statusToolFailStyle
		} else if pct >= 70 {
			style = statusToolRunningStyle
		}
		parts = append(parts, style.Render(fmt.Sprintf("ctx %s %d%%", bar, pct)))
	}

	// Token usage
	totalTokens := m.metrics.inputTokens + m.metrics.outputTokens
	if totalTokens > 0 {
		parts = append(parts, statusDimStyle.Render(
			fmt.Sprintf("↥%s", formatTokenCount(totalTokens))))
	}

	// Iteration counter
	if m.metrics.maxIter > 0 {
		parts = append(parts, statusDimStyle.Render(
			fmt.Sprintf("i%d/%d", m.metrics.iteration+1, m.metrics.maxIter)))
	}

	// Tool count
	if m.toolCount > 0 {
		parts = append(parts, statusDimStyle.Render(fmt.Sprintf("⚒%d", m.toolCount)))
	}

	// Shortcuts hint
	if m.activeTool != "" || m.waitingForResponse {
		parts = append(parts, statusDimStyle.Render("Esc cancel"))
	}

	line := strings.Join(parts, statusDimStyle.Render(" │ "))
	return statusBarStyle.Width(m.width).Render(line)
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
	maxDisplay := 5
	totalSuggestions := len(m.suggestions)

	startIdx := 0
	endIdx := totalSuggestions
	if totalSuggestions > maxDisplay {
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

	isArgCompletion := len(m.suggestions) > 0 && m.suggestions[0].ArgValue != ""
	header := "Commands:"
	if isArgCompletion {
		header = "Arguments:"
	}
	b.WriteString(suggestionHeaderStyle.Render(header))
	b.WriteString("\n")

	if startIdx > 0 {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↑ %d more above", startIdx)))
		b.WriteString("\n")
	}

	for i := startIdx; i < endIdx; i++ {
		suggestion := m.suggestions[i]
		isSelected := i == m.selectedSuggestion

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

	if endIdx < totalSuggestions {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↓ %d more below", totalSuggestions-endIdx)))
		b.WriteString("\n")
	}

	b.WriteString(suggestionHintStyle.Render("  [↑↓] Navigate  [Tab] Accept  [Enter] Execute  [Esc] Dismiss"))

	return suggestionBoxStyle.Width(m.width - 4).Render(b.String())
}

// modelInfo groups a model name with its role label.
type modelInfo struct {
	role  string // e.g. "Opus", "Sonnet", "Haiku"
	name  string // actual model name
	label string // display: "role → name" or just "name"
}

// getAnthropicModels returns the Anthropic-format model list.
// If ANTHROPIC_DEFAULT_* env vars are set, they override the official defaults.
func getAnthropicModels() []modelInfo {
	models := []modelInfo{
		{role: "Opus", name: orDefault("ANTHROPIC_DEFAULT_OPUS_MODEL", "claude-opus-4-8")},
		{role: "Sonnet", name: orDefault("ANTHROPIC_DEFAULT_SONNET_MODEL", "claude-sonnet-4-6")},
		{role: "Sonnet 4", name: "claude-sonnet-4-20250514"},
		{role: "Haiku", name: orDefault("ANTHROPIC_DEFAULT_HAIKU_MODEL", "claude-haiku-4-5")},
	}
	for i := range models {
		if models[i].name == models[i].role {
			models[i].label = models[i].name
		} else if models[i].name != "" {
			models[i].label = models[i].role + " → " + models[i].name
		}
	}
	return models
}

// getOpenAIModels returns the OpenAI-format model list with env var overrides.
func getOpenAIModels() []modelInfo {
	return []modelInfo{
		{role: "GPT-5", name: orDefault("OPENAI_DEFAULT_GPT5_MODEL", "gpt-5.4")},
		{role: "GPT-5 Mini", name: orDefault("OPENAI_DEFAULT_GPT5_MINI_MODEL", "gpt-5.4-mini")},
		{role: "GPT-4.1", name: orDefault("OPENAI_DEFAULT_GPT4_MODEL", "gpt-4.1")},
		{role: "Reasoning", name: orDefault("OPENAI_DEFAULT_REASONING_MODEL", "o4-mini")},
		{role: "GPT-4o", name: "gpt-4o"},
	}
}

// orDefault returns the env var value if set, otherwise the default.
func orDefault(envKey, defaultVal string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultVal
}

// renderModelPanel renders the model info and reference panel.
func (m Model) renderModelPanel() string {
	var b strings.Builder
	b.WriteString(statsHeaderStyle.Render("Model"))
	b.WriteString("\n\n")

	// Current model
	current := m.metrics.model
	if current == "" {
		current = "pending first request"
	}
	b.WriteString(statsLabelStyle.Render("Current: "))
	b.WriteString(statsValueStyle.Render(current))
	b.WriteString("\n")
	if m.metrics.provider != "" {
		b.WriteString(statsLabelStyle.Render("Provider: "))
		b.WriteString(statsValueStyle.Render(m.metrics.provider))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Anthropic models (with env var overrides)
	anthroLabel := "Anthropic"
	if os.Getenv("ANTHROPIC_DEFAULT_SONNET_MODEL") != "" {
		anthroLabel += " (custom via env)"
	}
	b.WriteString(statsLabelStyle.Render(anthroLabel + ":"))
	b.WriteString("\n")
	for _, mi := range getAnthropicModels() {
		b.WriteString(statsValueStyle.Render(fmt.Sprintf("  %s", mi.name)))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// OpenAI models
	openaiLabel := "OpenAI-compatible"
	if os.Getenv("OPENAI_DEFAULT_GPT5_MODEL") != "" {
		openaiLabel += " (custom via env)"
	}
	b.WriteString(statsLabelStyle.Render(openaiLabel + ":"))
	b.WriteString("\n")
	for _, mi := range getOpenAIModels() {
		b.WriteString(statsValueStyle.Render(fmt.Sprintf("  %s", mi.name)))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString(suggestionHintStyle.Render("  /model <name> to switch  [Esc] dismiss"))
	b.WriteString("\n")
	b.WriteString(suggestionHintStyle.Render("  Set ANTHROPIC_DEFAULT_* / OPENAI_DEFAULT_* env vars to customize"))

	return statsPanelStyle.Width(m.width - 2).Render(b.String())
}

// renderHelpPanel renders the commands reference panel.
func (m Model) renderHelpPanel() string {
	var b strings.Builder
	b.WriteString(statsHeaderStyle.Render("Commands"))
	b.WriteString("\n\n")

	categories := make(map[string][]Command)
	for _, cmd := range GetCommands() {
		categories[cmd.Category] = append(categories[cmd.Category], cmd)
	}

	// Builtin commands
	if cmds, ok := categories["builtin"]; ok {
		b.WriteString(statsLabelStyle.Render("Built-in"))
		b.WriteString("\n")
		for _, cmd := range cmds {
			name := statsValueStyle.Render(fmt.Sprintf("  /%-14s", cmd.Name))
			var desc string
			if cmd.ArgHint != "" {
				desc = cmd.ArgHint + " — " + cmd.Description
			} else {
				desc = cmd.Description
			}
			b.WriteString(fmt.Sprintf("%s  %s\n", name, statsLabelStyle.Render(desc)))
		}
		b.WriteString("\n")
	}

	b.WriteString(suggestionHintStyle.Render("  [Esc] dismiss"))

	return statsPanelStyle.Width(m.width - 2).Render(b.String())
}

// ─── Helpers ────────────────────────────────────────────────────────────

// formatTokenCount formats a token count with k-suffix for large values.
func formatTokenCount(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// shortenPath truncates a path to maxLen by replacing the middle with "…".
func shortenPath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	half := (maxLen - 1) / 2
	return path[:half] + "…" + path[len(path)-half:]
}

// renderMiniBar draws a compact horizontal bar for status line use.
func renderMiniBar(ratio float64, width int) string {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			if ratio >= 0.9 {
				bar += statusToolFailStyle.Render("█")
			} else if ratio >= 0.7 {
				bar += statusToolRunningStyle.Render("█")
			} else {
				bar += statusToolOKStyle.Render("█")
			}
		} else {
			bar += statsBarEmptyStyle.Render("░")
		}
	}
	return bar
}

// indentWithBar prefixes each line of s with the bar rune for visual alignment.
func indentWithBar(s string, bar string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = bar
		} else {
			lines[i] = bar + " " + line
		}
	}
	return strings.Join(lines, "\n")
}
