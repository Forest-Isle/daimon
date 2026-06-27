package sleep

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/world"
)

const embedBatchLimit = 64

// EmbedJob backfills semantic embeddings for journal rows during sleep cycles.
type EmbedJob struct {
	world    *world.Store
	embedder world.Embedder
}

// NewEmbedJob builds a journal embedding backfill job. A nil embedder is a
// supported disabled state and makes the job a no-op.
func NewEmbedJob(w *world.Store, e world.Embedder) *EmbedJob {
	return &EmbedJob{world: w, embedder: e}
}

func (j *EmbedJob) Name() string { return "embed" }

func (j *EmbedJob) Run(ctx context.Context) (string, error) {
	if j.embedder == nil {
		return "semantic embedding disabled", nil
	}
	entries, err := j.world.ListJournalWithoutEmbedding(ctx, embedBatchLimit)
	if err != nil {
		return "", err
	}
	embedded := 0
	for _, entry := range entries {
		vec, err := j.embedder.Embed(ctx, entry.Summary+" "+entry.Detail)
		if err != nil {
			slog.Warn("sleep: embed journal entry failed", "id", entry.ID, "err", err)
			continue
		}
		if err := j.world.SetJournalEmbedding(ctx, entry.ID, vec); err != nil {
			slog.Warn("sleep: store journal embedding failed", "id", entry.ID, "err", err)
			continue
		}
		embedded++
	}
	return fmt.Sprintf("embedded %d journal entries", embedded), nil
}
