package code_engine

import (
	"testing"
)

func TestNewSemanticCodeSearch(t *testing.T) {
	si := NewSymbolIndex()
	cg := NewCallGraph()
	scs := NewSemanticCodeSearch(si, cg)
	if scs == nil {
		t.Fatal("expected non-nil SemanticCodeSearch")
	}
	if scs.index != si {
		t.Error("index not set correctly")
	}
	if scs.callGraph != cg {
		t.Error("callGraph not set correctly")
	}
}

func TestParseIntent(t *testing.T) {
	scs := NewSemanticCodeSearch(NewSymbolIndex(), NewCallGraph())

	tests := []struct {
		query      string
		expectType SearchType
		expectSym  string
	}{
		{"find references to UserService", SearchFindReferences, "UserService"},
		{"where is Process called", SearchFindReferences, "Process"},
		{"definition of NewHandler", SearchFindDefinition, "NewHandler"},
		{"where is Config defined", SearchFindDefinition, "Config"},
		{"who calls Validate", SearchFindCallers, "Validate"},
		{"callers of SendEmail", SearchFindCallers, "SendEmail"},
		{"what does Execute call", SearchFindCallees, "Execute"},
		{"callees of Run", SearchFindCallees, "Run"},
		{"search for any function related to auth", SearchSemantic, ""},
		{"authentication middleware", SearchSemantic, ""},
	}

	for _, tt := range tests {
		t.Run(tt.query[:min(len(tt.query), 30)], func(t *testing.T) {
			intent := scs.ParseIntent(tt.query)
			if intent.Type != tt.expectType {
				t.Errorf("ParseIntent(%q).Type = %s, want %s", tt.query, intent.Type, tt.expectType)
			}
			if tt.expectSym != "" && intent.Symbol != tt.expectSym {
				t.Errorf("ParseIntent(%q).Symbol = %q, want %q", tt.query, intent.Symbol, tt.expectSym)
			}
		})
	}
}

func TestParseIntent_DefaultSemantic(t *testing.T) {
	scs := NewSemanticCodeSearch(NewSymbolIndex(), NewCallGraph())
	intent := scs.ParseIntent("how does authentication work")
	if intent.Type != SearchSemantic {
		t.Errorf("expected SearchSemantic, got %s", intent.Type)
	}
}

func TestFindDefinition(t *testing.T) {
	si := NewSymbolIndex()
	scs := NewSemanticCodeSearch(si, NewCallGraph())

	si.mu.Lock()
	si.byName["GetUser"] = []*Symbol{
		{Name: "GetUser", Kind: KindFunction, FilePath: "svc.go", LineStart: 42, Package: "service"},
		{Name: "getUserHelper", Kind: KindFunction, FilePath: "svc.go", LineStart: 50, Package: "service"},
	}
	si.mu.Unlock()

	results, err := scs.findDefinition("GetUser")
	if err != nil {
		t.Fatalf("findDefinition: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 exact match, got %d", len(results))
	}
	if results[0].MatchType != "exact" {
		t.Errorf("expected exact match, got %s", results[0].MatchType)
	}
	if results[0].Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", results[0].Score)
	}
}

func TestFindReferences(t *testing.T) {
	si := NewSymbolIndex()
	scs := NewSemanticCodeSearch(si, NewCallGraph())

	si.mu.Lock()
	si.byName["Logger"] = []*Symbol{
		{Name: "Logger", Kind: KindInterface, FilePath: "log.go"},
		{Name: "Logger", Kind: KindVariable, FilePath: "main.go"},
		{Name: "GetLogger", Kind: KindFunction, FilePath: "factory.go"},
	}
	si.mu.Unlock()

	results, err := scs.findReferences("Logger")
	if err != nil {
		t.Fatalf("findReferences: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(results))
	}
}

func TestFindCallers(t *testing.T) {
	si := NewSymbolIndex()
	cg := NewCallGraph()
	scs := NewSemanticCodeSearch(si, cg)

	// Set up call graph with a function
	cg.ensureNode("a.go:targetFunc:1", &Symbol{Name: "targetFunc"})
	cg.ensureNode("a.go:caller1:10", &Symbol{Name: "caller1"})
	cg.ensureNode("a.go:caller2:20", &Symbol{Name: "caller2"})

	cg.mu.Lock()
	cg.nodes["a.go:targetFunc:1"].Callers = []string{"a.go:caller1:10", "a.go:caller2:20"}
	cg.mu.Unlock()

	results, err := scs.findCallers("targetFunc")
	if err != nil {
		t.Fatalf("findCallers: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected caller results")
	}
}

func TestFindCallers_NoCallGraph(t *testing.T) {
	scs := NewSemanticCodeSearch(NewSymbolIndex(), nil)
	_, err := scs.findCallers("anything")
	if err == nil {
		t.Error("expected error when call graph is nil")
	}
}

func TestFindCallees(t *testing.T) {
	si := NewSymbolIndex()
	cg := NewCallGraph()
	scs := NewSemanticCodeSearch(si, cg)

	cg.ensureNode("a.go:mainFunc:1", &Symbol{Name: "mainFunc"})
	cg.ensureNode("a.go:helper1:10", &Symbol{Name: "helper1"})
	cg.ensureNode("a.go:helper2:20", &Symbol{Name: "helper2"})

	cg.mu.Lock()
	cg.nodes["a.go:mainFunc:1"].Callees = []string{"a.go:helper1:10", "a.go:helper2:20"}
	cg.mu.Unlock()

	results, err := scs.findCallees("mainFunc")
	if err != nil {
		t.Fatalf("findCallees: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 callee results, got %d", len(results))
	}
}

func TestFindCallees_NotFound(t *testing.T) {
	scs := NewSemanticCodeSearch(NewSymbolIndex(), NewCallGraph())
	_, err := scs.findCallees("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent function")
	}
}

func TestSemanticSearch(t *testing.T) {
	si := NewSymbolIndex()
	scs := NewSemanticCodeSearch(si, NewCallGraph())

	si.mu.Lock()
	si.byName["authenticate"] = []*Symbol{
		{Name: "authenticate", Kind: KindFunction, FilePath: "auth.go", Package: "auth"},
	}
	si.byName["authorize"] = []*Symbol{
		{Name: "authorize", Kind: KindFunction, FilePath: "auth.go", Package: "auth"},
	}
	si.mu.Unlock()

	results, err := scs.semanticSearch("authenticate user permissions")
	if err != nil {
		t.Fatalf("semanticSearch: %v", err)
	}

	// Should match "authenticate" - a word from the query
	if len(results) == 0 {
		t.Error("expected at least 1 semantic result")
	}
}

func TestSemanticSearch_NoShortWords(t *testing.T) {
	si := NewSymbolIndex()
	scs := NewSemanticCodeSearch(si, NewCallGraph())

	si.mu.Lock()
	si.byName["a"] = []*Symbol{{Name: "a", Kind: KindFunction}}
	si.byName["an"] = []*Symbol{{Name: "an", Kind: KindFunction}}
	si.byName["hi"] = []*Symbol{{Name: "hi", Kind: KindFunction}}
	si.mu.Unlock()

	results, _ := scs.semanticSearch("a an hi")
	if len(results) != 0 {
		t.Errorf("expected 0 results for short words, got %d", len(results))
	}
}

func TestSearch_AllTypes_NoErrors(t *testing.T) {
	si := NewSymbolIndex()
	scs := NewSemanticCodeSearch(si, NewCallGraph())

	tests := []struct {
		intent *SearchIntent
		name   string
	}{
		{&SearchIntent{Type: SearchFindDefinition, Symbol: "Nonexistent"}, "find_definition"},
		{&SearchIntent{Type: SearchFindReferences, Symbol: "Nonexistent"}, "find_references"},
		{&SearchIntent{Type: SearchSemantic, Text: "test query"}, "semantic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := scs.Search(tt.intent)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			// results may be nil for no matches; that's valid
			_ = results
		})
	}
}

func TestExtractSymbolFromQuery(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"find references to UserService", "UserService"},
		{"who calls ValidateInput", "ValidateInput"},
		{"find references to config.Load", "config.Load"},
		{"how does this work", "work"}, // last word fallback
		{"a", "a"},                     // single char - short
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.query[:min(len(tt.query), 20)], func(t *testing.T) {
			got := extractSymbolFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractSymbolFromQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestSortByScore(t *testing.T) {
	matches := []*CodeMatch{
		{Score: 0.3},
		{Score: 1.0},
		{Score: 0.7},
	}
	sortByScore(matches)
	if matches[0].Score != 1.0 || matches[1].Score != 0.7 || matches[2].Score != 0.3 {
		t.Errorf("scores not sorted descending: %v", matches[0].Score)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
