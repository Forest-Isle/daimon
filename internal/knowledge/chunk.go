package knowledge

import (
	"strings"
	"unicode/utf8"
)

// ChunkStrategy controls how text is split into chunks.
type ChunkStrategy struct {
	ChunkSize    int // target size in runes
	ChunkOverlap int // overlap in runes between adjacent chunks
}

// DefaultChunkStrategy returns sensible defaults.
func DefaultChunkStrategy() ChunkStrategy {
	return ChunkStrategy{ChunkSize: 512, ChunkOverlap: 64}
}

// ChunkText splits text into overlapping chunks based on the strategy.
// It tries to split on sentence boundaries (., ?, !) first.
func ChunkText(text string, strategy ChunkStrategy) []string {
	if strategy.ChunkSize <= 0 {
		strategy.ChunkSize = 512
	}
	if strategy.ChunkOverlap < 0 {
		strategy.ChunkOverlap = 0
	}
	if strategy.ChunkOverlap >= strategy.ChunkSize {
		strategy.ChunkOverlap = strategy.ChunkSize / 4
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	total := len(runes)
	if total <= strategy.ChunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < total {
		end := start + strategy.ChunkSize
		if end >= total {
			chunks = append(chunks, string(runes[start:]))
			break
		}
		// Try to split at a sentence boundary within the last 20% of the chunk
		splitAt := end
		searchStart := start + int(float64(strategy.ChunkSize)*0.8)
		for i := end; i >= searchStart; i-- {
			ch := runes[i]
			if ch == '.' || ch == '?' || ch == '!' || ch == '\n' {
				splitAt = i + 1
				break
			}
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:splitAt])))
		start = splitAt - strategy.ChunkOverlap
		if start < 0 {
			start = 0
		}
	}

	// Filter empty chunks
	var result []string
	for _, c := range chunks {
		if utf8.RuneCountInString(strings.TrimSpace(c)) > 0 {
			result = append(result, strings.TrimSpace(c))
		}
	}
	return result
}
