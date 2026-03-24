package memory

import "context"

// EmbeddingProvider generates vector embeddings from text.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
}

// NoopEmbedding is a placeholder when no embedding provider is configured.
type NoopEmbedding struct{}

func (n *NoopEmbedding) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}

func (n *NoopEmbedding) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	return result, nil
}

func (n *NoopEmbedding) Dimensions() int { return 0 }
