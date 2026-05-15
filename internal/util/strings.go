// Package util provides shared helper functions used across IronClaw packages.
package util

// TruncateStr truncates a string to maxLen bytes, appending "..." when
// truncation occurs. If the string is shorter than maxLen, it is returned
// unchanged. For Unicode-safe truncation, use TruncateRunes.
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TruncateRunes truncates a string to maxRunes runes, appending "…" when
// truncation occurs. This preserves multi-byte characters correctly. If the
// string has fewer runes than maxRunes, it is returned unchanged.
func TruncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}
