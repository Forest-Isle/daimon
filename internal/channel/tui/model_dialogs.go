package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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
		m.showHelpPanel = !m.showHelpPanel
		if m.showHelpPanel {
			m.showModelPanel = false
		}
		return true, nil

	case "version", "v":
		m.addMessage("system", fmt.Sprintf("Daimon %s", m.version))
		m.updateViewportKeepScroll()
		return true, nil

	case "status", "stats":
		m.showStatus()
		m.updateViewportKeepScroll()
		return true, nil

	case "model":
		if len(args) == 0 {
			m.showModelPanel = !m.showModelPanel
			if m.showModelPanel && len(m.modelItems) == 0 {
				m.modelSelectionIdx = -1
			} else if m.showModelPanel && len(m.modelItems) > 0 {
				m.modelSelectionIdx = 0
			}
			if m.showModelPanel {
				m.showHelpPanel = false
			}
			return true, nil
		}
		// /model <name> — let the gateway handle the switch
		return false, nil

	case "history", "hist":
		m.showHistory()
		m.updateViewportKeepScroll()
		return true, nil

	case "mouse", "m":
		return true, m.toggleMouseMode()

	case "export":
		filename := "conversation.txt"
		if len(args) > 0 {
			filename = args[0]
		}
		return true, m.exportConversation(filename)

	default:
		// Not a local command, let it go to the agent
		return false, nil
	}
}

func (m *Model) toggleMouseMode() tea.Cmd {
	m.mouseEnabled = !m.mouseEnabled
	if m.mouseEnabled {
		m.addMessage("system", "Mouse scroll on (text selection off)")
	} else {
		m.addMessage("system", "Text selection on (mouse scroll off)")
	}
	m.updateViewportKeepScroll()
	if m.mouseEnabled {
		return func() tea.Msg { return tea.EnableMouseCellMotion() }
	}
	return func() tea.Msg { return tea.DisableMouse() }
}

// showStatus displays a compact snapshot of the current TUI session.
func (m *Model) showStatus() {
	mouse := "on"
	if !m.mouseEnabled {
		mouse = "off"
	}
	follow := "on"
	if !m.autoScroll {
		follow = "off"
	}
	model := m.currentModel
	if model == "" {
		model = "not set"
	}
	var b strings.Builder
	b.WriteString("Status\n\n")
	fmt.Fprintf(&b, "Version: %s\n", m.version)
	fmt.Fprintf(&b, "Model: %s\n", model)
	fmt.Fprintf(&b, "Messages: %d\n", len(m.messages))
	fmt.Fprintf(&b, "Auto-scroll: %s\n", follow)
	fmt.Fprintf(&b, "Mouse scroll: %s\n", mouse)
	fmt.Fprintf(&b, "Working directory: %s", m.cwd)
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
			if r := []rune(preview); len(r) > 60 {
				preview = string(r[:60]) + "..."
			}
			fmt.Fprintf(&b, "%d. %s [%s] %s\n",
				i+1, icon, msg.timestamp.Format("15:04:05"), preview)
		}
	}

	m.addMessage("system", b.String())
}

// exportConversation exports the conversation to a file.
func (m *Model) exportConversation(filename string) tea.Cmd {
	content := formatConversationExport(m.messages)
	return func() tea.Msg {
		path, err := writeConversationExport(filename, content)
		return exportCompleteMsg{path: path, err: err}
	}
}

func formatConversationExport(messages []chatMessage) string {
	var b strings.Builder
	b.WriteString("Daimon Conversation Export\n\n")
	if len(messages) == 0 {
		b.WriteString("No messages.\n")
		return b.String()
	}
	for _, msg := range messages {
		role := msg.role
		if role == "" {
			role = "message"
		}
		fmt.Fprintf(&b, "[%s] %s\n", msg.timestamp.Format("2006-01-02 15:04:05"), strings.ToUpper(role))
		b.WriteString(msg.content)
		if !strings.HasSuffix(msg.content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func writeConversationExport(filename string, content string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "conversation.txt"
	}
	clean := filepath.Clean(filename)
	dir := filepath.Dir(clean)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return clean, fmt.Errorf("create export directory: %w", err)
		}
	}
	if err := os.WriteFile(clean, []byte(content), 0o600); err != nil {
		return clean, fmt.Errorf("write export: %w", err)
	}
	return clean, nil
}
