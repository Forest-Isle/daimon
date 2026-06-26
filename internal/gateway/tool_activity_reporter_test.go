package gateway

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapOutputByteCapRuneSafe(t *testing.T) {
	// A single line of 3-byte runes longer than the byte cap forces the
	// byte-truncation path; the result must stay valid UTF-8 (no split rune).
	var b strings.Builder
	for b.Len() <= maxOutputBytes+12 {
		b.WriteString("世")
	}
	got := capOutput(b.String())

	body := strings.TrimSuffix(got, "\n… (truncated)")
	if !utf8.ValidString(body) {
		t.Errorf("capOutput must trim to a valid rune boundary; got invalid UTF-8")
	}
	if len(body) > maxOutputBytes {
		t.Errorf("capOutput body = %d bytes, want <= %d", len(body), maxOutputBytes)
	}
	if !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("expected truncation marker, got tail %q", got[len(got)-12:])
	}
}

func TestDeriveResultSummary(t *testing.T) {
	tests := []struct {
		name    string
		errText string
		output  string
		want    string
	}{
		{"error wins", "permission denied\nstack", "ignored", "error: permission denied"},
		{"empty output", "", "", "done"},
		{"single line", "", "hello world", "hello world"},
		{"multi line", "", "a\nb\nc", "3 lines"},
		{"trailing newline single", "", "one\n", "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveResultSummary(tt.errText, tt.output); got != tt.want {
				t.Errorf("deriveResultSummary(%q,%q) = %q, want %q", tt.errText, tt.output, got, tt.want)
			}
		})
	}
}

func TestCapOutputLines(t *testing.T) {
	var b []byte
	for i := 0; i < 100; i++ {
		b = append(b, 'x', '\n')
	}
	got := capOutput(string(b))
	if n := len(splitLines(got)); n > maxOutputLines+1 {
		t.Errorf("capOutput kept %d lines, want <= %d", n, maxOutputLines+1)
	}
	if got[len(got)-len("(truncated)"):] != "(truncated)" {
		t.Errorf("expected truncation marker, got tail %q", got)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
