// Package logging provides log-level utilities including credential redaction.
package logging

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

// redactionPatterns defines regex patterns for common credential formats.
// Each entry maps a human-readable label to its compiled pattern.
var redactionPatterns = []struct {
	label   string
	pattern *regexp.Regexp
}{
	// OpenAI-style API keys: sk-... or sk-proj-...
	{"openai_key", regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`)},

	// Anthropic API keys: sk-ant-...
	{"anthropic_key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`)},

	// Generic key-... prefixed tokens
	{"key_prefix", regexp.MustCompile(`key-[A-Za-z0-9_\-]{20,}`)},

	// GitHub personal access tokens (classic: ghp_, fine-grained: github_pat_)
	{"github_pat", regexp.MustCompile(`(ghp_|gho_|ghs_|ghr_|github_pat_)[A-Za-z0-9_]{30,}`)},

	// Bearer tokens in header-like strings
	{"bearer", regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9\-._~+/]{20,}=*`)},

	// Basic auth in header-like strings
	{"basic_auth", regexp.MustCompile(`(?i)(Basic\s+)[A-Za-z0-9+/]{20,}=*`)},

	// JSON fields containing sensitive values: "password":"...", "secret":"...", etc.
	{"json_secret", regexp.MustCompile(`"(?i:password|passwd|pwd|secret|token|api_key|apikey|access_token|auth)"\s*:\s*"[^"]*"`)},

	// AWS access key IDs
	{"aws_key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
}

const redactedPlaceholder = "[REDACTED]"

// Redact replaces any recognised credential patterns in s with [REDACTED].
func Redact(s string) string {
	for _, rp := range redactionPatterns {
		s = rp.pattern.ReplaceAllStringFunc(s, func(match string) string {
			// For patterns with a prefix group (Bearer, Basic), keep the prefix visible.
			if rp.label == "bearer" || rp.label == "basic_auth" {
				idx := strings.IndexByte(match, ' ')
				if idx >= 0 {
					return match[:idx+1] + redactedPlaceholder
				}
			}
			// For JSON fields, keep the key but redact the value.
			if rp.label == "json_secret" {
				colonIdx := strings.Index(match, ":")
				if colonIdx >= 0 {
					return match[:colonIdx+1] + ` "` + redactedPlaceholder + `"`
				}
			}
			return redactedPlaceholder
		})
	}
	return s
}

// RedactingHandler is an slog.Handler wrapper that redacts sensitive data from
// log record messages and attribute values before passing them to the inner handler.
type RedactingHandler struct {
	inner slog.Handler
}

// NewRedactingHandler creates a handler that wraps inner and redacts credentials.
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle redacts the message and string attributes, then delegates to inner.
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Redact the message itself.
	r.Message = Redact(r.Message)

	// Redact string attribute values.
	redacted := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		redacted = append(redacted, redactAttr(a))
		return true
	})

	// Build a new record with redacted attributes.
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	newRecord.AddAttrs(redacted...)
	return h.inner.Handle(ctx, newRecord)
}

// WithAttrs returns a new handler with the given attrs, redacting values.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	ra := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		ra[i] = redactAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(ra)}
}

// WithGroup returns a new handler with the given group name.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr recursively redacts string values within an slog.Attr.
func redactAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(a.Value.String()))
	case slog.KindGroup:
		attrs := a.Value.Group()
		redacted := make([]slog.Attr, len(attrs))
		for i, ga := range attrs {
			redacted[i] = redactAttr(ga)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(redacted...)}
	default:
		return a
	}
}
