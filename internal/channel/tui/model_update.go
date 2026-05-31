package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Update handles all incoming messages and routes them to the appropriate
// handler based on the current mode.
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

// handleChatKey processes key events when in normal chat mode.
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

// handleApprovalKey processes y/n/a keys during tool approval mode.
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

// handleFeedbackKey processes y/n keys during feedback rating mode.
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

// handleReflectionKey processes 1/2/3 keys during replan decision mode.
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

// addMessage appends a message to the conversation history.
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
