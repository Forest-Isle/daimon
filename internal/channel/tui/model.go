package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
	suggestions         []SuggestionItem
	selectedSuggestion  int // -1 means no selection
	showingSuggestions  bool

	// Layout
	width  int
	height int
	ready  bool
}

// NewModel creates a new TUI model.
func NewModel(agentMode, version string) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, Ctrl+C to quit)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	return Model{
		textarea:           ta,
		messages:           make([]chatMessage, 0),
		agentMode:          agentMode,
		version:            version,
		selectedSuggestion: -1,
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

		headerHeight := 1
		inputHeight := 5 // textarea + border
		vpHeight := m.height - headerHeight - inputHeight
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
		}
		m.textarea.SetWidth(m.width)

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

	// --- Custom messages from adapter goroutines ---

	case agentResponseMsg:
		m.addMessage("agent", msg.text)
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()

	case streamUpdateMsg:
		m.streamingID = msg.id
		m.streamingText = msg.text
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()

	case streamFinishMsg:
		if msg.text != "" {
			m.addMessage("agent", msg.text)
		}
		m.streamingID = ""
		m.streamingText = ""
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()

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
		m.addMessage("system", "🔄 New conversation started.")
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()

	case errorMsg:
		m.addMessage("system", "⚠️ Error: "+msg.err.Error())
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()

	case notificationMsg:
		m.addMessage("system", msg.text)
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
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
	case tea.KeyCtrlC:
		return m, tea.Quit

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

		m.textarea.Reset()

		// Handle local slash commands
		if handled, cmd := m.handleLocalCommand(text); handled {
			return m, cmd
		}

		m.addMessage("user", text)
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
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
		m.addMessage("system", fmt.Sprintf("✅ Approved: %s", m.approvalTool))
		if m.approvalCh != nil {
			m.approvalCh <- true
			m.approvalCh = nil
		}
		m.mode = modeChat
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
	case "n", "N", "esc":
		m.addMessage("system", fmt.Sprintf("❌ Denied: %s", m.approvalTool))
		if m.approvalCh != nil {
			m.approvalCh <- false
			m.approvalCh = nil
		}
		m.mode = modeChat
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
	case "a", "A":
		m.addMessage("system", fmt.Sprintf("✅ Always approve: %s", m.approvalTool))
		if m.approvalCh != nil {
			m.approvalCh <- true
			m.approvalCh = nil
		}
		m.mode = modeChat
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
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
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
	case "n", "N", "esc":
		m.addMessage("system", "👎 Feedback: not helpful (-1.0)")
		if m.feedbackCh != nil {
			m.feedbackCh <- -1.0
			m.feedbackCh = nil
		}
		m.mode = modeChat
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
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
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
	case "2", "a", "A":
		m.addMessage("system", "🔄 Adjust & replan")
		if m.reflectCh != nil {
			m.reflectCh <- channel.ReplanAdjust
			m.reflectCh = nil
		}
		m.mode = modeChat
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
	case "3", "x", "X", "esc":
		m.addMessage("system", "🛑 Abort")
		if m.reflectCh != nil {
			m.reflectCh <- channel.ReplanAbort
			m.reflectCh = nil
		}
		m.mode = modeChat
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
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

// updateSuggestions refreshes the suggestion list based on current input.
func (m *Model) updateSuggestions() {
	input := m.textarea.Value()
	suggestions := GenerateSuggestions(input, len(input))

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

	// Header
	header := headerStyle.Width(m.width).Render(
		fmt.Sprintf(" IronClaw %s  [%s]", m.version, m.agentMode))
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
		b.WriteString(inputBorderStyle.Width(m.width).Render(""))
		b.WriteString("\n")
		b.WriteString(m.textarea.View())
	}

	return b.String()
}

func (m Model) renderChat() string {
	var b strings.Builder
	for _, msg := range m.messages {
		ts := timestampStyle.Render(msg.timestamp.Format("15:04"))
		switch msg.role {
		case "user":
			label := userLabelStyle.Render("You")
			_, _ = fmt.Fprintf(&b, "%s %s: %s\n\n", ts, label, msg.content)
		case "agent":
			label := agentLabelStyle.Render("Agent")
			rendered := renderMarkdown(msg.content)
			_, _ = fmt.Fprintf(&b, "%s %s:\n%s\n", ts, label, rendered)
		case "system":
			_, _ = fmt.Fprintf(&b, "%s %s\n\n", ts, systemStyle.Render(msg.content))
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
		approvalToolStyle.Render("🔧 Tool:"),
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
	displayCount := len(m.suggestions)
	if displayCount > maxDisplay {
		displayCount = maxDisplay
	}

	b.WriteString(suggestionHeaderStyle.Render("Commands:"))
	b.WriteString("\n")

	for i := 0; i < displayCount; i++ {
		suggestion := m.suggestions[i]
		isSelected := i == m.selectedSuggestion

		var line string
		if isSelected {
			// Highlight selected suggestion
			line = selectedSuggestionStyle.Render(fmt.Sprintf("▶ %-20s  %s",
				suggestion.Command.Name,
				suggestion.Command.Description))
		} else {
			line = suggestionStyle.Render(fmt.Sprintf("  %-20s  %s",
				suggestion.Command.Name,
				suggestion.Command.Description))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(m.suggestions) > maxDisplay {
		b.WriteString(suggestionHintStyle.Render(
			fmt.Sprintf("  ... and %d more", len(m.suggestions)-maxDisplay)))
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
		m.addMessage("system", "🔄 Conversation cleared.")
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
		return true, nil

	case "help", "h", "?":
		m.showHelp()
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
		return true, nil

	case "version", "v":
		m.addMessage("system", fmt.Sprintf("IronClaw %s (mode: %s)", m.version, m.agentMode))
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
		return true, nil

	case "status":
		m.showStatus()
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
		return true, nil

	case "history", "hist":
		m.showHistory()
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
		return true, nil

	case "export":
		filename := "conversation.txt"
		if len(args) > 0 {
			filename = args[0]
		}
		m.exportConversation(filename)
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
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
			b.WriteString(fmt.Sprintf("  /%s", cmd.Name))
			if cmd.ArgHint != "" {
				b.WriteString(fmt.Sprintf(" %s", cmd.ArgHint))
			}
			if len(cmd.Aliases) > 0 {
				b.WriteString(fmt.Sprintf(" (aliases: %s)", strings.Join(cmd.Aliases, ", ")))
			}
			b.WriteString(fmt.Sprintf("\n    %s\n", cmd.Description))
		}
	}

	b.WriteString("\nTip: Type / to see command suggestions with autocomplete")

	m.addMessage("system", b.String())
}

// showStatus displays current session status.
func (m *Model) showStatus() {
	var b strings.Builder
	b.WriteString("📊 Session Status:\n\n")
	b.WriteString(fmt.Sprintf("Mode: %s\n", m.agentMode))
	b.WriteString(fmt.Sprintf("Version: %s\n", m.version))
	b.WriteString(fmt.Sprintf("Messages: %d\n", len(m.messages)))
	if m.streamingID != "" {
		b.WriteString("Streaming: active\n")
	} else {
		b.WriteString("Streaming: idle\n")
	}

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
			icon := "💬"
			switch msg.role {
			case "user":
				icon = "👤"
			case "agent":
				icon = "🤖"
			case "system":
				icon = "⚙️"
			}
			preview := msg.content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			b.WriteString(fmt.Sprintf("%d. %s [%s] %s\n",
				i+1, icon, msg.timestamp.Format("15:04:05"), preview))
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

