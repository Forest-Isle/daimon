package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// mode controls how key events are routed.
type mode int

const (
	modeChat       mode = iota // normal: keys go to textarea
	modeApproval               // y/n/a intercepted for tool approval
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
		textarea:  ta,
		messages:  make([]chatMessage, 0),
		agentMode: agentMode,
		version:   version,
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

	case sessionResetMsg:
		m.messages = m.messages[:0]
		m.addMessage("system", "🔄 New conversation started.")
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()

	case errorMsg:
		m.addMessage("system", "⚠️ Error: "+msg.err.Error())
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
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEnter:
		text := strings.TrimSpace(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.textarea.Reset()

		// /quit command
		if text == "/quit" {
			return m, tea.Quit
		}

		m.addMessage("user", text)
		m.viewport.SetContent(m.renderChat())
		m.viewport.GotoBottom()
		return m, nil
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
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
	case modeReflection:
		b.WriteString(m.renderReflectionDialog())
	default:
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

func (m Model) renderReflectionDialog() string {
	content := fmt.Sprintf(
		"🤔 Low confidence plan (%.0f%%)\nReason: %s\n\n%s",
		m.reflectConfidence*100,
		m.reflectReason,
		approvalHintStyle.Render("[1/c] Continue  [2/a] Adjust  [3/x] Abort"),
	)
	return reflectionBoxStyle.Width(m.width - 4).Render(content)
}
