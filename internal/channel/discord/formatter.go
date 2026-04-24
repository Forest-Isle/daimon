package discord

// FormatForDiscord converts text to Discord-safe format.
// Discord supports a subset of Markdown natively.
func FormatForDiscord(text string) string {
	if text == "" {
		return "(empty response)"
	}

	// Discord has a 2000 character limit per message
	if len(text) > 1950 {
		text = text[:1950] + "\n... (truncated)"
	}

	return text
}
