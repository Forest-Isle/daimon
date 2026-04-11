package tui

import (
	"strings"
)

// SuggestionItem represents a single suggestion in the autocomplete list.
type SuggestionItem struct {
	Command     Command
	DisplayText string
}

// GenerateSuggestions creates a list of suggestions based on the current input.
// Returns nil if suggestions should not be shown.
func GenerateSuggestions(input string, cursorPos int) []SuggestionItem {
	// Only show suggestions if input starts with "/"
	if !strings.HasPrefix(input, "/") {
		return nil
	}

	// Extract the command part (everything after "/" until first space or end)
	parts := strings.SplitN(input, " ", 2)
	commandPart := parts[0]

	// Filter commands
	commands := FilterCommands(commandPart)
	if len(commands) == 0 {
		return nil
	}

	// Convert to suggestion items
	suggestions := make([]SuggestionItem, 0, len(commands))
	for _, cmd := range commands {
		displayText := "/" + cmd.Name
		if cmd.ArgHint != "" {
			displayText += " " + cmd.ArgHint
		}
		suggestions = append(suggestions, SuggestionItem{
			Command:     cmd,
			DisplayText: displayText,
		})
	}

	return suggestions
}

// ApplySuggestion replaces the current input with the selected suggestion.
func ApplySuggestion(input string, suggestion SuggestionItem) string {
	// Replace the command part with the selected command
	parts := strings.SplitN(input, " ", 2)
	newInput := "/" + suggestion.Command.Name

	// If there were arguments, preserve them
	if len(parts) > 1 {
		newInput += " " + parts[1]
	} else {
		// Add a space after the command for easy argument entry
		newInput += " "
	}

	return newInput
}
