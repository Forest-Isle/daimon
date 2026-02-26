package telegram

import "strings"

// FormatForTelegram converts markdown to Telegram-safe format.
// Telegram supports a subset of Markdown — we do minimal escaping.
func FormatForTelegram(text string) string {
	if text == "" {
		return "(empty response)"
	}

	// Telegram has a 4096 character limit per message
	if len(text) > 4000 {
		text = text[:4000] + "\n... (truncated)"
	}

	return text
}

// EscapeMarkdown escapes special Telegram MarkdownV2 characters.
func EscapeMarkdown(text string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(text)
}
