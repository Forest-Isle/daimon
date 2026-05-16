package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CodeSearchResult is the tool-local view of a semantic code match.
type CodeSearchResult struct {
	FilePath  string
	StartLine int
	EndLine   int
	Content   string
	Score     float64
}

// SemanticSearchTool exposes semantic code search over the indexed repository.
type SemanticSearchTool struct {
	available func() bool
	search    func(query string, topK int) ([]CodeSearchResult, error)
}

// NewSemanticSearchTool creates a semantic search tool bound to a codebase index.
func NewSemanticSearchTool(
	available func() bool,
	search func(query string, topK int) ([]CodeSearchResult, error),
) *SemanticSearchTool {
	return &SemanticSearchTool{available: available, search: search}
}

func (t *SemanticSearchTool) Name() string { return "semantic_search" }

func (t *SemanticSearchTool) Description() string {
	return "Search the indexed codebase semantically and return the most relevant code chunks."
}

func (t *SemanticSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural-language code search query",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matches to return (default: 5)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SemanticSearchTool) Execute(_ context.Context, input []byte) (Result, error) {
	if t.available == nil || t.search == nil || !t.available() {
		return Result{Output: "semantic search unavailable: codebase index is not configured."}, nil
	}

	var in struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return Result{Error: "query is required"}, nil
	}

	results, err := t.search(in.Query, in.TopK)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if len(results) == 0 {
		return Result{Output: "no semantic matches found."}, nil
	}

	var b strings.Builder
	for i, chunk := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%d. %s:%d-%d (score %.3f)\n%s", i+1, chunk.FilePath, chunk.StartLine, chunk.EndLine, chunk.Score, chunk.Content)
	}

	return Result{
		Output: b.String(),
		Type:   ResultText,
		Metadata: map[string]any{
			"result_count": len(results),
		},
	}, nil
}

func (t *SemanticSearchTool) RequiresApproval() bool { return false }

func (t *SemanticSearchTool) IsReadOnly() bool { return true }
