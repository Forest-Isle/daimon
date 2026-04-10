package agent

import "strings"

// ModelContextWindow returns the context window size for a given model name.
// Falls back to 200000 (Claude default) if model is unknown.
func ModelContextWindow(model string) int {
	// Claude models
	if strings.Contains(model, "opus") {
		if strings.Contains(model, "opus-4") {
			return 800000 // Claude 4 Opus (200K legacy context, 800K with extrapolation)
		}
		return 200000 // Claude 3.5 Opus
	}
	if strings.Contains(model, "sonnet") {
		if strings.Contains(model, "sonnet-4") {
			return 400000 // Claude 4 Sonnet (400K context)
		}
		return 200000 // Claude 3.5 Sonnet
	}
	if strings.Contains(model, "haiku") {
		return 200000 // Claude 3.5 Haiku
	}

	// GPT models (if provider switches to OpenAI)
	if strings.Contains(model, "gpt-4") {
		if strings.Contains(model, "turbo") {
			return 128000 // GPT-4 Turbo
		}
		return 8192 // GPT-4 standard
	}
	if strings.Contains(model, "gpt-3.5") {
		return 4096
	}

	// Default fallback (Claude default)
	return 200000
}
