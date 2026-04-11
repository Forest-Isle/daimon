package tui

import (
	"testing"
)

func TestFilterCommands(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected int // expected number of matches
	}{
		{"empty query returns all", "", 4},
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

func TestGenerateSuggestions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool // should return suggestions
	}{
		{"slash at start", "/", true},
		{"slash with partial command", "/qu", true},
		{"no slash", "hello", false},
		{"slash in middle", "hello /", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestions := GenerateSuggestions(tt.input, len(tt.input))
			hasSuggestions := len(suggestions) > 0
			if hasSuggestions != tt.expected {
				t.Errorf("GenerateSuggestions(%q) returned suggestions=%v, expected=%v",
					tt.input, hasSuggestions, tt.expected)
			}
		})
	}
}

func TestApplySuggestion(t *testing.T) {
	cmd := Command{Name: "quit", Description: "Exit"}
	suggestion := SuggestionItem{Command: cmd, DisplayText: "/quit"}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"replace command only", "/q", "/quit "},
		{"replace with args", "/q arg1", "/quit arg1"},
		{"exact match", "/quit", "/quit "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplySuggestion(tt.input, suggestion)
			if result != tt.expected {
				t.Errorf("ApplySuggestion(%q) = %q, expected %q",
					tt.input, result, tt.expected)
			}
		})
	}
}
