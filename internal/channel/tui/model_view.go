package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
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

		b.WriteString(inputBoxStyle.Width(m.width - 2).Render(m.textarea.View()))
	}

	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	return b.String()
}

// renderStatusBar renders the persistent bottom status line. It reuses the
// single row already reserved by the layout (statusHeight in the resize
// handler) so it costs no extra vertical space.
func (m Model) renderStatusBar() string {
	// Left: activity state.
	var stateText, stateStyle = "", statusReadyStyle
	switch {
	case m.activeTool != "":
		stateText = "⚙ " + m.activeTool
		if m.activeToolSummary != "" {
			stateText += ": " + m.activeToolSummary
		}
		stateStyle = statusBusyStyle
	case m.streamingID != "":
		stateText, stateStyle = "▊ streaming", statusBusyStyle
	case m.waitingForResponse:
		stateText, stateStyle = "⚙ working", statusBusyStyle
	default:
		stateText, stateStyle = "● ready", statusReadyStyle
	}
	// Clamp so a long tool summary can't overflow the single-line bar.
	if maxState := m.width / 2; maxState > 8 {
		stateText = truncateTail(stateText, maxState)
	}
	state := stateStyle.Render(stateText)

	// Right: model + scroll hint.
	var rightParts []string
	if !m.autoScroll && m.ready {
		rightParts = append(rightParts, statusHintStyle.Render("↑ scrolled · End to follow"))
	}
	if m.currentModel != "" {
		rightParts = append(rightParts, statusModelStyle.Render(shortenPath(m.currentModel, 28)))
	}
	right := strings.Join(rightParts, statusBarStyle.Render("  "))

	gap := m.width - lipgloss.Width(state) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	body := " " + state + statusBarStyle.Render(strings.Repeat(" ", gap)) + right + " "
	return statusBarStyle.Width(m.width).Render(body)
}

// renderHeader renders the top bar with session context.
func (m Model) renderHeader() string {
	left := fmt.Sprintf(" IronClaw %s ", m.version)

	// Right side: CWD
	var rightParts []string
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
//
// Each message block is cached on the message itself (keyed by terminal
// width) so glamour runs once per message instead of on every streaming
// tick. The streaming tail is rendered as plain wrapped text — cheap, and
// it avoids the flicker of half-closed markdown fences mid-stream.
func (m *Model) renderChat() string {
	var b strings.Builder
	for i := range m.messages {
		if i > 0 {
			b.WriteString("\n")
		}
		msg := &m.messages[i]
		if msg.renderedWidth != m.width || msg.rendered == "" {
			msg.rendered = m.renderMessageBlock(msg)
			msg.renderedWidth = m.width
		}
		b.WriteString(msg.rendered)
	}

	// Streaming text — never cached (changes every frame), rendered plain.
	if m.streamingID != "" && m.streamingText != "" {
		b.WriteString("\n")
		ts := timestampStyle.Render(time.Now().Format("15:04"))
		bar := agentBarStyle.Render("▌")
		label := agentLabelStyle.Render("IronClaw")
		indicator := streamingStyle.Render(" ▊")
		body := wrapText(m.streamingText, m.width-10)
		b.WriteString(fmt.Sprintf("%s %s %s\n%s%s",
			ts, bar, label, indentWithBar(body, bar), indicator))
	}

	return b.String()
}

// renderMessageBlock renders a single message block (header line + body).
// Deterministic given the message content, role, timestamp, and width — so
// the result is safe to cache.
func (m *Model) renderMessageBlock(msg *chatMessage) string {
	ts := timestampStyle.Render(msg.timestamp.Format("15:04"))
	contentWidth := m.width - 10 // account for timestamp + bar + padding

	switch msg.role {
	case "user":
		bar := userBarStyle.Render("▌")
		label := userLabelStyle.Render(m.username)
		wrapped := wrapText(msg.content, contentWidth)
		return fmt.Sprintf("%s %s %s\n%s  %s", ts, bar, label, bar, wrapped)

	case "agent":
		bar := agentBarStyle.Render("▌")
		label := agentLabelStyle.Render("IronClaw")
		rendered := renderMarkdown(msg.content)
		return fmt.Sprintf("%s %s %s\n%s",
			ts, bar, label, indentWithBar(rendered, bar))

	case "system":
		bar := systemBarStyle.Render("·")
		wrapped := wrapText(msg.content, contentWidth)
		return fmt.Sprintf("%s %s %s", ts, bar, systemStyle.Render(wrapped))
	}
	return ""
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

// renderApprovalDialog renders the tool approval overlay.
func (m Model) renderApprovalDialog() string {
	input := m.approvalInput
	if r := []rune(input); len(r) > 200 {
		input = string(r[:200]) + "..."
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


// renderModelPanel renders the interactive model selection panel.
func (m Model) renderModelPanel() string {
	var b strings.Builder
	b.WriteString(statsHeaderStyle.Render("Model"))
	b.WriteString("\n\n")

	b.WriteString(statsLabelStyle.Render("Available:"))
	b.WriteString("\n")
	for i, mi := range m.modelItems {
		if i == m.modelSelectionIdx {
			b.WriteString(selectedSuggestionStyle.Render(fmt.Sprintf("▶ %-24s  %s", mi.Name, mi.Role)))
		} else {
			b.WriteString(statsValueStyle.Render(fmt.Sprintf("  %-24s  %s", mi.Name, mi.Role)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString(suggestionHintStyle.Render("  [↑↓] Navigate  [Enter] Switch  [Esc] Dismiss"))

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

// shortenPath truncates a path to maxLen display columns by replacing the
// middle with "…". Operates on runes so multi-byte characters are never
// split mid-rune.
func shortenPath(path string, maxLen int) string {
	if runewidth.StringWidth(path) <= maxLen {
		return path
	}
	r := []rune(path)
	if maxLen < 1 {
		return "…"
	}
	half := (maxLen - 1) / 2
	if half < 1 {
		half = 1
	}
	if half*2 >= len(r) {
		return path
	}
	return string(r[:half]) + "…" + string(r[len(r)-half:])
}

// truncateTail trims s to maxWidth display columns, appending "…" when it
// overflows. Operates on runes so multi-byte characters are never split.
func truncateTail(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	if maxWidth < 1 {
		return "…"
	}
	r := []rune(s)
	w := 0
	for i, c := range r {
		cw := runewidth.RuneWidth(c)
		if w+cw > maxWidth-1 { // reserve 1 col for the ellipsis
			return string(r[:i]) + "…"
		}
		w += cw
	}
	return s
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
