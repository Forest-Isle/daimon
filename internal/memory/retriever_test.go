package memory

import "testing"

func TestContentSimilarityUsesWordOverlap(t *testing.T) {
	semantic := "The project test command requires -tags fts5."
	procedural := "Strategy: verified Go runtime changes using tools: go test -tags fts5 ./..."

	if got := contentSimilarity(semantic, procedural); got >= 0.8 {
		t.Fatalf("contentSimilarity() = %v, want below dedupe threshold", got)
	}

	if got := contentSimilarity("run go test with fts5", "run go test with fts5"); got != 1 {
		t.Fatalf("identical content similarity = %v, want 1", got)
	}
}
