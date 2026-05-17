package code_engine

import (
	"fmt"
	"sort"
	"strings"
)

// SearchIntent classifies what the user is looking for.
type SearchIntent struct {
	Type   SearchType `json:"type"`
	Symbol string     `json:"symbol,omitempty"`
	Text   string     `json:"text,omitempty"`
	Kind   SymbolKind `json:"kind,omitempty"`
}

// SearchType categorizes the search operation.
type SearchType string

const (
	SearchFindDefinition SearchType = "find_definition"
	SearchFindReferences SearchType = "find_references"
	SearchFindCallers    SearchType = "find_callers"
	SearchFindCallees    SearchType = "find_callees"
	SearchSemantic       SearchType = "semantic"
)

// CodeMatch represents a search result.
type CodeMatch struct {
	Symbol    *Symbol `json:"symbol"`
	Score     float64 `json:"score"`
	MatchType string  `json:"match_type"` // "exact", "partial", "semantic"
	Context   string  `json:"context,omitempty"`
}

// SemanticCodeSearch provides multi-strategy code search.
type SemanticCodeSearch struct {
	index     *SymbolIndex
	callGraph *CallGraph
}

// NewSemanticCodeSearch creates a new semantic searcher.
func NewSemanticCodeSearch(index *SymbolIndex, callGraph *CallGraph) *SemanticCodeSearch {
	return &SemanticCodeSearch{index: index, callGraph: callGraph}
}

// ParseIntent extracts search intent from a natural language query.
func (scs *SemanticCodeSearch) ParseIntent(query string) *SearchIntent {
	lower := strings.ToLower(query)

	// Pattern: "find references to X" / "where is X called"
	if strings.Contains(lower, "references to") || strings.Contains(lower, "where is") && strings.Contains(lower, "called") {
		sym := extractSymbolFromQuery(query)
		return &SearchIntent{Type: SearchFindReferences, Symbol: sym}
	}

	// Pattern: "find definition of X" / "where is X defined"
	if strings.Contains(lower, "definition of") || strings.Contains(lower, "where is") && strings.Contains(lower, "defined") {
		sym := extractSymbolFromQuery(query)
		return &SearchIntent{Type: SearchFindDefinition, Symbol: sym}
	}

	// Pattern: "who calls X" / "callers of X"
	if strings.Contains(lower, "who calls") || strings.Contains(lower, "callers of") {
		sym := extractSymbolFromQuery(query)
		return &SearchIntent{Type: SearchFindCallers, Symbol: sym}
	}

	// Pattern: "what does X call" / "callees of X"
	if strings.Contains(lower, "what does") && strings.Contains(lower, "call") || strings.Contains(lower, "callees of") {
		sym := extractSymbolFromQuery(query)
		return &SearchIntent{Type: SearchFindCallees, Symbol: sym}
	}

	// Default: semantic search
	return &SearchIntent{Type: SearchSemantic, Text: query}
}

// Search executes a search based on the parsed intent.
func (scs *SemanticCodeSearch) Search(intent *SearchIntent) ([]*CodeMatch, error) {
	switch intent.Type {
	case SearchFindDefinition:
		return scs.findDefinition(intent.Symbol)
	case SearchFindReferences:
		return scs.findReferences(intent.Symbol)
	case SearchFindCallers:
		return scs.findCallers(intent.Symbol)
	case SearchFindCallees:
		return scs.findCallees(intent.Symbol)
	default:
		return scs.semanticSearch(intent.Text)
	}
}

func (scs *SemanticCodeSearch) findDefinition(name string) ([]*CodeMatch, error) {
	symbols := scs.index.SearchByName(name)
	var matches []*CodeMatch
	for _, sym := range symbols {
		if strings.EqualFold(sym.Name, name) {
			matches = append(matches, &CodeMatch{
				Symbol:    sym,
				Score:     1.0,
				MatchType: "exact",
				Context:   fmt.Sprintf("%s %s at %s:%d", sym.Kind, sym.Name, sym.FilePath, sym.LineStart),
			})
		}
	}
	sortByScore(matches)
	return matches, nil
}

func (scs *SemanticCodeSearch) findReferences(name string) ([]*CodeMatch, error) {
	// Find definitions first
	defs, _ := scs.findDefinition(name)

	// Then find all symbols with matching name (potential references)
	refs := scs.index.SearchByName(name)
	var matches []*CodeMatch
	// Combine, prioritizing definitions
	for _, d := range defs {
		matches = append(matches, d)
	}
	for _, sym := range refs {
		matches = append(matches, &CodeMatch{
			Symbol:    sym,
			Score:     0.7,
			MatchType: "partial",
			Context:   fmt.Sprintf("Reference at %s:%d", sym.FilePath, sym.LineStart),
		})
	}
	return matches, nil
}

func (scs *SemanticCodeSearch) findCallers(name string) ([]*CodeMatch, error) {
	if scs.callGraph == nil {
		return nil, fmt.Errorf("call graph not available")
	}
	report := scs.callGraph.ImpactAnalysis(name, 3)
	var matches []*CodeMatch
	for _, fn := range report.AffectedFunctions {
		matches = append(matches, &CodeMatch{
			Symbol:    &Symbol{Name: fn},
			Score:     0.8,
			MatchType: "caller",
			Context:   fmt.Sprintf("Calls or is affected by %s", name),
		})
	}
	return matches, nil
}

func (scs *SemanticCodeSearch) findCallees(name string) ([]*CodeMatch, error) {
	if scs.callGraph == nil {
		return nil, fmt.Errorf("call graph not available")
	}
	// Find the node and list its callees
	fnID := ""
	for id, node := range scs.callGraph.nodes {
		if node.Symbol != nil && strings.EqualFold(node.Symbol.Name, name) {
			fnID = id
			break
		}
	}
	if fnID == "" {
		return nil, fmt.Errorf("function %s not found in call graph", name)
	}

	node := scs.callGraph.nodes[fnID]
	var matches []*CodeMatch
	for _, calleeID := range node.Callees {
		if calleeNode, ok := scs.callGraph.nodes[calleeID]; ok && calleeNode.Symbol != nil {
			matches = append(matches, &CodeMatch{
				Symbol:    calleeNode.Symbol,
				Score:     0.7,
				MatchType: "callee",
				Context:   fmt.Sprintf("Called by %s", name),
			})
		}
	}
	return matches, nil
}

func (scs *SemanticCodeSearch) semanticSearch(query string) ([]*CodeMatch, error) {
	// Token-based search across all symbols
	words := strings.Fields(strings.ToLower(query))
	var matches []*CodeMatch

	for _, word := range words {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if len(word) < 3 {
			continue
		}
		symbols := scs.index.SearchByName(word)
		for _, sym := range symbols {
			matches = append(matches, &CodeMatch{
				Symbol:    sym,
				Score:     0.5,
				MatchType: "semantic",
				Context:   fmt.Sprintf("%s %s in %s", sym.Kind, sym.Name, sym.Package),
			})
		}
	}

	sortByScore(matches)
	return matches, nil
}

// extractSymbolFromQuery tries to extract a symbol name from a natural language query.
func extractSymbolFromQuery(query string) string {
	words := strings.Fields(query)
	// Look for capitalized words that look like symbols
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?()[]{}\"'")
		if len(w) > 1 && (w[0] >= 'A' && w[0] <= 'Z' || strings.Contains(w, ".")) {
			return w
		}
	}
	// Fallback: return the last word
	if len(words) > 0 {
		last := strings.Trim(words[len(words)-1], ".,;:!?()[]{}\"'")
		if len(last) > 1 {
			return last
		}
	}
	return query
}

func sortByScore(matches []*CodeMatch) {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})
}
