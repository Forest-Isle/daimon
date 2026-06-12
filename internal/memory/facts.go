package memory

import "context"

// Completer is a local interface for LLM completion, avoiding circular import with agent package.
// The gateway injects a concrete adapter.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// ExtractedFact is a fact candidate used by memory lifecycle decisions.
type ExtractedFact struct {
	Content     string `json:"content"`
	Category    string `json:"category"`   // e.g. "preference", "fact", "task", "relationship", "identity"
	Type        string `json:"type"`       // "episodic", "semantic", "procedural"
	Importance  int    `json:"importance"` // 1-10
	Emotion     string `json:"emotion"`    // "positive", "negative", "neutral"
	Sensitivity string `json:"-"`          // set by PII detection, not from LLM
}
