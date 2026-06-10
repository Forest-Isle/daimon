package tui

import (
	"fmt"
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
		return true, nil

	case "version", "v":
		m.addMessage("system", fmt.Sprintf("IronClaw %s (mode: %s)", m.version, m.agentMode))
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
			return true, nil
		}
		// /model <name> — let the gateway handle the switch
		return false, nil

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
