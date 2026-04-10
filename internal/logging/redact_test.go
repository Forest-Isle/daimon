package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // substring that must NOT appear in the output
		check string // substring that MUST appear in the output
	}{
		{
			name:  "openai api key",
			input: "using key sk-abc123def456ghi789jkl012mno345pqr678stu901vwx",
			want:  "sk-abc123",
			check: "[REDACTED]",
		},
		{
			name:  "anthropic api key",
			input: "key is sk-ant-abc123def456ghi789jkl012",
			want:  "sk-ant-abc123",
			check: "[REDACTED]",
		},
		{
			name:  "github pat classic",
			input: "token ghp_1234567890abcdefghijklmnopqrstuvwxyz",
			want:  "ghp_1234567890",
			check: "[REDACTED]",
		},
		{
			name:  "github pat fine-grained",
			input: "token github_pat_1234567890abcdefghijklmnopqrstuvwxyz",
			want:  "github_pat_1234567890",
			check: "[REDACTED]",
		},
		{
			name:  "bearer token",
			input: "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			want:  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			check: "Bearer [REDACTED]",
		},
		{
			name:  "json password field",
			input: `{"password": "mysecretpassword123"}`,
			want:  "mysecretpassword123",
			check: "[REDACTED]",
		},
		{
			name:  "aws access key",
			input: "aws key AKIAIOSFODNN7EXAMPLE",
			want:  "AKIAIOSFODNN7EXAMPLE",
			check: "[REDACTED]",
		},
		{
			name:  "no secrets - unchanged",
			input: "this is a normal log message with no secrets",
			want:  "",
			check: "this is a normal log message with no secrets",
		},
		{
			name:  "key prefix token",
			input: "using key-abc123def456ghi789jkl012mno",
			want:  "key-abc123def456",
			check: "[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if tt.want != "" && strings.Contains(got, tt.want) {
				t.Errorf("Redact(%q) still contains secret %q: got %q", tt.input, tt.want, got)
			}
			if !strings.Contains(got, tt.check) {
				t.Errorf("Redact(%q) missing expected %q: got %q", tt.input, tt.check, got)
			}
		})
	}
}

func TestRedactingHandler(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewRedactingHandler(inner)
	logger := slog.New(handler)

	// Log a message containing a secret in both message and attribute.
	logger.Info("connecting with sk-abc123def456ghi789jkl012mno345pqr678",
		"token", "ghp_1234567890abcdefghijklmnopqrstuvwxyz",
	)

	output := buf.String()

	if strings.Contains(output, "sk-abc123def456") {
		t.Errorf("handler output still contains OpenAI key: %s", output)
	}
	if strings.Contains(output, "ghp_1234567890abcdefghij") {
		t.Errorf("handler output still contains GitHub PAT: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Errorf("handler output missing [REDACTED] placeholder: %s", output)
	}
}

func TestRedactingHandlerEnabled(t *testing.T) {
	inner := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	handler := NewRedactingHandler(inner)

	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected Enabled(Debug) to be false when inner level is Warn")
	}
	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("expected Enabled(Warn) to be true when inner level is Warn")
	}
}
