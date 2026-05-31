package knowledge

import (
	"strings"
	"testing"
)

func TestChunkText_EmptyString(t *testing.T) {
	result := ChunkText("", DefaultChunkStrategy())
	if result != nil {
		t.Errorf("expected nil for empty string, got %v", result)
	}
}

func TestChunkText_WhitespaceOnly(t *testing.T) {
	result := ChunkText("   \n\n  ", DefaultChunkStrategy())
	if result != nil {
		t.Errorf("expected nil for whitespace-only string, got %v", result)
	}
}

func TestChunkText_ShorterThanChunkSize(t *testing.T) {
	text := "Hello, world!"
	strategy := ChunkStrategy{ChunkSize: 100, ChunkOverlap: 10}
	result := ChunkText(text, strategy)
	if len(result) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(result))
	}
	if result[0] != text {
		t.Errorf("expected %q, got %q", text, result[0])
	}
}

func TestChunkText_ExactChunkSize(t *testing.T) {
	text := strings.Repeat("a", 512)
	strategy := ChunkStrategy{ChunkSize: 512, ChunkOverlap: 64}
	result := ChunkText(text, strategy)
	if len(result) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(result))
	}
}

func TestChunkText_LongerThanChunkSize(t *testing.T) {
	text := strings.Repeat("hello world ", 100) // ~1200 chars
	strategy := ChunkStrategy{ChunkSize: 200, ChunkOverlap: 40}
	result := ChunkText(text, strategy)
	if len(result) < 2 {
		t.Errorf("expected multiple chunks for long text, got %d", len(result))
	}
	// Verify no empty chunks
	for i, c := range result {
		if strings.TrimSpace(c) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}
	// Verify all chunks are within size (with tolerance for sentence boundary)
	for i, c := range result {
		if len([]rune(c)) > int(float64(strategy.ChunkSize)*1.2) {
			t.Errorf("chunk %d exceeds max size: %d runes (limit %d)", i, len([]rune(c)), strategy.ChunkSize)
		}
	}
}

func TestChunkText_ZeroDefaults(t *testing.T) {
	text := strings.Repeat("hello ", 200)
	result := ChunkText(text, ChunkStrategy{})
	if len(result) < 2 {
		t.Errorf("expected multiple chunks with default strategy, got %d", len(result))
	}
}

func TestChunkText_NegativeOverlap(t *testing.T) {
	text := strings.Repeat("hello ", 200)
	result := ChunkText(text, ChunkStrategy{ChunkSize: 200, ChunkOverlap: -10})
	if len(result) < 2 {
		t.Errorf("expected multiple chunks with clamped overlap, got %d", len(result))
	}
}

func TestChunkText_OverlapLargerThanSize(t *testing.T) {
	text := strings.Repeat("hello ", 200)
	result := ChunkText(text, ChunkStrategy{ChunkSize: 200, ChunkOverlap: 300})
	// Overlap clamped to ChunkSize/4 = 50
	if len(result) < 2 {
		t.Errorf("expected multiple chunks with clamped overlap, got %d", len(result))
	}
}

func TestChunkText_NonOverlapping(t *testing.T) {
	text := strings.Repeat("word ", 100)
	strategy := ChunkStrategy{ChunkSize: 100, ChunkOverlap: 0}
	result := ChunkText(text, strategy)
	if len(result) < 2 {
		t.Errorf("expected multiple chunks for non-overlapping, got %d", len(result))
	}
}

func TestChunkText_SentenceBoundarySplit(t *testing.T) {
	// Create text where sentence boundary is near chunk edge
	sentences := make([]string, 10)
	for i := 0; i < 10; i++ {
		sentences[i] = strings.Repeat("word ", 30) + "."
	}
	text := strings.Join(sentences, " ")
	strategy := ChunkStrategy{ChunkSize: 200, ChunkOverlap: 40}
	result := ChunkText(text, strategy)
	if len(result) < 2 {
		t.Errorf("expected multiple chunks with sentence boundaries, got %d", len(result))
	}
}

func TestChunkText_NewlineBoundarySplit(t *testing.T) {
	lines := make([]string, 20)
	for i := 0; i < 20; i++ {
		lines[i] = strings.Repeat("line ", 30)
	}
	text := strings.Join(lines, "\n")
	strategy := ChunkStrategy{ChunkSize: 200, ChunkOverlap: 40}
	result := ChunkText(text, strategy)
	if len(result) < 2 {
		t.Errorf("expected multiple chunks with newline boundaries, got %d", len(result))
	}
}

func TestDefaultChunkStrategy(t *testing.T) {
	s := DefaultChunkStrategy()
	if s.ChunkSize != 512 {
		t.Errorf("expected ChunkSize 512, got %d", s.ChunkSize)
	}
	if s.ChunkOverlap != 64 {
		t.Errorf("expected ChunkOverlap 64, got %d", s.ChunkOverlap)
	}
}
