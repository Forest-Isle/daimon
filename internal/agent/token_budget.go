package agent

import "github.com/Forest-Isle/IronClaw/internal/session"

// BudgetAction represents the recommended compression level based on context utilization.
type BudgetAction int

const (
	BudgetOK             BudgetAction = iota // below all thresholds
	BudgetCompressLight                       // light compression needed
	BudgetCompressMedium                      // medium compression needed
	BudgetCompressHeavy                       // aggressive compression needed
)

// String returns a human-readable name for the BudgetAction.
func (a BudgetAction) String() string {
	switch a {
	case BudgetCompressLight:
		return "compress_light"
	case BudgetCompressMedium:
		return "compress_medium"
	case BudgetCompressHeavy:
		return "compress_heavy"
	default:
		return "ok"
	}
}

// BudgetCheck holds the result of a token budget evaluation.
type BudgetCheck struct {
	TotalChars int
	UsageRatio float64
	Action     BudgetAction
}

// TokenBudget monitors context window utilization and recommends compression levels.
type TokenBudget struct {
	ModelLimit      int     // context window size in tokens
	LightThreshold  float64 // fraction triggering light compression (default 0.70)
	MediumThreshold float64 // fraction triggering medium compression (default 0.80)
	HeavyThreshold  float64 // fraction triggering heavy compression (default 0.90)
	EstimateRatio   float64 // chars-to-tokens ratio (default 0.25)
}

// NewTokenBudget creates a TokenBudget with the given parameters.
// Zero values are replaced with defaults: modelLimit=200000, light=0.70, medium=0.80, heavy=0.90, ratio=0.25.
func NewTokenBudget(modelLimit int, light, medium, heavy, estimateRatio float64) *TokenBudget {
	if modelLimit <= 0 {
		modelLimit = 200000
	}
	if light <= 0 {
		light = 0.70
	}
	if medium <= 0 {
		medium = 0.80
	}
	if heavy <= 0 {
		heavy = 0.90
	}
	if estimateRatio <= 0 {
		estimateRatio = 0.25
	}
	return &TokenBudget{
		ModelLimit:      modelLimit,
		LightThreshold:  light,
		MediumThreshold: medium,
		HeavyThreshold:  heavy,
		EstimateRatio:   estimateRatio,
	}
}

// Check evaluates the current context size and returns the appropriate BudgetAction.
// It counts chars from systemPrompt + all message Content/ToolInput fields + 20 overhead per message.
func (tb *TokenBudget) Check(messages []session.Message, systemPrompt string) BudgetCheck {
	totalChars := len(systemPrompt)
	for _, m := range messages {
		totalChars += len(m.Content) + len(m.ToolInput) + 20
	}

	ratio := EstimateUtilization(totalChars, tb.EstimateRatio, tb.ModelLimit)

	var action BudgetAction
	switch {
	case ratio >= tb.HeavyThreshold:
		action = BudgetCompressHeavy
	case ratio >= tb.MediumThreshold:
		action = BudgetCompressMedium
	case ratio >= tb.LightThreshold:
		action = BudgetCompressLight
	default:
		action = BudgetOK
	}

	return BudgetCheck{
		TotalChars: totalChars,
		UsageRatio: ratio,
		Action:     action,
	}
}
