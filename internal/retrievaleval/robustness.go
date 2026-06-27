package retrievaleval

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/daimon/internal/world"
)

// SeedRobustnessCorpus writes adversarial retrieval cases that diagnose whether
// provenance and recency boosts can outrank stronger topical lexical matches.
func SeedRobustnessCorpus(ctx context.Context, ws *world.Store) ([]LabeledQuery, error) {
	entries := []world.JournalEntry{
		{
			ID:         "bench_robust_quartz_fact",
			Kind:       "fact",
			Summary:    "quartz vector index owner status",
			Detail:     "quartz vector index owner status belongs to the search platform team",
			OccurredAt: "2025-01-10 09:00:00",
		},
		{
			ID:         "bench_robust_quartz_correction",
			Kind:       "correction",
			Summary:    "status correction",
			Detail:     "correction says espresso grinder status changed after cleaning",
			OccurredAt: "2026-06-26 09:00:00",
		},
		{
			ID:         "bench_robust_harbor_fact",
			Kind:       "fact",
			Summary:    "harbor backup encryption policy",
			Detail:     "harbor backup encryption policy requires offline vault rotation",
			OccurredAt: "2025-02-10 09:00:00",
		},
		{
			ID:         "bench_robust_harbor_decision",
			Kind:       "decision",
			Summary:    "policy decision",
			Detail:     "decision switches studio lamp policy after outage recovery",
			OccurredAt: "2026-06-25 09:00:00",
		},
		{
			ID:         "bench_robust_atlas_fact",
			Kind:       "fact",
			Summary:    "atlas schema migration mode",
			Detail:     "atlas schema migration mode remains expand contract for production",
			OccurredAt: "2025-03-10 09:00:00",
		},
		{
			ID:         "bench_robust_atlas_correction",
			Kind:       "correction",
			Summary:    "mode correction",
			Detail:     "correction says writing mode uses distraction free theme",
			OccurredAt: "2026-06-24 09:00:00",
		},
	}

	for _, entry := range entries {
		if err := ws.AppendJournal(ctx, entry); err != nil {
			return nil, fmt.Errorf("seed robustness journal %s: %w", entry.ID, err)
		}
	}

	return []LabeledQuery{
		{
			Name:  "recent correction versus topical fact",
			Text:  "quartz vector index owner status",
			Gold:  map[string]bool{"bench_robust_quartz_fact": true},
			Limit: 1,
		},
		{
			Name:  "recent decision versus topical fact",
			Text:  "harbor backup encryption policy",
			Gold:  map[string]bool{"bench_robust_harbor_fact": true},
			Limit: 1,
		},
		{
			Name:  "second correction versus topical fact",
			Text:  "atlas schema migration mode",
			Gold:  map[string]bool{"bench_robust_atlas_fact": true},
			Limit: 1,
		},
	}, nil
}
