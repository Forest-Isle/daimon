package tui

import (
	"strings"
)

// ArgCompleter is a function that returns candidate argument strings for a
// given slash command and the argument prefix typed so far.
// cmd is the command name (without slash, e.g. "feature").
// subCmd is the first sub-argument if already typed (e.g. "enable"), or "".
// argSoFar is the partial string the user is currently typing.
type ArgCompleter func(cmd, subCmd, argSoFar string) []string

// SuggestionItem represents a single suggestion in the autocomplete list.
type SuggestionItem struct {
	Command     Command
	DisplayText string
	// ArgValue is set when this suggestion completes an argument rather than
	// a command name. The full replacement string is stored in DisplayText.
	ArgValue string
}

// GenerateSuggestions creates a list of suggestions based on the current input.
// Returns nil if suggestions should not be shown.
// argCompleter is optional; when non-nil it provides dynamic argument candidates.
func GenerateSuggestions(input string, cursorPos int, argCompleter ArgCompleter) []SuggestionItem {
	// Only show suggestions if input starts with "/"
	if !strings.HasPrefix(input, "/") {
		return nil
	}

	parts := strings.SplitN(input, " ", -1)
	commandPart := parts[0] // e.g. "/feature"

	// If there is no space yet, complete the command name
	if len(parts) == 1 {
		commands := FilterCommands(commandPart)
		if len(commands) == 0 {
			return nil
		}
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

	// We're past the command name — complete arguments
	cmdName := strings.ToLower(strings.TrimPrefix(commandPart, "/"))
	cmd := findCommand(cmdName)
	if cmd == nil {
		return nil
	}

	// parts[1..] are the already-typed arguments
	typedArgs := parts[1:]
	// Determine sub-command and current partial arg
	var subCmd, argSoFar string
	switch len(typedArgs) {
	case 0:
		// Just a trailing space after command name
	case 1:
		argSoFar = typedArgs[0]
	default:
		subCmd = typedArgs[0]
		argSoFar = typedArgs[len(typedArgs)-1]
	}

	var candidates []string

	// First try dynamic completer (gateway-provided feature names etc.)
	if argCompleter != nil {
		candidates = argCompleter(cmdName, subCmd, argSoFar)
	}

	// Fall back to static SubArgs for the first argument position
	if len(candidates) == 0 && subCmd == "" && len(cmd.SubArgs) > 0 {
		candidates = cmd.SubArgs
	}

	if len(candidates) == 0 {
		return nil
	}

	// Filter candidates by prefix
	prefix := strings.ToLower(argSoFar)
	var filtered []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), prefix) {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil
	}

	// Build suggestion items — display text is the full completed command line
	baseCmd := "/" + cmdName
	if subCmd != "" {
		baseCmd += " " + subCmd
	}

	suggestions := make([]SuggestionItem, 0, len(filtered))
	for _, c := range filtered {
		displayText := baseCmd + " " + c
		suggestions = append(suggestions, SuggestionItem{
			Command:     *cmd,
			DisplayText: displayText,
			ArgValue:    c,
		})
	}
	return suggestions
}

// findCommand returns the Command with the given name (or alias), or nil.
func findCommand(name string) *Command {
	for i, cmd := range commandRegistry {
		if strings.ToLower(cmd.Name) == name {
			return &commandRegistry[i]
		}
		for _, alias := range cmd.Aliases {
			if strings.ToLower(alias) == name {
				return &commandRegistry[i]
			}
		}
	}
	return nil
}

// ApplySuggestion replaces the current input with the selected suggestion.
func ApplySuggestion(input string, suggestion SuggestionItem) string {
	if suggestion.ArgValue != "" {
		// Argument completion — replace the last token with the candidate
		parts := strings.Fields(input)
		if len(parts) == 0 {
			return input
		}
		// Drop the last (partial) token and append the completed value
		if len(parts) > 1 {
			// Trailing space means user finished previous word and is starting next
			if strings.HasSuffix(input, " ") {
				return input + suggestion.ArgValue + " "
			}
			parts[len(parts)-1] = suggestion.ArgValue
			return strings.Join(parts, " ") + " "
		}
		return "/" + suggestion.Command.Name + " " + suggestion.ArgValue + " "
	}

	// Command name completion — original behaviour
	parts := strings.SplitN(input, " ", 2)
	newInput := "/" + suggestion.Command.Name
	if len(parts) > 1 {
		newInput += " " + parts[1]
	} else {
		newInput += " "
	}
	return newInput
}
