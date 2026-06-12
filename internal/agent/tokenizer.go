package agent

import (
	"fmt"
	"log/slog"
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/Forest-Isle/daimon/internal/session"
)

// Tokenizer counts tokens for context management.
type Tokenizer interface {
	// Count returns the token count for a single text string.
	Count(text string) int
	// CountMessages returns the total token count for a list of messages
	// plus the system prompt, including per-message overhead.
	CountMessages(msgs []session.Message, systemPrompt string) int
}

// --- TiktokenTokenizer ---

// TiktokenTokenizer uses tiktoken-go for accurate token counting.
type TiktokenTokenizer struct {
	enc *tiktoken.Tiktoken
}

// NewTiktokenTokenizer creates a tokenizer using the tiktoken encoding
// appropriate for the given model. Returns an error if the model encoding
// cannot be resolved.
func NewTiktokenTokenizer(model string) (*TiktokenTokenizer, error) {
	encoding := modelToEncoding(model)
	enc, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("tiktoken encoding %q for model %q: %w", encoding, model, err)
	}
	return &TiktokenTokenizer{enc: enc}, nil
}

// maxDirectEncodeLen is the maximum text length that will be encoded directly
// by tiktoken. Longer texts are sampled and extrapolated to avoid O(n^2)
// BPE performance on very large inputs.
const maxDirectEncodeLen = 32768

func (t *TiktokenTokenizer) Count(text string) int {
	if len(text) <= maxDirectEncodeLen {
		return len(t.enc.Encode(text, nil, nil))
	}
	// Sample: encode the first chunk and extrapolate.
	sampleTokens := len(t.enc.Encode(text[:maxDirectEncodeLen], nil, nil))
	ratio := float64(sampleTokens) / float64(maxDirectEncodeLen)
	return int(float64(len(text)) * ratio)
}

func (t *TiktokenTokenizer) CountMessages(msgs []session.Message, systemPrompt string) int {
	total := t.Count(systemPrompt)
	// Per-message overhead: role tag + separators ≈ 4 tokens.
	const perMessageOverhead = 4
	for _, m := range msgs {
		total += t.Count(m.Content) + t.Count(m.ToolInput) + perMessageOverhead
	}
	return total
}

// modelToEncoding maps a model name to its tiktoken encoding.
// Claude models use cl100k_base (same BPE family as GPT-4).
func modelToEncoding(model string) string {
	switch {
	case strings.Contains(model, "gpt-4"), strings.Contains(model, "gpt-3.5"):
		return "cl100k_base"
	default:
		// Claude and unknown models: cl100k_base is the closest widely
		// available encoding and provides a reasonable approximation.
		return "cl100k_base"
	}
}

// --- RatioTokenizer ---

// RatioTokenizer estimates tokens using a character-to-token ratio.
// This is the legacy fallback when tiktoken is unavailable.
type RatioTokenizer struct {
	Ratio float64
}

func (r *RatioTokenizer) Count(text string) int {
	return int(float64(len(text)) * r.Ratio)
}

func (r *RatioTokenizer) CountMessages(msgs []session.Message, systemPrompt string) int {
	totalChars := len(systemPrompt)
	for _, m := range msgs {
		totalChars += len(m.Content) + len(m.ToolInput) + 20
	}
	return int(float64(totalChars) * r.Ratio)
}

// --- Factory ---

// NewTokenizer creates a Tokenizer. It tries tiktoken-go first; if that
// fails (e.g. missing encoding data), it falls back to a ratio-based
// estimator using the provided ratio.
func NewTokenizer(model string, ratio float64) Tokenizer {
	tok, err := NewTiktokenTokenizer(model)
	if err != nil {
		slog.Warn("tokenizer: tiktoken unavailable, falling back to ratio estimator",
			"model", model, "err", err)
		if ratio <= 0 {
			ratio = 0.25
		}
		return &RatioTokenizer{Ratio: ratio}
	}
	return tok
}
