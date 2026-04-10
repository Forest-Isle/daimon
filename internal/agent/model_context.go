package agent

import "strings"

// ModelContextWindow returns the context window size for a given model name.
// Falls back to 200000 (Claude default) if model is unknown.
//
// Context windows for known models:
// - Claude 4 Opus: 800K (with extrapolation)
// - Claude 4 Sonnet: 400K (2025 models like sonnet-4-20250514)
// - Claude 3.5 Opus/Sonnet/Haiku: 200K
// - GPT-4 Turbo: 128K
// - GPT-4: 8K
// - GPT-3.5-turbo: 4K
func ModelContextWindow(model string) int {
	// Claude 4 models (2025+)
	if strings.Contains(model, "opus-4-1") || strings.Contains(model, "opus-4-20") {
		return 800000 // Claude 4 Opus
	}
	if strings.Contains(model, "sonnet-4-20") {
		return 400000 // Claude 4 Sonnet (2025)
	}

	// Claude 3.5 models (earlier versions)
	if strings.Contains(model, "opus") {
		return 200000
	}
	if strings.Contains(model, "sonnet") {
		return 200000
	}
	if strings.Contains(model, "haiku") {
		return 200000
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

	// Default fallback (Claude 3.5 default)
	return 200000
}
