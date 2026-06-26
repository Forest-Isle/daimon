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

	sections := []string{m.renderHeader()}

	if len(m.messages) == 0 && m.streamingID == "" && !m.waitingForResponse {
		sections = append(sections, m.renderWelcome())
	} else {
		sections = append(sections, m.viewport.View())
	}

	if m.waitingForResponse && m.streamingID == "" {
		sections = append(sections, m.renderTypingIndicator())
	}

	sections = append(sections, m.renderBottomArea(), m.renderStatusBar())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) renderBottomArea() string {
	switch m.mode {
	case modeApproval:
		return m.renderApprovalDialog()
	case modeFeedback:
		return m.renderFeedbackDialog()
	default:
		var blocks []string
		if m.showingSuggestions && len(m.suggestions) > 0 {
			blocks = append(blocks, m.renderSuggestions())
		} else if m.showHelpPanel {
			blocks = append(blocks, m.renderHelpPanel())
		} else if m.showModelPanel {
			blocks = append(blocks, m.renderModelPanel())
		}

		blocks = append(blocks, inputBoxStyle.Width(m.inputBoxWidth()).Render(m.textarea.View()))
		return lipgloss.JoinVertical(lipgloss.Left, blocks...)
	}
}

// renderStatusBar renders the persistent bottom status line. It reuses the
// single row already reserved by the layout (statusHeight in the resize
// handler) so it costs no extra vertical space.
func (m Model) renderStatusBar() string {
	width := m.termWidth()

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
	if maxState := width / 2; maxState > 8 {
		stateText = truncateTail(stateText, maxState)
	}
	state := stateStyle.Render(stateText)

	// Right: mouse state + model + scroll hint.
	rightAvailable := width - lipgloss.Width(state) - 3
	if rightAvailable < 0 {
		rightAvailable = 0
	}
	var rightSegments []statusSegment
	if !m.mouseEnabled {
		rightSegments = append(rightSegments, statusSegment{text: "text select · Ctrl+O", style: statusHintStyle})
	}
	if !m.autoScroll && m.ready {
		rightSegments = append(rightSegments, statusSegment{text: "scrolled · End", style: statusHintStyle})
	}
	if m.hasSteps() {
		rightSegments = append(rightSegments, statusSegment{text: "Ctrl+T details", style: statusHintStyle})
	}
	if m.currentModel != "" {
		rightSegments = append(rightSegments, statusSegment{text: shortenPath(m.currentModel, 28), style: statusModelStyle})
	}
	right := renderStatusSegments(rightSegments, rightAvailable)

	gap := width - lipgloss.Width(state) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	body := " " + state + statusBarStyle.Render(strings.Repeat(" ", gap)) + right + " "
	return statusBarStyle.Width(width).Render(body)
}

type statusSegment struct {
	text  string
	style lipgloss.Style
}

func renderStatusSegments(segments []statusSegment, maxWidth int) string {
	if maxWidth <= 0 || len(segments) == 0 {
		return ""
	}

	var parts []string
	remaining := maxWidth
	separatorWidth := 2
	for _, segment := range segments {
		if len(parts) > 0 {
			if remaining <= separatorWidth {
				break
			}
			remaining -= separatorWidth
		}
		if remaining <= 0 {
			break
		}
		text := truncateTail(segment.text, remaining)
		parts = append(parts, segment.style.Render(text))
		remaining -= runewidth.StringWidth(text)
	}

	return strings.Join(parts, statusBarStyle.Render("  "))
}

// renderHeader renders the top bar with session context.
func (m Model) renderHeader() string {
	width := m.termWidth()
	contentWidth := width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}

	left := fmt.Sprintf("Daimon %s", m.version)

	// Right side: CWD
	var rightParts []string
	if m.cwd != "" {
		shortCwd := shortenPath(m.cwd, minInt(30, contentWidth/2))
		rightParts = append(rightParts, headerLabelStyle.Render(shortCwd))
	}
	right := strings.Join(rightParts, "  ")

	// Calculate spacing
	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)
	if leftLen+rightLen+1 > contentWidth {
		left = truncateTail(left, maxInt(1, contentWidth-rightLen-1))
		leftLen = lipgloss.Width(left)
	}
	spacer := contentWidth - leftLen - rightLen
	if spacer < 1 {
		spacer = 1
	}

	return headerStyle.Width(width).Render(left + strings.Repeat(" ", spacer) + right)
}

// renderWelcome renders the branded welcome screen.
func (m Model) renderWelcome() string {
	// Title leads with the agent glyph so the welcome speaks the same visual
	// language as a Daimon turn; no emoji (its width varies across terminals
	// and would skew the centering).
	title := agentGlyphStyle.Render("⏺") + "  " + welcomeTitleStyle.Render("Daimon")
	subtitle := welcomeSubtitleStyle.Render("Local-first AI Agent Runtime")

	shortcuts := []struct{ key, desc string }{
		{"/help", "Show available commands"},
		{"/clear", "Clear conversation history"},
		{"/quit", "Exit Daimon"},
		{"Ctrl+O", "Toggle text selection mode"},
	}

	// Two-column hint block: keys padded to a fixed column (measured on the
	// plain text, styled after), descriptions trailing. Left-aligned as a
	// block, the block itself centered below.
	const keyCol = 10
	var hintLines []string
	for _, s := range shortcuts {
		pad := keyCol - runewidth.StringWidth(s.key)
		if pad < 1 {
			pad = 1
		}
		hintLines = append(hintLines,
			welcomeKeyStyle.Render(s.key)+strings.Repeat(" ", pad)+welcomeHintStyle.Render(s.desc))
	}
	hints := lipgloss.JoinVertical(lipgloss.Left, hintLines...)

	content := lipgloss.JoinVertical(lipgloss.Center, title, subtitle, "", "", hints)

	availableHeight := m.viewport.Height
	if availableHeight < 1 {
		availableHeight = 1
	}
	// Fall back to the compact form when the screen is too short or too narrow.
	if lipgloss.Height(content) > availableHeight || lipgloss.Width(content) > m.termWidth() {
		return m.renderCompactWelcome(availableHeight)
	}

	centered := lipgloss.NewStyle().Width(m.termWidth()).Align(lipgloss.Center).Render(content)
	topPad := (availableHeight - lipgloss.Height(content)) / 2
	if topPad < 0 {
		topPad = 0
	}
	return strings.Repeat("\n", topPad) + centered
}

func (m Model) renderCompactWelcome(maxRows int) string {
	width := m.messageContentWidth()
	lines := []string{
		agentGlyphStyle.Render("⏺") + " " + welcomeTitleStyle.Render(truncateTail("Daimon", maxInt(1, width-2))),
		statusDimStyle.Render(truncateTail("Local-first AI Agent Runtime", width)),
		welcomeHintStyle.Render(truncateTail("/help commands · Ctrl+O text select", width)),
	}
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > len(lines) {
		maxRows = len(lines)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines[:maxRows]...)
}

// renderChat renders the message history plus any live streaming tail.
//
// The finalized history is cached as one joined string (renderStaticChat);
// the streaming tail is the only part rebuilt on each ~20Hz tick, so a long
// transcript no longer re-concatenates on every frame.
func (m *Model) renderChat() string {
	static := m.renderStaticChat()
	if m.streamingID == "" || m.streamingText == "" {
		return static
	}
	tail := m.renderStreamingTail()
	if static == "" {
		return tail
	}
	return static + "\n\n" + tail
}

// renderStaticChat returns the joined render of all finalized messages,
// rebuilding only when the transcript changed (chatRev) or the width changed.
// Each block is also cached on its message (keyed by width) so glamour runs
// once per message rather than per frame.
func (m *Model) renderStaticChat() string {
	if m.chatCacheBuilt && m.chatCacheRev == m.chatRev && m.chatCacheWidth == m.width {
		return m.chatCache
	}

	var b strings.Builder
	for i := range m.messages {
		if i > 0 {
			if m.messages[i].role == "step" && m.messages[i-1].role == "step" {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		msg := &m.messages[i]
		if msg.role == "step" {
			b.WriteString(m.renderMessageBlock(msg)) // depends on stepsExpanded; never cached
			continue
		}
		if msg.renderedWidth != m.width || msg.rendered == "" {
			msg.rendered = m.renderMessageBlock(msg)
			msg.renderedWidth = m.width
		}
		b.WriteString(msg.rendered)
	}

	m.chatCache = b.String()
	m.chatCacheWidth = m.width
	m.chatCacheRev = m.chatRev
	m.chatCacheBuilt = true
	return m.chatCache
}

// renderStreamingTail renders the live, still-growing agent text. Plain
// wrapped text (no glamour) — cheap, and it avoids the flicker of half-closed
// markdown fences mid-stream.
func (m *Model) renderStreamingTail() string {
	glyph := agentGlyphStyle.Render("⏺")
	body := wrapText(m.streamingText, m.messageContentWidth()) + streamCursorStyle.Render("▌")
	return indentBlock(body, glyph+" ", "  ")
}

// renderMessageBlock renders a single message block: a leading role glyph on
// the first line, continuation indented to align under the text. Deterministic
// given the message content, role, and width — so the result is safe to cache.
func (m *Model) renderMessageBlock(msg *chatMessage) string {
	contentWidth := m.messageContentWidth()

	switch msg.role {
	case "user":
		body := wrapText(msg.content, contentWidth)
		return indentBlock(body, userGlyphStyle.Render("›")+" ", "  ")

	case "agent":
		rendered := strings.Trim(renderMarkdown(msg.content), "\n")
		return indentBlock(rendered, agentGlyphStyle.Render("⏺")+" ", "  ")

	case "system":
		body := systemStyle.Render(wrapText(msg.content, contentWidth))
		return indentBlock(body, "  ", "  ")

	case "step":
		return m.renderStepLine(msg.step)
	}
	return ""
}

// renderStepLine renders one workflow step as a guide-prefixed line:
//
//	│ ⚙ <tool> · <arg>   <status> <duration> · <result>
//
// A sub-agent step (depth>0) is indented one level deeper per depth with a "⤷"
// connector so nested tool activity reads as part of the delegating round. The
// variable-length arg is budgeted on plain widths (accounting for the guide) so
// the styled line stays within terminal width. When stepsExpanded is set, the
// step's captured raw output is appended (dim) under the line.
func (m *Model) renderStepLine(s *workflowStep) string {
	firstGuide, contGuide := stepGuides(s.depth)
	// messageContentWidth already leaves room for the depth-0 guide ("│ ",
	// depth0GuideWidth cols); deeper guides cost more, so subtract the extra.
	width := m.messageContentWidth() - (runewidth.StringWidth(firstGuide) - depth0GuideWidth)
	if width < 8 {
		width = 8
	}

	statusPlain, statusStyled := stepStatus(s)

	meta := ""
	if s.done {
		var parts []string
		if s.duration > 0 {
			parts = append(parts, formatDuration(s.duration))
		}
		if s.resultSummary != "" {
			parts = append(parts, s.resultSummary)
		}
		meta = strings.Join(parts, " · ")
	}

	head := "⚙ " + s.tool
	used := runewidth.StringWidth(statusPlain) + 1 + runewidth.StringWidth(head)
	metaCost := 0
	if meta != "" {
		metaCost = 2 + runewidth.StringWidth(meta)
	}

	arg := s.arg
	if arg != "" {
		argBudget := width - used - metaCost - runewidth.StringWidth(" · ")
		if argBudget < 4 {
			arg = ""
		} else {
			arg = truncateTail(arg, argBudget)
		}
	}

	line := statusStyled + " " + stepGlyphStyle.Render("⚙ ") + s.tool
	if arg != "" {
		line += stepArgStyle.Render(" · " + arg)
	}
	if meta != "" {
		line += "  " + stepMetaStyle.Render(meta)
	}

	body := line
	if m.stepsExpanded && s.output != "" {
		body += "\n" + stepOutputStyle.Render(wrapText(s.output, width-2))
	}

	return indentBlock(body, stepGuideStyle.Render(firstGuide), stepGuideStyle.Render(contGuide))
}

// stepGuides returns the first-line and continuation prefixes for a step at the
// given nesting depth. Depth 0 is the round's own guide ("│ "); deeper levels
// add two columns per level plus a "⤷ " connector for sub-agent steps.
func stepGuides(depth int) (first, cont string) {
	if depth <= 0 {
		return "│ ", "  "
	}
	pad := strings.Repeat("  ", depth)
	return "│ " + pad + "⤷ ", "│ " + pad + "  "
}

// depth0GuideWidth is the display width of the depth-0 step guide ("│ "), which
// messageContentWidth already reserves headroom for.
const depth0GuideWidth = 2

// stepStatus returns the plain text (for width budgeting) and the styled glyph
// for a step's current state.
func stepStatus(s *workflowStep) (plain, styled string) {
	switch {
	case !s.done:
		return "⏵ running", stepRunStyle.Render("⏵ running")
	case s.interrupted:
		return "⊘ interrupted", stepMetaStyle.Render("⊘ interrupted")
	case s.ok:
		return "✓", stepOkStyle.Render("✓")
	default:
		return "✗", stepErrStyle.Render("✗")
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d > 0 && d < time.Millisecond:
		return "<1ms" // sub-millisecond in-process tools would otherwise read as "0ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// renderTypingIndicator renders the animated "waiting" dots, aligned under the
// agent glyph so it reads as a turn that hasn't produced text yet.
func (m Model) renderTypingIndicator() string {
	dots := []string{"○", "○", "○"}
	dots[m.typingTick] = typingDotActiveStyle.Render("●")
	for i := range dots {
		if i != m.typingTick {
			dots[i] = typingDotInactiveStyle.Render(dots[i])
		}
	}
	return agentGlyphStyle.Render("⏺") + " " +
		strings.Join(dots, " ") + "  " + statusDimStyle.Render("thinking…")
}

// renderApprovalDialog renders the tool approval overlay.
func (m Model) renderApprovalDialog() string {
	input := m.approvalInput
	if r := []rune(input); len(r) > 200 {
		input = string(r[:200]) + "..."
	}
	input = wrapText(input, m.panelContentWidth())
	content := fmt.Sprintf(
		"%s %s\n\n%s\n\n%s",
		approvalToolStyle.Render("Tool:"),
		approvalToolStyle.Render(m.approvalTool),
		input,
		approvalHintStyle.Render("[y] Approve  [n] Deny  [a] Always approve"),
	)
	return approvalBoxStyle.Width(m.panelWidth()).Render(content)
}

// renderFeedbackDialog renders the feedback rating overlay.
func (m Model) renderFeedbackDialog() string {
	content := fmt.Sprintf(
		"%s\n\n%s",
		approvalToolStyle.Render("👍 Was this response helpful?"),
		approvalHintStyle.Render("[y] Yes (+1.0)  [n] No (-1.0)"),
	)
	return feedbackBoxStyle.Width(m.panelWidth()).Render(content)
}

// renderSuggestions renders the autocomplete suggestion list.
func (m Model) renderSuggestions() string {
	if len(m.suggestions) == 0 {
		return ""
	}

	var b strings.Builder
	maxDisplay := m.maxSuggestionItems()
	totalSuggestions := len(m.suggestions)

	selected := m.selectedSuggestion
	if selected < 0 {
		selected = 0
	}
	startIdx, endIdx := visibleRange(totalSuggestions, selected, maxDisplay)

	isArgCompletion := len(m.suggestions) > 0 && m.suggestions[0].ArgValue != ""
	header := "Commands"
	if isArgCompletion {
		header = "Arguments"
	}
	meta := fmt.Sprintf("%d matches", totalSuggestions)
	if m.selectedSuggestion >= 0 {
		meta = fmt.Sprintf("%d/%d", m.selectedSuggestion+1, totalSuggestions)
	}
	b.WriteString(panelTitleLine(header, meta, m.panelContentWidth(), suggestionHeaderStyle))
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
			primary = "/" + suggestion.Command.Name
			secondary = suggestion.Command.Description
		}

		if isSelected {
			line := twoColumnLine("▶ ", primary, secondary, m.panelContentWidth())
			b.WriteString(selectedSuggestionStyle.Render(line))
		} else {
			line := twoColumnLine("  ", primary, secondary, m.panelContentWidth())
			b.WriteString(suggestionStyle.Render(line))
		}
		b.WriteString("\n")
	}

	if endIdx < totalSuggestions {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↓ %d more below", totalSuggestions-endIdx)))
		b.WriteString("\n")
	}

	b.WriteString(suggestionHintStyle.Render("  [↑↓] Navigate  [Tab] Accept  [Enter] Execute  [Esc] Dismiss"))

	return suggestionBoxStyle.Width(m.panelWidth()).Render(b.String())
}

// renderModelPanel renders the interactive model selection panel.
func (m Model) renderModelPanel() string {
	var b strings.Builder
	meta := fmt.Sprintf("%d choices", len(m.modelItems))
	if m.modelSelectionIdx >= 0 && len(m.modelItems) > 0 {
		meta = fmt.Sprintf("%d/%d", m.modelSelectionIdx+1, len(m.modelItems))
	}
	b.WriteString(panelTitleLine("Model", meta, m.panelContentWidth(), statsHeaderStyle))
	b.WriteString("\n\n")

	if m.currentModel != "" {
		b.WriteString(statsLabelStyle.Render("Current: "))
		b.WriteString(statsValueStyle.Render(truncateTail(m.currentModel, m.panelContentWidth()-9)))
		b.WriteString("\n")
	}

	b.WriteString(statsLabelStyle.Render("Available"))
	b.WriteString("\n")
	selected := m.modelSelectionIdx
	if selected < 0 {
		selected = 0
	}
	maxItems := m.maxPanelListItems(9)
	startIdx, endIdx := visibleRange(len(m.modelItems), selected, maxItems)
	if startIdx > 0 {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↑ %d more", startIdx)))
		b.WriteString("\n")
	}
	for i := startIdx; i < endIdx; i++ {
		mi := m.modelItems[i]
		role := mi.Role
		if mi.Name == m.currentModel {
			role += " · current"
		}
		line := twoColumnLine("  ", mi.Name, role, m.panelContentWidth())
		if i == m.modelSelectionIdx {
			b.WriteString(selectedSuggestionStyle.Render(twoColumnLine("▶ ", mi.Name, role, m.panelContentWidth())))
		} else {
			b.WriteString(statsValueStyle.Render(line))
		}
		b.WriteString("\n")
	}
	if endIdx < len(m.modelItems) {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  ↓ %d more", len(m.modelItems)-endIdx)))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString(suggestionHintStyle.Render("  [↑↓] Navigate  [Enter] Switch  [Esc] Dismiss"))

	return statsPanelStyle.Width(m.panelWidth()).Render(b.String())
}

// renderHelpPanel renders the commands reference panel.
func (m Model) renderHelpPanel() string {
	var b strings.Builder
	commands := GetCommands()
	b.WriteString(panelTitleLine("Commands", fmt.Sprintf("%d commands", len(commands)), m.panelContentWidth(), statsHeaderStyle))
	b.WriteString("\n\n")

	maxItems := m.maxPanelListItems(8)
	if maxItems > len(commands) {
		maxItems = len(commands)
	}

	b.WriteString(statsLabelStyle.Render("Common"))
	b.WriteString("\n")
	for i := 0; i < maxItems; i++ {
		cmd := commands[i]
		name := "/" + cmd.Name
		desc := cmd.Description
		if cmd.ArgHint != "" {
			desc = cmd.ArgHint + " · " + desc
		}
		b.WriteString(statsValueStyle.Render(twoColumnLine("  ", name, desc, m.panelContentWidth())))
		b.WriteString("\n")
	}
	if hidden := len(commands) - maxItems; hidden > 0 {
		b.WriteString(suggestionHintStyle.Render(fmt.Sprintf("  +%d more · type / to filter", hidden)))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString(suggestionHintStyle.Render("  [Esc] dismiss"))

	return statsPanelStyle.Width(m.panelWidth()).Render(b.String())
}

// ─── Helpers ────────────────────────────────────────────────────────────

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
// overflows. runewidth.Truncate measures the ellipsis with the same condition
// it uses for the body, so the result holds even when "…" is a wide (East
// Asian ambiguous) rune — a 1-col reservation would overflow by one there.
func truncateTail(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	if maxWidth < 1 {
		return "…"
	}
	return runewidth.Truncate(s, maxWidth, "…")
}

func twoColumnLine(prefix, primary, secondary string, width int) string {
	if width < 1 {
		return ""
	}

	prefixWidth := runewidth.StringWidth(prefix)
	available := width - prefixWidth
	if available <= 0 {
		return truncateTail(prefix, width)
	}

	if secondary == "" || available < 18 {
		return truncateTail(prefix+primary, width)
	}

	primaryWidth := 22
	if available < primaryWidth+12 {
		primaryWidth = available / 2
	}
	if primaryWidth < 8 {
		primaryWidth = available
	}

	secondaryWidth := available - primaryWidth - 2
	if secondaryWidth < 1 {
		return truncateTail(prefix+primary, width)
	}

	return prefix + padRightDisplay(primary, primaryWidth) + "  " + truncateTail(secondary, secondaryWidth)
}

func panelTitleLine(title, meta string, width int, titleStyle lipgloss.Style) string {
	if width < 1 {
		return ""
	}
	if meta == "" || width < 12 {
		return titleStyle.Render(truncateTail(title, width))
	}

	meta = truncateTail(meta, maxInt(1, width/2))
	metaWidth := runewidth.StringWidth(meta)
	titleMax := width - metaWidth - 1
	if titleMax < 1 {
		return titleStyle.Render(truncateTail(title, width))
	}

	title = truncateTail(title, titleMax)
	gap := width - runewidth.StringWidth(title) - metaWidth
	if gap < 1 {
		gap = 1
	}
	return titleStyle.Render(title) + strings.Repeat(" ", gap) + suggestionMetaStyle.Render(meta)
}

func padRightDisplay(s string, width int) string {
	if width < 1 {
		return ""
	}
	out := truncateTail(s, width)
	if pad := width - runewidth.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

func visibleRange(total, selected, maxDisplay int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if maxDisplay < 1 {
		maxDisplay = 1
	}
	if maxDisplay >= total {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}

	start := selected - maxDisplay/2
	if start < 0 {
		start = 0
	}
	end := start + maxDisplay
	if end > total {
		end = total
		start = end - maxDisplay
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// indentBlock prefixes the first line of s with first and every subsequent
// non-empty line with cont, keeping blank lines empty (no trailing spaces).
func indentBlock(s, first, cont string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		switch {
		case i == 0:
			lines[i] = first + lines[i]
		case lines[i] == "":
			// keep blank
		default:
			lines[i] = cont + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
