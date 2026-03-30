package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// CachedEmbedder wraps an EmbeddingProvider with SHA256-based caching.
type CachedEmbedder struct {
	provider EmbeddingProvider
	cache    map[string][]float32
	mu       sync.RWMutex
}

// NewCachedEmbedder creates a cached embedder.
func NewCachedEmbedder(provider EmbeddingProvider) *CachedEmbedder {
	return &CachedEmbedder{
		provider: provider,
		cache:    make(map[string][]float32),
	}
}

func (ce *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := ce.cacheKey(text)

	ce.mu.RLock()
	if emb, ok := ce.cache[key]; ok {
		ce.mu.RUnlock()
		return emb, nil
	}
	ce.mu.RUnlock()

	emb, err := ce.provider.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	ce.mu.Lock()
	ce.cache[key] = emb
	ce.mu.Unlock()

	return emb, nil
}

func (ce *CachedEmbedder) cacheKey(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func (ce *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := ce.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

func (ce *CachedEmbedder) Dimensions() int {
	return ce.provider.Dimensions()
}
