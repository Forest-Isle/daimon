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
	{
		Name:        "quit",
		Description: "Exit the TUI",
		Category:    "builtin",
	},
	{
		Name:        "clear",
		Description: "Clear conversation history",
		Category:    "builtin",
	},
	{
		Name:        "help",
		Description: "Show available commands",
		Category:    "builtin",
	},
	{
		Name:        "reset",
		Description: "Reset the current session",
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
