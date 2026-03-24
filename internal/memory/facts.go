package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Completer is a local interface for LLM completion, avoiding circular import with agent package.
// The gateway injects a concrete adapter.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userMessage string) (string, error)
}

// ExtractedFact is a single fact extracted from a conversation message.
type ExtractedFact struct {
	Content  string `json:"content"`
	Category string `json:"category"` // e.g. "preference", "fact", "task", "relationship"
}

// LLMFactExtractor extracts distilled facts from raw conversation messages using an LLM.
type LLMFactExtractor struct {
	completer Completer
	enabled   bool
}

// NewLLMFactExtractor creates a new LLMFactExtractor.
func NewLLMFactExtractor(completer Completer, cfg MemoryConfig) *LLMFactExtractor {
	return &LLMFactExtractor{
		completer: completer,
		enabled:   cfg.FactExtraction,
	}
}

const factExtractionSystemPrompt = `You are a memory.md distiller. Given a conversation exchange, extract concise, standalone facts worth remembering for future context.

Rules:
1. Output ONLY a JSON array of fact objects, no prose.
2. Each fact must be self-contained (understandable without context).
3. Include only facts that would be useful in future conversations: preferences, identities, goals, learned facts.
4. Ignore ephemeral details (current time, temporary states).
5. Maximum 5 facts per extraction.
6. Each fact object: {"content": "<fact>", "category": "<preference|fact|task|relationship|identity>"}

If no memorable facts are present, output: []`

// Extract extracts facts from a goal/outcome pair.
func (e *LLMFactExtractor) Extract(ctx context.Context, goal, outcome string) ([]ExtractedFact, error) {
	if !e.enabled || e.completer == nil {
		return nil, nil
	}

	userMsg := fmt.Sprintf("USER GOAL: %s\n\nOUTCOME/RESPONSE: %s", goal, outcome)
	resp, err := e.completer.Complete(ctx, factExtractionSystemPrompt, userMsg)
	if err != nil {
		return nil, fmt.Errorf("fact extraction LLM call: %w", err)
	}

	return parseFacts(resp)
}

// parseFacts extracts a JSON array of ExtractedFact from raw LLM text output.
func parseFacts(text string) ([]ExtractedFact, error) {
	text = strings.TrimSpace(text)
	// Try to find JSON array boundaries.
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil, nil // no facts
	}
	jsonStr := text[start : end+1]

	var facts []ExtractedFact
	if err := json.Unmarshal([]byte(jsonStr), &facts); err != nil {
		return nil, fmt.Errorf("parse facts JSON: %w", err)
	}
	return facts, nil
}
