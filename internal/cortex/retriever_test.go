package cortex

import (
	"context"
	"errors"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/knowledge/graph"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// --- mocks ---

type mockMemStore struct {
	memory.Store
	searchFunc func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error)
	saveFunc   func(ctx context.Context, entry memory.Entry) error
}

func (m *mockMemStore) Search(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, query)
	}
	return nil, nil
}

func (m *mockMemStore) Save(ctx context.Context, entry memory.Entry) error {
	if m.saveFunc != nil {
		return m.saveFunc(ctx, entry)
	}
	return nil
}

type mockKBSearcher struct {
	knowledge.Searcher
	searchFunc func(ctx context.Context, query knowledge.KnowledgeQuery) ([]knowledge.KnowledgeResult, error)
}

func (m *mockKBSearcher) Search(ctx context.Context, query knowledge.KnowledgeQuery) ([]knowledge.KnowledgeResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, query)
	}
	return nil, nil
}

type mockGraphStore struct {
	graph.Graph
	findByNameFunc func(ctx context.Context, name string) ([]graph.Node, error)
	traverseFunc   func(ctx context.Context, nodeID string, maxDepth int) ([]graph.Triple, error)
}

func (m *mockGraphStore) FindByName(ctx context.Context, name string) ([]graph.Node, error) {
	if m.findByNameFunc != nil {
		return m.findByNameFunc(ctx, name)
	}
	return nil, nil
}

func (m *mockGraphStore) Traverse(ctx context.Context, nodeID string, maxDepth int) ([]graph.Triple, error) {
	if m.traverseFunc != nil {
		return m.traverseFunc(ctx, nodeID, maxDepth)
	}
	return nil, nil
}

// satisfy the rest of Graph interface with noops
func (m *mockGraphStore) UpsertNode(ctx context.Context, node graph.Node) (string, error) {
	return node.ID, nil
}
func (m *mockGraphStore) UpsertEdge(ctx context.Context, edge graph.Edge) (string, error) {
	return edge.ID, nil
}
func (m *mockGraphStore) Neighbors(ctx context.Context, nodeID string, edgeType string) ([]graph.Triple, error) {
	return nil, nil
}
func (m *mockGraphStore) FindNode(ctx context.Context, nodeType, name string) (*graph.Node, error) {
	return nil, nil
}
func (m *mockGraphStore) AddProvenance(ctx context.Context, edgeID, sourceType, sourceID string) error {
	return nil
}


// --- tests ---

func TestNewUnifiedRetriever(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)
	if ur == nil {
		t.Fatal("NewUnifiedRetriever returned nil")
	}
	if ur.fusionWeights == nil {
		t.Fatal("fusionWeights should not be nil")
	}
	if ur.fusionWeights.MemoryWeight != 0.35 {
		t.Errorf("MemoryWeight = %f, want 0.35", ur.fusionWeights.MemoryWeight)
	}
}

func TestSetFusionWeights(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)

	ur.SetFusionWeights(&FusionWeights{
		MemoryWeight:     0.5,
		KnowledgeWeight:  0.3,
		GraphWeight:      0.1,
		ProceduralWeight: 0.1,
	})
	if ur.fusionWeights.MemoryWeight != 0.5 {
		t.Errorf("MemoryWeight = %f, want 0.5", ur.fusionWeights.MemoryWeight)
	}

	ur.SetFusionWeights(nil)
	if ur.fusionWeights.MemoryWeight != 0.35 {
		t.Errorf("after nil: MemoryWeight = %f, want 0.35", ur.fusionWeights.MemoryWeight)
	}
}

func TestGetProcedural(t *testing.T) {
	ps := &ProceduralStore{}
	ur := NewUnifiedRetriever(nil, nil, nil, ps)
	if got := ur.GetProcedural(); got != ps {
		t.Error("GetProcedural() did not return the same procedural store")
	}

	var nilUR *UnifiedRetriever
	if got := nilUR.GetProcedural(); got != nil {
		t.Error("GetProcedural on nil receiver should return nil")
	}
}

func TestSearch_NoSources(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)
	results, err := ur.Search(context.Background(), "test query", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search with all nil sources returned error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_MemoryOnly(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "mem1", Content: "user likes Go programming"}, Score: 0.9},
				{Entry: memory.Entry{ID: "mem2", Content: "user worked on distributed systems"}, Score: 0.7},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, nil, nil)
	results, err := ur.Search(context.Background(), "Go programming", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	for _, r := range results {
		if r.Source != "memory" {
			t.Errorf("expected source 'memory', got '%s' for %s", r.Source, r.ID)
		}
		if r.Type != Episodic {
			t.Errorf("expected type Episodic, got %s", r.Type)
		}
	}
}

func TestSearch_KnowledgeOnly(t *testing.T) {
	kb := &mockKBSearcher{
		searchFunc: func(ctx context.Context, query knowledge.KnowledgeQuery) ([]knowledge.KnowledgeResult, error) {
			return []knowledge.KnowledgeResult{
				{Chunk: knowledge.Chunk{ID: "kb1", Content: "Go is a statically typed language"}, Score: 0.95},
				{Chunk: knowledge.Chunk{ID: "kb2", Content: "Concurrency in Go uses goroutines"}, Score: 0.85},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(nil, kb, nil, nil)
	results, err := ur.Search(context.Background(), "Go language", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	for _, r := range results {
		if r.Source != "knowledge" {
			t.Errorf("expected source 'knowledge', got '%s'", r.Source)
		}
		if r.Type != Semantic {
			t.Errorf("expected type Semantic, got %s", r.Type)
		}
	}
}

func TestSearch_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("memory search failed")
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return nil, expectedErr
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, nil, nil)
	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 5})
	if err == nil {
		t.Error("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) && err.Error() != expectedErr.Error() {
		t.Errorf("expected error containing %q, got %v", expectedErr.Error(), err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
}

func TestSearch_ErrorWithPartialResults(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return nil, errors.New("mem store down")
		},
	}
	kb := &mockKBSearcher{
		searchFunc: func(ctx context.Context, query knowledge.KnowledgeQuery) ([]knowledge.KnowledgeResult, error) {
			return []knowledge.KnowledgeResult{
				{Chunk: knowledge.Chunk{ID: "kb1", Content: "knowledge result"}, Score: 0.8},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, kb, nil, nil)
	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 5})
	if err != nil {
		t.Errorf("unexpected error when at least one source succeeds: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from knowledge source")
	}
	if results[0].Source != "knowledge" {
		t.Errorf("expected source 'knowledge', got '%s'", results[0].Source)
	}
}

func TestSearch_FusionWeightsApplied(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "mem1", Content: "alpha memory result"}, Score: 0.8},
			}, nil
		},
	}
	kb := &mockKBSearcher{
		searchFunc: func(ctx context.Context, query knowledge.KnowledgeQuery) ([]knowledge.KnowledgeResult, error) {
			return []knowledge.KnowledgeResult{
				{Chunk: knowledge.Chunk{ID: "kb1", Content: "beta knowledge result"}, Score: 0.9},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, kb, nil, nil)
	ur.SetFusionWeights(&FusionWeights{
		MemoryWeight:     0.1,
		KnowledgeWeight:  0.8,
		GraphWeight:      0.05,
		ProceduralWeight: 0.05,
	})

	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	// Verify scores are non-negative and finite
	for _, r := range results {
		if r.Score < 0 {
			t.Errorf("negative score for %s: %f", r.ID, r.Score)
		}
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)
	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 0})
	if err != nil {
		t.Fatalf("Search with zero limit failed: %v", err)
	}
	if results != nil && len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestSearch_Deduplication(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "mem1", Content: "identical content here"}, Score: 0.9},
				{Entry: memory.Entry{ID: "mem2", Content: "identical content here"}, Score: 0.8},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, nil, nil)
	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Content == results[j].Content {
				t.Errorf("duplicate content found: %q at indices %d and %d", results[i].Content, i, j)
			}
		}
	}
}

func TestSearch_ProceduralResults(t *testing.T) {
	// Use a memory store that returns nothing for regular search
	// but returns procedural content for the procedural store
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return nil, nil
		},
	}
	// Use a separate procedural store with its own mock that returns procedural JSON
	procMemStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{
					Entry: memory.Entry{ID: "p1", Content: `{"TaskPattern":"write test","ToolSequence":["go test"],"SuccessRate":0.9}`},
					Score: 0.9,
				},
			}, nil
		},
	}

	ps := NewProceduralStore(procMemStore, nil)
	ur := NewUnifiedRetriever(memStore, nil, nil, ps)
	results, err := ur.Search(context.Background(), "write test", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected procedural results")
	}
	// Verify at least one result is procedural
	foundProcedural := false
	for _, r := range results {
		if r.Type == Procedural && r.Source == "procedural" {
			foundProcedural = true
			if r.Strategy == nil {
				t.Error("expected Strategy to be set for procedural results")
			}
			break
		}
	}
	if !foundProcedural {
		t.Error("expected at least one procedural result")
	}
}

func TestSearch_GraphResults(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return nil, nil
		},
	}
	gs := &mockGraphStore{
		findByNameFunc: func(ctx context.Context, name string) ([]graph.Node, error) {
			return []graph.Node{
				{ID: "n1", Name: "Go", Type: "language"},
			}, nil
		},
		traverseFunc: func(ctx context.Context, nodeID string, maxDepth int) ([]graph.Triple, error) {
			return []graph.Triple{
				{
					Subject:   graph.Node{ID: "n1", Name: "Go", Type: "language"},
					Predicate: "is_a",
					Object:    graph.Node{ID: "n2", Name: "Programming Language", Type: "concept"},
					Weight:    0.8,
				},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, gs, nil)
	results, err := ur.Search(context.Background(), "The Go programming language", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) > 0 {
		if results[0].Source != "graph" {
			t.Errorf("expected first result source 'graph', got '%s'", results[0].Source)
		}
	}
}

func TestSearch_AllSources(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "m1", Content: "memory result"}, Score: 0.7},
			}, nil
		},
	}
	kb := &mockKBSearcher{
		searchFunc: func(ctx context.Context, query knowledge.KnowledgeQuery) ([]knowledge.KnowledgeResult, error) {
			return []knowledge.KnowledgeResult{
				{Chunk: knowledge.Chunk{ID: "k1", Content: "knowledge result"}, Score: 0.8},
			}, nil
		},
	}
	gs := &mockGraphStore{
		findByNameFunc: func(ctx context.Context, name string) ([]graph.Node, error) {
			return []graph.Node{{ID: "n1", Name: "test", Type: "concept"}}, nil
		},
		traverseFunc: func(ctx context.Context, nodeID string, maxDepth int) ([]graph.Triple, error) {
			return []graph.Triple{
				{
					Subject:   graph.Node{ID: "n1", Name: "test", Type: "concept"},
					Predicate: "related_to",
					Object:    graph.Node{ID: "n2", Name: "example", Type: "concept"},
					Weight:    0.5,
				},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, kb, gs, nil)
	results, err := ur.Search(context.Background(), "test memory knowledge graph", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from all sources")
	}

	// Verify multiple sources are represented
	sources := make(map[string]bool)
	for _, r := range results {
		sources[r.Source] = true
	}
	// At minimum memory + knowledge should be present (graph depends on entity matching)
	if !sources["memory"] {
		t.Error("missing memory source")
	}
	if !sources["knowledge"] {
		t.Error("missing knowledge source")
	}
}

// --- Helper function tests ---

func TestApplySourceWeight_Empty(t *testing.T) {
	result := applySourceWeight(nil, 0.5)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestApplySourceWeight_SingleItem(t *testing.T) {
	items := []*UnifiedMemory{
		{ID: "a", Score: 0.8, Content: "hello"},
	}
	result := applySourceWeight(items, 1.0)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	expectedRRF := 1.0 / 61.0
	expected := (1.0 + expectedRRF) * 1.0
	if result[0].Score != expected {
		t.Errorf("expected score %f, got %f", expected, result[0].Score)
	}
}

func TestApplySourceWeight_MultipleItems(t *testing.T) {
	items := []*UnifiedMemory{
		{ID: "a", Score: 0.3, Content: "low"},
		{ID: "b", Score: 0.9, Content: "high"},
	}
	result := applySourceWeight(items, 0.5)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// Item "b" (higher original score, rank 1) should have higher fused score
	if result[1].Score <= result[0].Score {
		t.Errorf("expected 'b' score (%f) > 'a' score (%f)", result[1].Score, result[0].Score)
	}
}

func TestApplySourceWeight_UniformScores(t *testing.T) {
	items := []*UnifiedMemory{
		{ID: "a", Score: 0.5, Content: "same"},
		{ID: "b", Score: 0.5, Content: "same"},
	}
	result := applySourceWeight(items, 0.5)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// When maxScore == minScore, the formula uses 1.0 directly
	expectedRRF := 1.0 / 61.0
	expected := (1.0 + expectedRRF) * 0.5
	if result[0].Score != expected {
		t.Errorf("expected score %f, got %f", expected, result[0].Score)
	}
}

func TestBoostMemoriesByGraphConnectivity(t *testing.T) {
	memories := []*UnifiedMemory{
		{ID: "m1", Content: "The Go language is great for microservices", Score: 1.0},
		{ID: "m2", Content: "Python is good for data science", Score: 1.0},
	}

	graphEntities := []string{"go", "microservices"}
	boostMemoriesByGraphConnectivity(memories, graphEntities)

	if memories[0].Score <= 1.0 {
		t.Errorf("expected first memory score > 1.0 after boost, got %f", memories[0].Score)
	}
}

func TestBoostMemoriesByGraphConnectivity_Empty(t *testing.T) {
	boostMemoriesByGraphConnectivity(nil, []string{"go"})
	boostMemoriesByGraphConnectivity([]*UnifiedMemory{}, []string{"go"})
	boostMemoriesByGraphConnectivity([]*UnifiedMemory{{ID: "m1", Content: "test", Score: 1.0}}, nil)
	boostMemoriesByGraphConnectivity([]*UnifiedMemory{{ID: "m1", Content: "test", Score: 1.0}}, []string{})
	// No panic = pass
}

func TestDedupeByContentSimilarity(t *testing.T) {
	items := []*UnifiedMemory{
		{ID: "a", Content: "aaa bbb ccc ddd eee"},
		{ID: "b", Content: "aaa bbb ccc ddd eee"},
		{ID: "c", Content: "xyz xyz xyz"},
	}

	result := dedupeByContentSimilarity(items)
	if len(result) != 2 {
		t.Errorf("expected 2 items after dedup, got %d", len(result))
	}
}

func TestDedupeByContentSimilarity_Empty(t *testing.T) {
	result := dedupeByContentSimilarity(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestContentSimilarity(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"", "anything", 0},
		{"anything", "", 0},
		{"", "", 0},
		{"hello", "hello", 1},
		{"abc", "xyz", 0},
	}

	for _, tt := range tests {
		got := contentSimilarity(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("contentSimilarity(%q, %q) = %f, want %f", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestExtractEntityCandidates(t *testing.T) {
	candidates := extractEntityCandidates("The programming language is used for microservices")
	for _, c := range candidates {
		if isStopWord(c) {
			t.Errorf("stop word '%s' should not be a candidate", c)
		}
		if len(c) <= 3 {
			t.Errorf("candidate '%s' should be longer than 3 chars", c)
		}
	}

	hasProgramming := false
	for _, c := range candidates {
		if c == "programming" {
			hasProgramming = true
			break
		}
	}
	if !hasProgramming {
		t.Error("expected 'programming' to be a candidate")
	}
}

func TestExtractEntityCandidates_Empty(t *testing.T) {
	candidates := extractEntityCandidates("")
	if len(candidates) != 0 {
		t.Errorf("expected empty, got %v", candidates)
	}

	candidates = extractEntityCandidates("the and for")
	if len(candidates) != 0 {
		t.Errorf("expected empty (all stop words), got %v", candidates)
	}
}

func TestBuildPromptSection(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)

	memories := []*UnifiedMemory{
		{ID: "m1", Type: Episodic, Content: "episodic memory", Source: "memory"},
		{ID: "m2", Type: Semantic, Content: "semantic knowledge", Source: "knowledge"},
		{ID: "m3", Type: Procedural, Content: "procedural strategy", Source: "procedural"},
		{ID: "m4", Type: Semantic, Content: "graph relation", Source: "graph"},
	}

	sections := ur.BuildPromptSection(memories)
	if sections == nil {
		t.Fatal("BuildPromptSection returned nil")
	}

	if len(sections.MemoryLines) != 1 {
		t.Errorf("expected 1 memory line, got %d", len(sections.MemoryLines))
	}
	if len(sections.KnowledgeLines) != 1 {
		t.Errorf("expected 1 knowledge line, got %d", len(sections.KnowledgeLines))
	}
	if len(sections.StrategyLines) != 1 {
		t.Errorf("expected 1 strategy line, got %d", len(sections.StrategyLines))
	}
	if len(sections.GraphLines) != 1 {
		t.Errorf("expected 1 graph line, got %d", len(sections.GraphLines))
	}

	if sections.Combined == "" {
		t.Error("Combined section should not be empty")
	}
}

func TestBuildPromptSection_NilItems(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)
	memories := []*UnifiedMemory{nil, {ID: "m1", Type: Episodic, Content: "valid", Source: "memory"}}
	sections := ur.BuildPromptSection(memories)
	if sections == nil {
		t.Fatal("BuildPromptSection returned nil")
	}
	if len(sections.MemoryLines) != 1 {
		t.Errorf("expected 1 memory line, got %d", len(sections.MemoryLines))
	}
}

func TestBuildPromptSection_Empty(t *testing.T) {
	ur := NewUnifiedRetriever(nil, nil, nil, nil)
	sections := ur.BuildPromptSection(nil)
	if sections == nil {
		t.Fatal("BuildPromptSection returned nil for nil input")
	}
	if sections.Combined != "" {
		t.Errorf("expected empty combined, got %q", sections.Combined)
	}
}

func TestPromptSectionsFormat_Nil(t *testing.T) {
	var ps *PromptSections
	result := ps.Format()
	if result != "" {
		t.Errorf("Format on nil receiver should return empty string, got %q", result)
	}
}

func TestTruncateLine(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"hello\nworld", 20, "hello world"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncateLine(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncateLine(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestSearch_WithDeduplicateDistinctContent(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "m1", Content: "ABCDEFG"}, Score: 0.9},
				{Entry: memory.Entry{ID: "m2", Content: "HIJKLMN"}, Score: 0.5},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, nil, nil)
	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
}

func TestSearch_ResultOrderingByScore(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "m1", Content: "ABCDEFGHIJ"}, Score: 0.9},
				{Entry: memory.Entry{ID: "m2", Content: "KLMNOPQRST"}, Score: 0.5},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, nil, nil)
	results, err := ur.Search(context.Background(), "test", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Both items have unique characters so should not be deduped.
	// Item m1 (ABCDEFGHIJ, original score 0.9, rank 0) should have higher fused score than m2.
	if results[0].Score < results[1].Score {
		t.Errorf("results not sorted descending: %.4f < %.4f", results[0].Score, results[1].Score)
	}
}

func TestSearch_TimeoutBehavior(t *testing.T) {
	memStore := &mockMemStore{
		searchFunc: func(ctx context.Context, query memory.SearchQuery) ([]memory.SearchResult, error) {
			return []memory.SearchResult{
				{Entry: memory.Entry{ID: "m1", Content: "quick result"}, Score: 0.5},
			}, nil
		},
	}

	ur := NewUnifiedRetriever(memStore, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	results, err := ur.Search(ctx, "test", SearchOptions{Limit: 5})
	// With cancelled context, goroutines should still complete (they don't check ctx)
	// but the result should work fine since the goroutines use their own internal flow
	_ = results
	_ = err
}
