package gateway

import "testing"

func TestSummarizeToolInput(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{"bash command", "bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"file path", "file_read", `{"file_path":"/etc/hosts"}`, "/etc/hosts"},
		{"query", "grep", `{"pattern":"TODO"}`, "TODO"},
		{"newlines collapsed", "bash", `{"command":"a\nb"}`, "a b"},
		{"no recognizable field", "x", `{"foo":"bar"}`, ""},
		{"invalid json", "x", `not json`, ""},
		{"empty", "x", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeToolInput(tt.tool, tt.input); got != tt.want {
				t.Errorf("summarizeToolInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestClampSummaryLongUnicode(t *testing.T) {
	// 80 CJK runes; must clamp to 60 runes + ellipsis and stay valid.
	long := ""
	for i := 0; i < 80; i++ {
		long += "测"
	}
	got := clampSummary(long)
	if r := []rune(got); len(r) != 61 { // 60 + "…"
		t.Errorf("expected 61 runes after clamp, got %d (%q)", len(r), got)
	}
}
