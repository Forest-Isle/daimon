package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Update handles all incoming messages and routes them to the appropriate
// handler based on the current mode.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update markdown renderer width for proper text wrapping
		updateRendererWidth(m.messageContentWidth() + 4)
		m.textarea.SetWidth(m.textareaWidth())
		m.syncInputHeight()

		if !m.ready {
			m.viewport = viewport.New(m.termWidth(), m.viewportHeight())
			m.viewport.SetContent(m.renderChat())
			m.ready = true
		} else {
			m.viewport.Width = m.termWidth()
			m.viewport.Height = m.viewportHeight()
			m.viewport.SetContent(m.renderChat()) // Re-render with new width
			if m.autoScroll {
				m.viewport.GotoBottom()
			}
		}

	case tickMsg:
		m.typingTick = (m.typingTick + 1) % 3
		if m.waitingForResponse && m.streamingID == "" {
			cmds = append(cmds, typingTick())
		}

	case tea.KeyMsg:
		switch m.mode {
		case modeApproval:
			return m.handleApprovalKey(msg)
		case modeFeedback:
			return m.handleFeedbackKey(msg)
		default:
			return m.handleChatKey(msg)
		}

	case tea.MouseMsg:
		if m.ready && m.mouseEnabled {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			m.autoScroll = m.viewport.AtBottom()
			return m, cmd
		}
		return m, nil

	// --- Custom messages from adapter goroutines ---

	case agentResponseMsg:
		m.waitingForResponse = false
		m.clearToolActivity()
		m.addMessage("agent", msg.text)
		m.updateViewportKeepScroll()

	case streamUpdateMsg:
		m.waitingForResponse = false
		m.clearToolActivity()
		m.streamingID = msg.id
		m.streamingText = msg.text
		m.updateViewportKeepScroll()

	case streamFinishMsg:
		m.waitingForResponse = false
		m.clearToolActivity()
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

	case feedbackRequestMsg:
		m.mode = modeFeedback
		m.feedbackCh = msg.resultCh

	case sessionResetMsg:
		m.messages = m.messages[:0]
		m.stepIndex = nil
		m.addMessage("system", "New conversation started.")
		m.refreshViewport()

	case errorMsg:
		m.waitingForResponse = false
		m.clearToolActivity()
		m.addMessage("system", "Error: "+msg.err.Error())
		m.updateViewportKeepScroll()

	case notificationMsg:
		m.addMessage("system", msg.text)
		m.updateViewportKeepScroll()

	case exportCompleteMsg:
		if msg.err != nil {
			m.addMessage("system", "Export failed: "+msg.err.Error())
		} else {
			m.addMessage("system", "Exported conversation: "+msg.path)
		}
		m.updateViewportKeepScroll()

	case toolActivityMsg:
		if msg.done {
			if i, ok := m.stepIndex[msg.callID]; ok {
				s := m.messages[i].step
				s.done = true
				s.ok = msg.ok
				s.resultSummary = msg.resultSummary
				s.output = msg.output
				s.duration = msg.duration
				m.chatRev++
			}
			if msg.tool == m.activeTool {
				m.activeTool = ""
				m.activeToolSummary = ""
			}
		} else {
			m.appendStep(msg.callID, msg.tool, msg.arg)
			m.activeTool = msg.tool
			m.activeToolSummary = msg.arg
		}
		m.updateViewportKeepScroll()

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
	// Handle model selection navigation
	if m.showModelPanel && len(m.modelItems) > 0 {
		switch msg.Type {
		case tea.KeyUp:
			if m.modelSelectionIdx > 0 {
				m.modelSelectionIdx--
			} else {
				m.modelSelectionIdx = len(m.modelItems) - 1
			}
			return m, nil
		case tea.KeyDown:
			if m.modelSelectionIdx < len(m.modelItems)-1 {
				m.modelSelectionIdx++
			} else {
				m.modelSelectionIdx = 0
			}
			return m, nil
		case tea.KeyEnter:
			if m.modelSelectionIdx >= 0 && m.modelSelectionIdx < len(m.modelItems) {
				selected := m.modelItems[m.modelSelectionIdx]
				m.textarea.SetValue("/model " + selected.Name)
				m.showModelPanel = false
				return m, func() tea.Msg { return tea.KeyMsg{Type: tea.KeyEnter} }
			}
			return m, nil
		case tea.KeyEsc:
			m.showModelPanel = false
			return m, nil
		}
	}

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
				m.syncInputHeight()
			}
			return m, nil

		case tea.KeyEsc:
			// Dismiss suggestions
			m.clearSuggestions()
			return m, nil
		}
	}

	// ── Mouse toggle ──────────────────────────────────────────────
	if msg.Type == tea.KeyCtrlO {
		return m, m.toggleMouseMode()
	}

	// ── Workflow-step raw-output expand toggle ────────────────────
	if msg.Type == tea.KeyCtrlT {
		m.stepsExpanded = !m.stepsExpanded
		m.chatRev++
		m.updateViewportKeepScroll()
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		// Priority: close panels first, then cancel running request
		if m.showHelpPanel {
			m.showHelpPanel = false
			return m, nil
		}
		if m.showModelPanel {
			m.showModelPanel = false
			return m, nil
		}
		if m.streamingID != "" || m.waitingForResponse {
			m.addMessage("system", "⏹ Request cancelled.")
			m.streamingID = ""
			m.streamingText = ""
			m.waitingForResponse = false
			m.clearToolActivity()
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

	case tea.KeyEnd:
		// Jump to the latest output and resume following the stream.
		if m.ready {
			m.autoScroll = true
			m.viewport.GotoBottom()
			return m, nil
		}

	case tea.KeyUp:
		// Input history: navigate to previous entry
		if len(m.inputHistory) > 0 && m.historyIdx > 0 {
			if m.historyIdx == len(m.inputHistory) {
				m.historySaved = m.textarea.Value()
			}
			m.historyIdx--
			m.textarea.SetValue(m.inputHistory[m.historyIdx])
			m.syncInputHeight()
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
			m.syncInputHeight()
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
		m.syncInputHeight()

		// Handle local slash commands
		if handled, cmd := m.handleLocalCommand(text); handled {
			return m, cmd
		}

		m.waitingForResponse = true
		m.typingTick = 0
		m.addMessage("user", text)
		m.refreshViewport()
		return m, typingTick()
	}

	// Update textarea and refresh suggestions
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.updateSuggestions()
	m.syncInputHeight()

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

// addMessage appends a message to the conversation history.
//
// chatRev is bumped so the static-chat render cache rebuilds on the next
// frame. Both transcript-clearing paths (/clear, session reset) append a
// message immediately after truncating, so this single bump covers them too.
func (m *Model) addMessage(role, content string) {
	m.messages = append(m.messages, chatMessage{
		role:      role,
		content:   content,
		timestamp: time.Now(),
	})
	m.chatRev++
}

// appendStep adds a pending workflow step to the transcript and indexes it by
// callID so the matching done event can update it in place.
func (m *Model) appendStep(callID, tool, arg string) {
	m.messages = append(m.messages, chatMessage{
		role:      "step",
		step:      &workflowStep{callID: callID, tool: tool, arg: arg},
		timestamp: time.Now(),
	})
	if m.stepIndex == nil {
		m.stepIndex = make(map[string]int)
	}
	m.stepIndex[callID] = len(m.messages) - 1
	m.chatRev++
}

// hasSteps reports whether the transcript contains any workflow step.
func (m *Model) hasSteps() bool {
	return len(m.stepIndex) > 0
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

// clearToolActivity resets the active-tool status indicator.
func (m *Model) clearToolActivity() {
	m.activeTool = ""
	m.activeToolSummary = ""
}

// viewportHeight returns the chat viewport height for the current chrome.
// The bottom area is measured from the same render path used by View, so
// panels and dialogs cannot push the status bar below the terminal.
func (m *Model) viewportHeight() int {
	h := m.termHeight() - 1 - m.typingIndicatorHeight() - m.bottomAreaHeight() - 1
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) termWidth() int {
	if m.width > 0 {
		return m.width
	}
	return 80
}

func (m Model) termHeight() int {
	if m.height > 0 {
		return m.height
	}
	return 24
}

func (m Model) textareaWidth() int {
	w := m.termWidth() - 4
	if w < 1 {
		return 1
	}
	return w
}

func (m Model) inputBoxWidth() int {
	w := m.termWidth() - 2
	if w < 1 {
		return 1
	}
	return w
}

func (m Model) panelWidth() int {
	w := m.termWidth() - 4
	if w < 1 {
		return 1
	}
	return w
}

func (m Model) panelContentWidth() int {
	w := m.termWidth() - 8
	if w < 12 {
		return maxInt(1, m.termWidth()-4)
	}
	return w
}

func (m Model) messageContentWidth() int {
	w := m.termWidth() - 10
	if w < 12 {
		return maxInt(1, m.termWidth()-4)
	}
	return w
}

func (m Model) typingIndicatorHeight() int {
	if m.waitingForResponse && m.streamingID == "" {
		return 1
	}
	return 0
}

func (m Model) bottomAreaHeight() int {
	return lipgloss.Height(m.renderBottomArea())
}

func (m Model) inputBoxHeight() int {
	inputH := m.textarea.Height()
	if inputH < 1 {
		inputH = 1
	}
	return inputH + 2
}

func (m Model) maxPanelHeight() int {
	h := m.termHeight() - 1 - 1 - 1 - m.typingIndicatorHeight() - m.inputBoxHeight()
	if h < 3 {
		return 3
	}
	if h > 12 {
		return 12
	}
	return h
}

func (m Model) maxPanelListItems(reservedRows int) int {
	items := m.maxPanelHeight() - reservedRows
	if items < 1 {
		return 1
	}
	return items
}

func (m Model) maxSuggestionItems() int {
	items := m.maxPanelHeight() - 6
	if items < 1 {
		return 1
	}
	if items > 5 {
		return 5
	}
	return items
}

// syncInputHeight grows or shrinks the input box to fit the wrapped content,
// from 1 line up to maxInputLines. Without this the textarea (height 1) scrolls
// long input horizontally and hides earlier rows. The chat viewport is resized
// to match so the layout always fills exactly one screen.
func (m *Model) syncInputHeight() {
	w := m.textarea.Width()
	if w < 1 {
		return
	}
	rows := 0
	for _, line := range strings.Split(m.textarea.Value(), "\n") {
		rows += wrappedRowCount(line, w)
	}
	if rows < 1 {
		rows = 1
	}
	if rows > maxInputLines {
		rows = maxInputLines
	}
	if rows != m.textarea.Height() {
		m.textarea.SetHeight(rows)
		if m.ready {
			wasBottom := m.autoScroll
			m.viewport.Height = m.viewportHeight()
			if wasBottom {
				m.viewport.GotoBottom()
			}
		}
	}
}

// clearSuggestions hides the suggestion list.
func (m *Model) clearSuggestions() {
	m.suggestions = nil
	m.showingSuggestions = false
	m.selectedSuggestion = -1
}
