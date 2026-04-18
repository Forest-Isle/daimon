package tui

import (
	"strings"
)

// Command represents a slash command available in the TUI.
type Command struct {
	Name        string
	Description string
	Aliases     []string
	ArgHint     string // e.g., "<message>" or "[options]"
	Category    string // "builtin", "skill", etc.
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
	{
		Name:        "status",
		Description: "Show current session status",
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

	// Insights
	{
		Name:        "insights",
		Description: "Show evolution insights",
		ArgHint:     "[days]",
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
