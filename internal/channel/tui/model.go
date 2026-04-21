package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mode controls how key events are routed.
type mode int

const (
	modeChat       mode = iota // normal: keys go to textarea
	modeApproval               // y/n/a intercepted for tool approval
	modeFeedback               // y/n intercepted for feedback rating
	modeReflection             // 1/2/3 intercepted for replan decision
)

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role      string // "user", "agent", "system"
	content   string
	timestamp time.Time
}

// toolHistoryEntry records a completed tool execution for the stats panel.
type toolHistoryEntry struct {
	name       string
	succeeded  bool
	durationMs int64
}

// metricsState holds the latest runtime metrics for display.
type metricsState struct {
	iteration    int
	maxIter      int
	utilization  float64
	cacheCreate  int64
	cacheRead    int64
	inputTokens  int64
	outputTokens int64
	model        string
	provider     string
}

// Model is the Bubble Tea model for the TUI channel.
type Model struct {
	// UI components
	viewport viewport.Model
	textarea textarea.Model

	// State
	mode          mode
	messages      []chatMessage
	streamingID   string // non-empty while streaming
	streamingText string
	agentMode     string // "simple" or "cognitive"
	version       string
	dashboardURL  string // non-empty when web dashboard is enabled

	// Approval state
	approvalTool  string
	approvalInput string
	approvalCh    chan bool

	// Reflection state
	reflectReason     string
	reflectConfidence float64
	reflectCh         chan channel.ReplanDecision

	// Feedback state
	feedbackCh chan float64

	// Suggestion state
	suggestions        []SuggestionItem
	selectedSuggestion int // -1 means no selection
	showingSuggestions bool
	argCompleter       ArgCompleter // optional; injected by the adapter

	// Scroll state
	autoScroll bool // true = follow new content; false = user is reading history

	// Input history (↑/↓ navigation)
	inputHistory []string
	historyIdx   int    // current position; len(inputHistory) = "new input"
	historySaved string // stash current input when entering history

	// Metrics & tool tracking
	activeTool  string // currently executing tool name (empty when idle)
	lastTool    string // most recent completed tool
	lastToolOK  bool
	lastToolMs  int64
	toolHistory []toolHistoryEntry
	toolCount   int // total tools executed this session
	metrics     metricsState
	phase       string // current cognitive phase (empty in simple mode)
	showStats   bool   // toggle for detailed stats panel

	// Compression tracking for stats panel
	compressionCount   int
	lastCompressFrom   float64 // before utilization (0.0–1.0)
	lastCompressTo     float64 // after utilization (0.0–1.0)
	lastCompressReason string

	// Layout
	width  int
	height int
	ready  bool
}

// NewModel creates a new TUI model.
func NewModel(agentMode, version string) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, /help for commands)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Prompt = "  "
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	return Model{
		textarea:           ta,
		messages:           make([]chatMessage, 0),
		agentMode:          agentMode,
		version:            version,
		selectedSuggestion: -1,
		autoScroll:         true,
	}
}

func (m Model) Init() tea.Cmd {
	return textarea.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update markdown renderer width for proper text wrapping
		updateRendererWidth(m.width)

		headerHeight := 1
		inputHeight := 5 // textarea + border (3 lines + 2 border)
		statusHeight := 1
		vpHeight := m.height - headerHeight - inputHeight - statusHeight
		if vpHeight < 1 {
			vpHeight = 1
		}

		if !m.ready {
			m.viewport = viewport.New(m.width, vpHeight)
			m.viewport.SetContent(m.renderChat())
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpHeight
			m.viewport.SetContent(m.renderChat()) // Re-render with new width
		}
		m.textarea.SetWidth(m.width - 4) // account for input box padding+border

	case tea.KeyMsg:
		switch m.mode {
		case modeApproval:
			return m.handleApprovalKey(msg)
		case modeFeedback:
			return m.handleFeedbackKey(msg)
		case modeReflection:
			return m.handleReflectionKey(msg)
		default:
			return m.handleChatKey(msg)
		}

	case tea.MouseMsg:
		if m.ready {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			m.autoScroll = m.viewport.AtBottom()
			return m, cmd
		}
		return m, nil

	// --- Custom messages from adapter goroutines ---

	case agentResponseMsg:
		m.addMessage("agent", msg.text)
		m.updateViewportKeepScroll()

	case streamUpdateMsg:
		m.streamingID = msg.id
		m.streamingText = msg.text
		m.updateViewportKeepScroll()

	case streamFinishMsg:
		if msg.text != "" {
			m.addMessage("agent", msg.text)
		}
		m.streamingID = ""
		m.streamingText = ""
		m.updateViewportKeepScroll()

	case approvalRequestMsg:
		m.mode = modeApproval
		m.approvalTool = msg.toolName
		m.approvalInput = msg.input
		m.approvalCh = msg.resultCh

	case reflectionRequestMsg:
		m.mode = modeReflection
		m.reflectReason = msg.reason
		m.reflectConfidence = msg.confidence
		m.reflectCh = msg.resultCh

	case feedbackRequestMsg:
		m.mode = modeFeedback
		m.feedbackCh = msg.resultCh

	case sessionResetMsg:
		m.messages = m.messages[:0]
		m.addMessage("system", "New conversation started.")
		m.refreshViewport()

	case errorMsg:
		m.addMessage("system", "Error: "+msg.err.Error())
		m.updateViewportKeepScroll()

	case notificationMsg:
		m.addMessage("system", msg.text)
		m.updateViewportKeepScroll()

	case setAgentModeMsg:
		m.agentMode = msg.mode
		return m, nil

	case toolStartMsg:
		m.activeTool = msg.toolName
		return m, nil

	case toolEndMsg:
		m.activeTool = ""
		m.lastTool = msg.toolName
		m.lastToolOK = msg.succeeded
		m.lastToolMs = msg.durationMs
		m.toolCount++
		entry := toolHistoryEntry{
			name:       msg.toolName,
			succeeded:  msg.succeeded,
			durationMs: msg.durationMs,
		}
		m.toolHistory = append(m.toolHistory, entry)
		const maxHistory = 50
		if len(m.toolHistory) > maxHistory {
			m.toolHistory = m.toolHistory[len(m.toolHistory)-maxHistory:]
		}
		return m, nil

	case phaseStartMsg:
		m.phase = msg.phase
		return m, nil

	case phaseEndMsg:
		m.phase = ""
		return m, nil

	case metricsUpdateMsg:
		m.metrics = metricsState(msg)
		return m, nil

	case compressionNotificationMsg:
		m.compressionCount++
		m.lastCompressFrom = msg.beforePct
		m.lastCompressTo = msg.afterPct
		m.lastCompressReason = msg.reason
		notification := fmt.Sprintf(
			"Context compressed: %d%% → %d%% (%s, %d layers)",
			int(msg.beforePct*100), int(msg.afterPct*100),
			msg.reason, msg.layersRun,
		)
		m.messages = append(m.messages, chatMessage{
			role:      "system",
			content:   notification,
			timestamp: time.Now(),
		})
		m.updateViewportKeepScroll()
		return m, nil
	}

	// Update sub-components
	if m.mode == modeChat {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle suggestion navigation
	if m.showingSuggestions && len(m.suggestions) > 0 {
		switch msg.Type {
		case tea.KeyUp:
			// Navigate up in suggestions (wrap around)
			if m.selectedSuggestion <= 0 {
				m.selectedSuggestion = len(m.suggestions) - 1
			} else {
				m.selectedSuggestion--
			}
			return m, nil

		case tea.KeyDown:
			// Navigate down in suggestions (wrap around)
			if m.selectedSuggestion >= len(m.suggestions)-1 {
				m.selectedSuggestion = 0
			} else {
				m.selectedSuggestion++
			}
			return m, nil

		case tea.KeyTab:
			// Accept suggestion without executing
			if m.selectedSuggestion >= 0 && m.selectedSuggestion < len(m.suggestions) {
				suggestion := m.suggestions[m.selectedSuggestion]
				newInput := ApplySuggestion(m.textarea.Value(), suggestion)
				m.textarea.SetValue(newInput)
				m.clearSuggestions()
			}
			return m, nil

		case tea.KeyEsc:
			// Dismiss suggestions
			m.clearSuggestions()
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyEsc:
		// Priority: close panels first, then cancel running request
		if m.showStats {
			m.showStats = false
			return m, nil
		}
		if m.streamingID != "" || m.activeTool != "" {
			m.addMessage("system", "⏹ Request cancelled.")
			m.streamingID = ""
			m.streamingText = ""
			m.activeTool = ""
			m.refreshViewport()
			return m, func() tea.Msg { return cancelRequestMsg{} }
		}
		return m, nil

	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyPgUp, tea.KeyPgDown:
		// Forward page scroll keys to viewport; update autoScroll based on position.
		if m.ready {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			m.autoScroll = m.viewport.AtBottom()
			return m, cmd
		}

	case tea.KeyUp:
		// Input history: navigate to previous entry
		if len(m.inputHistory) > 0 && m.historyIdx > 0 {
			if m.historyIdx == len(m.inputHistory) {
				m.historySaved = m.textarea.Value()
			}
			m.historyIdx--
			m.textarea.SetValue(m.inputHistory[m.historyIdx])
			return m, nil
		}
		return m, nil

	case tea.KeyDown:
		// Input history: navigate to next entry
		if m.historyIdx < len(m.inputHistory) {
			m.historyIdx++
			if m.historyIdx == len(m.inputHistory) {
				m.textarea.SetValue(m.historySaved)
			} else {
				m.textarea.SetValue(m.inputHistory[m.historyIdx])
			}
			return m, nil
		}
		return m, nil

	case tea.KeyEnter:
		text := strings.TrimSpace(m.textarea.Value())
		if text == "" {
			return m, nil
		}

		// If suggestions are showing and one is selected, apply it and execute
		if m.showingSuggestions && m.selectedSuggestion >= 0 && m.selectedSuggestion < len(m.suggestions) {
			suggestion := m.suggestions[m.selectedSuggestion]
			text = strings.TrimSpace(ApplySuggestion(text, suggestion))
			m.clearSuggestions()
		}

		// Save to input history (deduplicate consecutive)
		if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != text {
			m.inputHistory = append(m.inputHistory, text)
			const maxInputHistory = 100
			if len(m.inputHistory) > maxInputHistory {
				m.inputHistory = m.inputHistory[len(m.inputHistory)-maxInputHistory:]
			}
		}
		m.historyIdx = len(m.inputHistory)
		m.historySaved = ""

		m.textarea.Reset()

		// Handle local slash commands
		if handled, cmd := m.handleLocalCommand(text); handled {
			return m, cmd
		}

		m.addMessage("user", text)
		m.refreshViewport()
		return m, nil
	}

	// Update textarea and refresh suggestions
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.updateSuggestions()

	return m, cmd
}

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.addMessage("system", fmt.Sprintf("Approved: %s", m.approvalTool))
		if m.approvalCh != nil {
			m.approvalCh <- true
			m.approvalCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	case "n", "N", "esc":
		m.addMessage("system", fmt.Sprintf("Denied: %s", m.approvalTool))
		if m.approvalCh != nil {
			m.approvalCh <- false
			m.approvalCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	case "a", "A":
		m.addMessage("system", "Always approve enabled (all future tools will auto-approve)")
		if m.approvalCh != nil {
			m.approvalCh <- true
			m.approvalCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
		// Send message to adapter to enable autoApprove
		return m, func() tea.Msg { return setAutoApproveMsg{} }
	}
	return m, nil
}

func (m *Model) handleFeedbackKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.addMessage("system", "👍 Feedback: helpful (+1.0)")
		if m.feedbackCh != nil {
			m.feedbackCh <- 1.0
			m.feedbackCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	case "n", "N", "esc":
		m.addMessage("system", "👎 Feedback: not helpful (-1.0)")
		if m.feedbackCh != nil {
			m.feedbackCh <- -1.0
			m.feedbackCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	}
	return m, nil
}

func (m *Model) handleReflectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1", "c", "C":
		m.addMessage("system", "▶️ Continue")
		if m.reflectCh != nil {
			m.reflectCh <- channel.ReplanContinue
			m.reflectCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	case "2", "a", "A":
		m.addMessage("system", "Adjust & replan")
		if m.reflectCh != nil {
			m.reflectCh <- channel.ReplanAdjust
			m.reflectCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	case "3", "x", "X", "esc":
		m.addMessage("system", "Aborted")
		if m.reflectCh != nil {
			m.reflectCh <- channel.ReplanAbort
			m.reflectCh = nil
		}
		m.mode = modeChat
		m.updateViewportKeepScroll()
	}
	return m, nil
}

func (m *Model) addMessage(role, content string) {
	m.messages = append(m.messages, chatMessage{
		role:      role,
		content:   content,
		timestamp: time.Now(),
	})
}

// refreshViewport re-renders content, re-enables autoScroll, and jumps to bottom.
// Use for explicit user actions (sending a message, /clear, session reset).
func (m *Model) refreshViewport() {
	if m.ready {
		m.autoScroll = true
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
	}
}

// updateViewportKeepScroll re-renders content and only scrolls to bottom when
// autoScroll is enabled (i.e. the user hasn't scrolled up to read history).
func (m *Model) updateViewportKeepScroll() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderChat())
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

// SetArgCompleter injects a dynamic argument completer into the model.
func (m *Model) SetArgCompleter(fn ArgCompleter) {
	m.argCompleter = fn
}

// updateSuggestions refreshes the suggestion list based on current input.
func (m *Model) updateSuggestions() {
	input := m.textarea.Value()
	suggestions := GenerateSuggestions(input, len(input), m.argCompleter)

	if len(suggestions) == 0 {
		m.clearSuggestions()
		return
	}

	m.suggestions = suggestions
	m.showingSuggestions = true

	// Reset selection if it's out of bounds
	if m.selectedSuggestion >= len(suggestions) {
		m.selectedSuggestion = 0
	} else if m.selectedSuggestion < 0 && len(suggestions) > 0 {
		m.selectedSuggestion = 0
	}
}

// clearSuggestions hides the suggestion list.
func (m *Model) clearSuggestions() {
	m.suggestions = nil
	m.showingSuggestions = false
	m.selectedSuggestion = -1
}

// View renders the full TUI.
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var b strings.Builder

	// Header — headerStyle has Padding(0,1), so content width = m.width - 2
	left := fmt.Sprintf(" IronClaw %s  [%s]", m.version, m.agentMode)
	if m.dashboardURL != "" {
		dashLabel := "Dashboard: " + m.dashboardURL
		contentWidth := m.width - 2
		gap := contentWidth - lipgloss.Width(left) - lipgloss.Width(dashLabel)
		if gap >= 2 {
			left += strings.Repeat(" ", gap) + dashLabel
		}
	}
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

	// Cognitive phase
	if m.phase != "" {
		parts = append(parts, statusPhaseStyle.Render("⟨"+m.phase+"⟩"))
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

// renderStatsPanel renders the detailed stats panel.
func (m Model) renderStatsPanel() string {
	var b strings.Builder

	// Model & Session section
	b.WriteString(statsHeaderStyle.Render("Model & Session"))
	b.WriteString("\n")
	streaming := "idle"
	if m.streamingID != "" {
		streaming = "active"
	}
	model := m.metrics.model
	if model == "" {
		model = "—"
	}
	provider := m.metrics.provider
	if provider == "" {
		provider = "—"
	}
	_, _ = fmt.Fprintf(&b, "  %s %s    %s %s\n",
		statsLabelStyle.Render("Model:"), statsValueStyle.Render(model),
		statsLabelStyle.Render("Provider:"), statsValueStyle.Render(provider))
	_, _ = fmt.Fprintf(&b, "  %s %s    %s %s    %s %s    %s %s\n",
		statsLabelStyle.Render("Mode:"), statsValueStyle.Render(m.agentMode),
		statsLabelStyle.Render("Ver:"), statsValueStyle.Render(m.version),
		statsLabelStyle.Render("Msgs:"), statsValueStyle.Render(fmt.Sprintf("%d", len(m.messages))),
		statsLabelStyle.Render("Stream:"), statsValueStyle.Render(streaming))

	// Token Usage section
	b.WriteString("\n")
	b.WriteString(statsHeaderStyle.Render("Token Usage"))
	b.WriteString("\n")
	if m.metrics.inputTokens > 0 || m.metrics.outputTokens > 0 {
		total := m.metrics.inputTokens + m.metrics.outputTokens
		_, _ = fmt.Fprintf(&b, "  %s %s    %s %s    %s %s\n",
			statsLabelStyle.Render("Input:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.inputTokens)),
			statsLabelStyle.Render("Output:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.outputTokens)),
			statsLabelStyle.Render("Total:"),
			statsValueStyle.Render(formatTokenCount(total)))
	} else {
		b.WriteString(statsLabelStyle.Render("  No token data yet"))
		b.WriteString("\n")
	}
	if m.metrics.cacheCreate > 0 || m.metrics.cacheRead > 0 {
		_, _ = fmt.Fprintf(&b, "  %s %s    %s %s\n",
			statsLabelStyle.Render("Cache Write:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.cacheCreate)),
			statsLabelStyle.Render("Cache Read:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.cacheRead)))
	}

	// Tool History section
	b.WriteString("\n")
	b.WriteString(statsHeaderStyle.Render("Tool History"))
	b.WriteString("\n")

	if len(m.toolHistory) == 0 {
		b.WriteString(statsLabelStyle.Render("  No tools executed yet"))
	} else {
		start := 0
		if len(m.toolHistory) > 8 {
			start = len(m.toolHistory) - 8
		}
		for _, entry := range m.toolHistory[start:] {
			icon := statusToolOKStyle.Render("✓")
			if !entry.succeeded {
				icon = statusToolFailStyle.Render("✗")
			}
			name := statsValueStyle.Render(fmt.Sprintf("%-16s", entry.name))
			dur := statsLabelStyle.Render(fmt.Sprintf("%5dms", entry.durationMs))
			_, _ = fmt.Fprintf(&b, "  %s %s %s\n", icon, name, dur)
		}
	}

	// Context section
	b.WriteString("\n")
	b.WriteString(statsHeaderStyle.Render("Context"))
	b.WriteString("\n")

	pct := m.metrics.utilization
	barWidth := 30
	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	barStyle := statsBarFilledStyle
	if pct >= 0.9 {
		barStyle = statsBarCritStyle
	} else if pct >= 0.7 {
		barStyle = statsBarWarnStyle
	}
	bar := barStyle.Render(strings.Repeat("█", filled)) +
		statsBarEmptyStyle.Render(strings.Repeat("░", barWidth-filled))
	_, _ = fmt.Fprintf(&b, "  %s %s %s\n",
		statsLabelStyle.Render("Utilization:"),
		bar,
		statsValueStyle.Render(fmt.Sprintf("%d%%", int(pct*100))))

	if m.metrics.maxIter > 0 {
		_, _ = fmt.Fprintf(&b, "  %s %s    %s %s\n",
			statsLabelStyle.Render("Iteration:"),
			statsValueStyle.Render(fmt.Sprintf("%d/%d", m.metrics.iteration+1, m.metrics.maxIter)),
			statsLabelStyle.Render("Tools:"),
			statsValueStyle.Render(fmt.Sprintf("%d", m.toolCount)))
	}

	if m.compressionCount > 0 {
		compVal := fmt.Sprintf("%d", m.compressionCount)
		if m.lastCompressFrom > 0 {
			compVal += fmt.Sprintf(" (last: %d%%→%d%%)", int(m.lastCompressFrom*100), int(m.lastCompressTo*100))
		}
		_, _ = fmt.Fprintf(&b, "  %s %s\n",
			statsLabelStyle.Render("Compressions:"),
			statsValueStyle.Render(compVal))
	}

	return statsPanelStyle.Width(m.width - 2).Render(b.String())
}

func formatTokenCount(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

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

func (m Model) renderFeedbackDialog() string {
	content := fmt.Sprintf(
		"%s\n\n%s",
		approvalToolStyle.Render("👍 Was this response helpful?"),
		approvalHintStyle.Render("[y] Yes (+1.0)  [n] No (-1.0)"),
	)
	return feedbackBoxStyle.Width(m.width - 4).Render(content)
}

func (m Model) renderReflectionDialog() string {
	content := fmt.Sprintf(
		"🤔 Low confidence plan (%.0f%%)\nReason: %s\n\n%s",
		m.reflectConfidence*100,
		m.reflectReason,
		approvalHintStyle.Render("[1/c] Continue  [2/a] Adjust  [3/x] Abort"),
	)
	return reflectionBoxStyle.Width(m.width - 4).Render(content)
}

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

// handleLocalCommand processes local slash commands that don't need LLM.
// Returns (handled, cmd) where handled=true means the command was processed locally.
func (m *Model) handleLocalCommand(text string) (bool, tea.Cmd) {
	if !strings.HasPrefix(text, "/") {
		return false, nil
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false, nil
	}

	cmd := strings.TrimPrefix(parts[0], "/")
	args := parts[1:]

	switch cmd {
	case "quit", "exit", "q":
		return true, tea.Quit

	case "clear", "cls":
		m.messages = m.messages[:0]
		m.addMessage("system", "Conversation cleared.")
		m.refreshViewport()
		return true, nil

	case "help", "h", "?":
		m.showHelp()
		m.updateViewportKeepScroll()
		return true, nil

	case "version", "v":
		m.addMessage("system", fmt.Sprintf("IronClaw %s (mode: %s)", m.version, m.agentMode))
		m.updateViewportKeepScroll()
		return true, nil

	case "stats", "status":
		m.showStats = !m.showStats
		return true, nil

	case "history", "hist":
		m.showHistory()
		m.updateViewportKeepScroll()
		return true, nil

	case "export":
		filename := "conversation.txt"
		if len(args) > 0 {
			filename = args[0]
		}
		m.exportConversation(filename)
		m.updateViewportKeepScroll()
		return true, nil

	default:
		// Not a local command, let it go to the agent
		return false, nil
	}
}

// showHelp displays available commands.
func (m *Model) showHelp() {
	var b strings.Builder
	b.WriteString("📚 Available Commands:\n\n")

	// Group commands by category
	categories := make(map[string][]Command)
	for _, cmd := range GetCommands() {
		categories[cmd.Category] = append(categories[cmd.Category], cmd)
	}

	// Display builtin commands
	if cmds, ok := categories["builtin"]; ok {
		b.WriteString("Built-in Commands:\n")
		for _, cmd := range cmds {
			fmt.Fprintf(&b, "  /%s", cmd.Name)
			if cmd.ArgHint != "" {
				fmt.Fprintf(&b, " %s", cmd.ArgHint)
			}
			if len(cmd.Aliases) > 0 {
				fmt.Fprintf(&b, " (aliases: %s)", strings.Join(cmd.Aliases, ", "))
			}
			fmt.Fprintf(&b, "\n    %s\n", cmd.Description)
		}
	}

	b.WriteString("\nTip: Type / to see command suggestions with autocomplete")

	m.addMessage("system", b.String())
}

// showHistory displays conversation history summary.
func (m *Model) showHistory() {
	var b strings.Builder
	b.WriteString("📜 Conversation History:\n\n")

	if len(m.messages) == 0 {
		b.WriteString("No messages yet.")
	} else {
		for i, msg := range m.messages {
			icon := "?"
			switch msg.role {
			case "user":
				icon = "you"
			case "agent":
				icon = "bot"
			case "system":
				icon = "sys"
			}
			preview := msg.content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			fmt.Fprintf(&b, "%d. %s [%s] %s\n",
				i+1, icon, msg.timestamp.Format("15:04:05"), preview)
		}
	}

	m.addMessage("system", b.String())
}

// exportConversation exports the conversation to a file.
func (m *Model) exportConversation(filename string) {
	// This is a placeholder - actual file writing would need to be done
	// through a proper channel or command since Bubble Tea models shouldn't do I/O
	m.addMessage("system", fmt.Sprintf("📤 Export requested: %s\n(Note: File export not yet implemented in TUI)", filename))
}
