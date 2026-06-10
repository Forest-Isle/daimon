package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterCommands(t *testing.T) {
	allCommands := GetCommands()
	totalCommands := len(allCommands)

	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"empty query returns all", "", totalCommands},
		{"exact match", "/quit", 1},
		{"prefix match", "/q", 1},
		{"partial match", "/hel", 1},
		{"no match", "/xyz", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := FilterCommands(tt.query)
			if len(results) != tt.expected {
				t.Errorf("FilterCommands(%q) returned %d results, expected %d",
					tt.query, len(results), tt.expected)
			}
		})
	}
}

func TestGenerateSuggestions_CommandNames(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantSuggest bool
	}{
		{"slash at start", "/", true},
		{"slash with partial command", "/qu", true},
		{"no slash", "hello", false},
		{"slash in middle", "hello /", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestions := GenerateSuggestions(tt.input, len(tt.input), nil)
			hasSuggestions := len(suggestions) > 0
			if hasSuggestions != tt.wantSuggest {
				t.Errorf("GenerateSuggestions(%q) returned suggestions=%v, expected=%v",
					tt.input, hasSuggestions, tt.wantSuggest)
			}
		})
	}
}

func TestGenerateSuggestions_StaticSubArgs(t *testing.T) {
	// /feature has SubArgs: list, enable, disable
	suggestions := GenerateSuggestions("/feature ", 9, nil)
	require.NotEmpty(t, suggestions)
	var names []string
	for _, s := range suggestions {
		names = append(names, s.ArgValue)
	}
	assert.Contains(t, names, "list")
	assert.Contains(t, names, "enable")
	assert.Contains(t, names, "disable")
}

func TestGenerateSuggestions_StaticSubArgs_Filtered(t *testing.T) {
	suggestions := GenerateSuggestions("/feature en", 11, nil)
	require.NotEmpty(t, suggestions)
	for _, s := range suggestions {
		assert.Equal(t, "enable", s.ArgValue)
	}
}

func TestGenerateSuggestions_DynamicArgCompleter(t *testing.T) {
	completer := func(cmd, subCmd, argSoFar string) []string {
		if cmd == "feature" && subCmd == "enable" {
			return []string{"scheduler"}
		}
		return nil
	}

	suggestions := GenerateSuggestions("/feature enable s", 17, completer)
	require.Len(t, suggestions, 1)
	assert.Equal(t, "scheduler", suggestions[0].ArgValue)
}

func TestApplySuggestion_CommandName(t *testing.T) {
	cmd := Command{Name: "quit", Description: "Exit"}
	suggestion := SuggestionItem{Command: cmd, DisplayText: "/quit"}

	tests := []struct {
		input    string
		expected string
	}{
		{"/q", "/quit "},
		{"/q arg1", "/quit arg1"},
		{"/quit", "/quit "},
	}

	for _, tt := range tests {
		result := ApplySuggestion(tt.input, suggestion)
		assert.Equal(t, tt.expected, result, "input: %q", tt.input)
	}
}

func TestApplySuggestion_ArgValue(t *testing.T) {
	cmd := Command{Name: "feature"}
	suggestion := SuggestionItem{
		Command:     cmd,
		DisplayText: "/feature enable scheduler",
		ArgValue:    "scheduler",
	}

	result := ApplySuggestion("/feature enable s", suggestion)
	assert.Equal(t, "/feature enable scheduler ", result)
}
