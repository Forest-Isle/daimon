package tool

import (
	"context"
	"encoding/json"
	"strings"
)

type ToolSearchTool struct {
	catalog  *DeferredCatalog
	registry *Registry
}

func NewToolSearchTool(catalog *DeferredCatalog, registry *Registry) *ToolSearchTool {
	return &ToolSearchTool{catalog: catalog, registry: registry}
}

func (t *ToolSearchTool) Name() string { return "tool_search" }

func (t *ToolSearchTool) Description() string {
	return "Search deferred tools by keyword. Use query \"select:<tool_name>\" to resolve exact tools and make them callable in the next model call."
}

func (t *ToolSearchTool) RequiresApproval() bool { return false }
func (t *ToolSearchTool) IsReadOnly() bool       { return true }

func (t *ToolSearchTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		IsReadOnly:      true,
		IsDestructive:   false,
		RequiresNetwork: false,
		ApprovalMode:    "never",
		ParallelSafety:  ParallelSafe,
	}
}

func (t *ToolSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Keyword search, or select:<tool_name>[,<tool_name>...] to resolve exact deferred tools.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum keyword search results to return. Defaults to 8.",
			},
		},
		"required": []string{"query"},
	}
}

type toolSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type toolSearchResponse struct {
	Query    string              `json:"query"`
	Mode     string              `json:"mode"`
	Matches  []DeferredToolMatch `json:"matches,omitempty"`
	Resolved []DeferredToolMatch `json:"resolved,omitempty"`
	Errors   []string            `json:"errors,omitempty"`
}

func (t *ToolSearchTool) Execute(ctx context.Context, input []byte) (Result, error) {
	var in toolSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{Error: "invalid input: " + err.Error()}, nil
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return Result{Error: "query is required"}, nil
	}

	resp := toolSearchResponse{Query: query}
	if names, ok := parseToolSelectQuery(query); ok {
		resp.Mode = "select"
		for _, name := range names {
			resolved, err := t.catalog.ResolveInto(ctx, t.registry, name)
			if err != nil {
				resp.Errors = append(resp.Errors, err.Error())
				continue
			}
			resp.Resolved = append(resp.Resolved, DeferredToolMatch{
				Name:        resolved.Name(),
				Description: resolved.Description(),
				Resolved:    true,
				InputSchema: resolved.InputSchema(),
			})
		}
	} else {
		resp.Mode = "search"
		resp.Matches = t.catalog.Search(query, in.Limit)
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return Result{}, err
	}
	return Result{Output: string(data), Type: ResultText}, nil
}

func parseToolSelectQuery(query string) ([]string, bool) {
	const prefix = "select:"
	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
		return nil, false
	}
	raw := strings.TrimSpace(trimmed[len(prefix):])
	if raw == "" {
		return []string{}, true
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	names := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, true
}
