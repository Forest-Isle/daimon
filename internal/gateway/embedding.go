package gateway

import (
	"context"
	"strings"

	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/memory"
)

// embedderHandle bundles an embedding provider with an optional readiness gate.
// waitReady is nil when the provider is usable immediately (OpenAI or no-op);
// for the local lazy engine it blocks until background warmup finishes, then
// reports whether the engine became usable.
type embedderHandle struct {
	provider  memory.EmbeddingProvider
	waitReady func(context.Context) bool
	real      bool
}

// buildEmbedder selects the embedding provider by fallback chain:
// valid OpenAI key → local engine (if enabled) → no-op. A key that is empty or
// an unexpanded ${...} placeholder counts as absent.
//
// The memory and codebase-index subsystems each call this independently, so a
// local fallback spins up two lazy warmups; Ollama deduplicates concurrent
// pulls of the same model, so the cost is a redundant probe, not a double
// download.
func buildEmbedder(cfg *config.Config) embedderHandle {
	if k := cfg.Memory.OpenAIAPIKey; k != "" && !strings.HasPrefix(k, "${") {
		return embedderHandle{
			provider: memory.NewCachedEmbedder(
				memory.NewOpenAIEmbeddingWithURL(k, cfg.Memory.EmbeddingModel, cfg.Memory.EmbeddingBaseURL)),
			real: true,
		}
	}
	if cfg.Memory.LocalEmbedding.Enabled {
		provider, waitReady := memory.StartLocalEmbedding(memory.LocalEmbeddingOptions{
			Engine:   cfg.Memory.LocalEmbedding.Engine,
			Model:    cfg.Memory.LocalEmbedding.Model,
			Host:     cfg.Memory.LocalEmbedding.Host,
			AutoPull: cfg.Memory.LocalEmbedding.AutoPull,
		})
		return embedderHandle{provider: provider, waitReady: waitReady, real: true}
	}
	return embedderHandle{provider: &memory.NoopEmbedding{}, real: false}
}
