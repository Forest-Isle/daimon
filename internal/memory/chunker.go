package memory

import (
	"strings"
	"unicode/utf8"
)

const (
	// ChunkThreshold is the number of facts that triggers chunking.
	ChunkThreshold = 200
	// ChunkSizeBytes is the target size in bytes for a chunk file.
	ChunkSizeBytes = 100 * 1024 // 100KB
)

// Chunker handles file chunking logic for large fact files.
type Chunker struct {
	chunkSize    int // target size in runes
	chunkOverlap int // overlap in runes
}

// NewChunker creates a new Chunker with default settings.
func NewChunker() *Chunker {
	return &Chunker{
		chunkSize:    512,  // ~256 Chinese characters
		chunkOverlap: 64,   // ~32 Chinese characters
	}
}

// ShouldChunk determines if a facts file should be chunked.
func (c *Chunker) ShouldChunk(factCount int, sizeBytes int) bool {
	return factCount > ChunkThreshold || sizeBytes > ChunkSizeBytes
}

// SplitFacts splits a list of facts into chunks based on semantic boundaries.
func (c *Chunker) SplitFacts(facts []MarkdownFact, targetFactsPerChunk int) [][]MarkdownFact {
	if len(facts) <= targetFactsPerChunk {
		return [][]MarkdownFact{facts}
	}

	var chunks [][]MarkdownFact
	for i := 0; i < len(facts); i += targetFactsPerChunk {
		end := i + targetFactsPerChunk
		if end > len(facts) {
			end = len(facts)
		}
		chunks = append(chunks, facts[i:end])
	}

	return chunks
}

// ChunkText splits text into overlapping chunks (reuses knowledge package logic).
func (c *Chunker) ChunkText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	total := len(runes)
	if total <= c.chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < total {
		end := start + c.chunkSize
		if end >= total {
			chunks = append(chunks, string(runes[start:]))
			break
		}

		// Try to split at a sentence boundary within the last 20% of the chunk
		splitAt := end
		searchStart := start + int(float64(c.chunkSize)*0.8)
		for i := end; i >= searchStart; i-- {
			ch := runes[i]
			if ch == '.' || ch == '?' || ch == '!' || ch == '\n' {
				splitAt = i + 1
				break
			}
		}

		chunks = append(chunks, strings.TrimSpace(string(runes[start:splitAt])))
		start = splitAt - c.chunkOverlap
		if start < 0 {
			start = 0
		}
	}

	// Filter empty chunks
	var result []string
	for _, chunk := range chunks {
		if utf8.RuneCountInString(strings.TrimSpace(chunk)) > 0 {
			result = append(result, strings.TrimSpace(chunk))
		}
	}

	return result
}

// MergeChunks merges small chunks back together if they're below threshold.
func (c *Chunker) MergeChunks(chunks [][]MarkdownFact, minFactsPerChunk int) [][]MarkdownFact {
	if len(chunks) <= 1 {
		return chunks
	}

	var merged [][]MarkdownFact
	var current []MarkdownFact

	for _, chunk := range chunks {
		if len(current)+len(chunk) <= ChunkThreshold {
			current = append(current, chunk...)
		} else {
			if len(current) > 0 {
				merged = append(merged, current)
			}
			current = chunk
		}
	}

	if len(current) > 0 {
		merged = append(merged, current)
	}

	return merged
}
