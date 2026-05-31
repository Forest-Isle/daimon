//go:build fts5

package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIngestPipeline_New(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	pipeline := kb.GetPipeline()
	if pipeline == nil {
		t.Fatal("expected non-nil pipeline")
	}
	if pipeline.strategy.ChunkSize != 512 {
		t.Errorf("expected default ChunkSize 512, got %d", pipeline.strategy.ChunkSize)
	}
}

func TestIngestPipeline_IngestMarkdown(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{ChunkSize: 100, ChunkOverlap: 20})
	ctx := context.Background()

	// Create a temp markdown file
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "test.md")
	mdContent := `# Test Document

This is a test document for the ingest pipeline.

## Section 1

This section has some content that should be split into multiple chunks.

## Section 2

More content here to ensure we get enough text for chunking.
` + repeatStr("More content for chunking. ", 50)
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	if err := kb.Ingest(ctx, mdPath, "markdown"); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Verify sources
	sources, err := kb.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].ChunkCount == 0 {
		t.Error("expected non-zero chunk count")
	}

	// Verify searchable
	results, err := kb.Search(ctx, KnowledgeQuery{Text: "Test Document", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results from ingested content")
	}
}

func TestIngestPipeline_IngestText(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{ChunkSize: 200, ChunkOverlap: 40})
	ctx := context.Background()

	dir := t.TempDir()
	txtPath := filepath.Join(dir, "test.txt")
	txtContent := repeatStr("This is plain text content for testing the ingest pipeline. ", 30)
	if err := os.WriteFile(txtPath, []byte(txtContent), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	if err := kb.Ingest(ctx, txtPath, "text"); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	sources, _ := kb.Sources(ctx)
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].ChunkCount == 0 {
		t.Error("expected non-zero chunk count")
	}
}

func TestIngestPipeline_IngestDir(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{ChunkSize: 100, ChunkOverlap: 20})
	pipeline := kb.GetPipeline()
	ctx := context.Background()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\n" + repeatStr("Content A. ", 30)), 0644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B\n\n" + repeatStr("Content B. ", 30)), 0644)

	if err := pipeline.IngestDir(ctx, dir); err != nil {
		t.Fatalf("IngestDir: %v", err)
	}

	sources, _ := kb.Sources(ctx)
	if len(sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(sources))
	}
}

func TestIngestPipeline_IngestDir_Empty(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	pipeline := kb.GetPipeline()
	ctx := context.Background()

	dir := t.TempDir()
	if err := pipeline.IngestDir(ctx, dir); err != nil {
		t.Fatalf("IngestDir on empty dir: %v", err)
	}
}

func TestIngestPipeline_EmptyContent(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{})
	ctx := context.Background()

	dir := t.TempDir()
	mdPath := filepath.Join(dir, "empty.md")
	os.WriteFile(mdPath, []byte("   \n\n  "), 0644)

	err := kb.Ingest(ctx, mdPath, "markdown")
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestIngestPipeline_CustomConfig(t *testing.T) {
	db := newTestStore(t)
	defer db.Close()

	kb := New(db, noopEmbedder(), Config{
		ChunkSize:    50,
		ChunkOverlap: 10,
	})
	pipeline := kb.GetPipeline()
	if pipeline.strategy.ChunkSize != 50 {
		t.Errorf("expected ChunkSize 50, got %d", pipeline.strategy.ChunkSize)
	}
}

func repeatStr(s string, n int) string {
	result := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		result = append(result, s...)
	}
	return string(result)
}
