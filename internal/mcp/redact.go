package mcp

import (
	"regexp"
	"strings"
)

// redactionPatterns defines regex patterns for common credential formats.
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

	// GitHub personal access tokens
	{"github_pat", regexp.MustCompile(`(ghp_|gho_|ghs_|ghr_|github_pat_)[A-Za-z0-9_]{30,}`)},

	// Bearer tokens in header-like strings
	{"bearer", regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9\-._~+/]{20,}=*`)},

	// Basic auth in header-like strings
	{"basic_auth", regexp.MustCompile(`(?i)(Basic\s+)[A-Za-z0-9+/]{20,}=*`)},

	// JSON fields containing sensitive values
	{"json_secret", regexp.MustCompile(`"(?i:password|passwd|pwd|secret|token|api_key|apikey|access_token|auth)"\s*:\s*"[^"]*"`)},

	// AWS access key IDs
	{"aws_key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
}

const redactedPlaceholder = "[REDACTED]"

// Redact replaces any recognised credential patterns in s with [REDACTED].
func Redact(s string) string {
	for _, rp := range redactionPatterns {
		s = rp.pattern.ReplaceAllStringFunc(s, func(match string) string {
			if rp.label == "bearer" || rp.label == "basic_auth" {
				idx := strings.IndexByte(match, ' ')
				if idx >= 0 {
					return match[:idx+1] + redactedPlaceholder
				}
			}
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
