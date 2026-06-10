package tui

import (
	"strings"
)

// Command represents a slash command available in the TUI.
type Command struct {
	Name        string
	Description string
	Aliases     []string
	ArgHint     string   // e.g., "<message>" or "[options]"
	Category    string   // "builtin", "skill", etc.
	SubArgs     []string // static first-level sub-arguments for autocomplete (e.g., ["list","enable","disable"])
}

// commandRegistry holds all available slash commands.
var commandRegistry = []Command{
	// Session management
	{
		Name:        "quit",
		Description: "Exit the TUI",
		Aliases:     []string{"exit", "q"},
		Category:    "builtin",
	},
	{
		Name:        "clear",
		Description: "Clear conversation history",
		Aliases:     []string{"cls"},
		Category:    "builtin",
	},
	{
		Name:        "reset",
		Description: "Reset the current session",
		Category:    "builtin",
	},

	// Information
	{
		Name:        "help",
		Description: "Show available commands",
		Aliases:     []string{"h", "?"},
		Category:    "builtin",
	},
	{
		Name:        "version",
		Description: "Show IronClaw version",
		Aliases:     []string{"v"},
		Category:    "builtin",
	},

	// Memory management
	{
		Name:        "memory",
		Description: "Memory management commands",
		ArgHint:     "<list|search|clear>",
		Category:    "builtin",
	},

	// Skill management
	{
		Name:        "skills",
		Description: "List available skills",
		Aliases:     []string{"skill"},
		Category:    "builtin",
	},

	// Export/History
	{
		Name:        "export",
		Description: "Export conversation history",
		ArgHint:     "[filename]",
		Category:    "builtin",
	},
	{
		Name:        "history",
		Description: "Show conversation history",
		Aliases:     []string{"hist"},
		Category:    "builtin",
	},

	// Task resume
	{
		Name:        "resume",
		Description: "Resume task from last checkpoint",
		ArgHint:     "[session_id]",
		Category:    "builtin",
	},

	// Task ledger
	{
		Name:        "tasks",
		Description: "List active and recent tasks",
		Category:    "builtin",
	},

	// Team coordination
	{
		Name:        "team",
		Description: "Break a goal into parallel tasks and execute with agent team",
		ArgHint:     "<goal>",
		Category:    "builtin",
	},

	// Mode switching
	{
		Name:        "mode",
		Description: "Show or switch agent mode (linear). Usage: /mode [linear]",
		ArgHint:     "[linear]",
		Category:    "builtin",
		SubArgs:     []string{"linear"},
	},

	// Feature management
	{
		Name:        "feature",
		Description: "List, enable, or disable features. Usage: /feature [list|enable|disable] [name]",
		ArgHint:     "[list|enable|disable] [name]",
		Category:    "builtin",
		SubArgs:     []string{"list", "enable", "disable"},
	},

	// Config inspection
	{
		Name:        "config",
		Description: "Show current effective configuration",
		ArgHint:     "show",
		Category:    "builtin",
	},

	// Context compression
	{
		Name:        "compact",
		Description: "Manually trigger context compression",
		Category:    "builtin",
	},

	// Model switching
	{
		Name:        "model",
		Description: "Show or switch the current LLM model. Usage: /model [name]",
		ArgHint:     "[model_name]",
		Category:    "builtin",
	},
}

// localCommands lists slash commands handled entirely in the TUI
// without forwarding to the agent.
var localCommands = map[string]bool{
	"quit": true, "exit": true, "q": true,
	"clear": true, "cls": true,
	"help": true, "h": true, "?": true,
	"version": true, "v": true,
	"status": true, "stats": true,
	"history": true, "hist": true,
	"export": true,
	"model": true,
}

// isLocalCommand returns true if text is a slash command handled locally.
func isLocalCommand(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false
	}
	cmd := strings.TrimPrefix(parts[0], "/")
	return localCommands[strings.ToLower(cmd)]
}

// GetCommands returns all registered commands.
func GetCommands() []Command {
	return commandRegistry
}

// RegisterCommand adds a new command to the registry (for skills, etc.).
func RegisterCommand(cmd Command) {
	commandRegistry = append(commandRegistry, cmd)
}

// FilterCommands returns commands matching the given query.
// Empty query returns all commands.
func FilterCommands(query string) []Command {
	query = strings.ToLower(strings.TrimPrefix(query, "/"))

	if query == "" {
		return commandRegistry
	}

	var matches []Command
	var exactMatches []Command
	var prefixMatches []Command
	var fuzzyMatches []Command

	for _, cmd := range commandRegistry {
		cmdName := strings.ToLower(cmd.Name)

		// Exact match
		if cmdName == query {
			exactMatches = append(exactMatches, cmd)
			continue
		}

		// Prefix match
		if strings.HasPrefix(cmdName, query) {
			prefixMatches = append(prefixMatches, cmd)
			continue
		}

		// Check aliases
		for _, alias := range cmd.Aliases {
			aliasLower := strings.ToLower(alias)
			if aliasLower == query {
				exactMatches = append(exactMatches, cmd)
				break
			}
			if strings.HasPrefix(aliasLower, query) {
				prefixMatches = append(prefixMatches, cmd)
				break
			}
		}

		// Fuzzy match (contains)
		if strings.Contains(cmdName, query) {
			fuzzyMatches = append(fuzzyMatches, cmd)
		}
	}

	// Priority: exact > prefix > fuzzy
	matches = append(matches, exactMatches...)
	matches = append(matches, prefixMatches...)
	matches = append(matches, fuzzyMatches...)

	return matches
}
