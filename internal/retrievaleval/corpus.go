package retrievaleval

import (
	"context"
	"fmt"

	"github.com/Forest-Isle/daimon/internal/world"
)

// SeedCorpus writes a deterministic personal-agent retrieval corpus and returns labels.
func SeedCorpus(ctx context.Context, ws *world.Store) ([]LabeledQuery, error) {
	entries := []world.JournalEntry{
		{
			ID:         "bench_codex_cache_fact_01",
			Kind:       "fact",
			Summary:    "codex cache migration rollout notes",
			Detail:     "codex cache migration rollout kept old planner notes for baseline lexical interference",
			OccurredAt: "2025-02-01 00:00:00",
		},
		{
			ID:         "bench_codex_cache_fact_02",
			Kind:       "fact",
			Summary:    "codex cache migration rollout checklist",
			Detail:     "codex cache migration rollout checklist repeated cache migration rollout terminology",
			OccurredAt: "2025-02-02 00:00:00",
		},
		{
			ID:         "bench_codex_cache_fact_03",
			Kind:       "fact",
			Summary:    "codex cache migration rollout archive",
			Detail:     "codex cache migration rollout archive captured pre-correction assumptions",
			OccurredAt: "2025-02-03 00:00:00",
		},
		{
			ID:         "bench_codex_cache_correction",
			Kind:       "correction",
			Summary:    "codex cache correction",
			Detail:     "correction says disable cache migration until checksums are verified",
			OccurredAt: "2026-06-10 09:00:00",
		},
		{
			ID:         "bench_scheduler_token_fact_01",
			Kind:       "fact",
			Summary:    "scheduler token budget planning",
			Detail:     "scheduler token budget planning used a broad retry policy for every agent",
			OccurredAt: "2025-03-01 00:00:00",
		},
		{
			ID:         "bench_scheduler_token_fact_02",
			Kind:       "fact",
			Summary:    "scheduler token budget planning notes",
			Detail:     "scheduler token budget planning notes repeated token budget planning vocabulary",
			OccurredAt: "2025-03-02 00:00:00",
		},
		{
			ID:         "bench_scheduler_token_fact_03",
			Kind:       "fact",
			Summary:    "scheduler token budget planning archive",
			Detail:     "scheduler token budget planning archive for older autonomous worker assumptions",
			OccurredAt: "2025-03-03 00:00:00",
		},
		{
			ID:         "bench_scheduler_token_decision",
			Kind:       "decision",
			Summary:    "scheduler budget decision",
			Detail:     "decision caps token retries for background agents after quota pressure",
			OccurredAt: "2026-06-11 10:00:00",
		},
		{
			ID:         "bench_memory_privacy_fact_01",
			Kind:       "fact",
			Summary:    "memory privacy redaction rule",
			Detail:     "memory privacy redaction keeps personal paths out of shared summaries",
			OccurredAt: "2026-06-01 08:00:00",
		},
		{
			ID:         "bench_memory_privacy_decision",
			Kind:       "decision",
			Summary:    "memory privacy decision",
			Detail:     "decision keeps redaction strict before memory export and report generation",
			OccurredAt: "2026-06-12 11:00:00",
		},
		{
			ID:         "bench_rollup_fact_01",
			Kind:       "fact",
			Summary:    "episode rollup summary cadence",
			Detail:     "episode rollup summary cadence runs after every completed local agent session",
			OccurredAt: "2026-06-13 12:00:00",
		},
	}

	for _, entry := range entries {
		if err := ws.AppendJournal(ctx, entry); err != nil {
			return nil, fmt.Errorf("seed journal %s: %w", entry.ID, err)
		}
	}

	commitments := []world.Commitment{
		{
			ID:    "bench_commit_release_notes",
			Kind:  "project",
			Title: "publish agent release notes",
			Body:  "collect release notes after retrieval evaluation is measured",
			State: "active",
		},
		{
			ID:    "bench_commit_weekly_review",
			Kind:  "routine",
			Title: "weekly memory review",
			Body:  "review memory privacy redaction and stale commitments",
			State: "active",
		},
	}
	for _, commitment := range commitments {
		if err := ws.CreateCommitment(ctx, commitment); err != nil {
			return nil, fmt.Errorf("seed commitment %s: %w", commitment.ID, err)
		}
	}

	return []LabeledQuery{
		{
			Name:  "cache correction",
			Text:  "codex cache migration rollout",
			Gold:  map[string]bool{"bench_codex_cache_correction": true},
			Limit: 2,
		},
		{
			Name:  "scheduler decision",
			Text:  "scheduler token budget planning",
			Gold:  map[string]bool{"bench_scheduler_token_decision": true},
			Limit: 2,
		},
		{
			Name:  "privacy decision",
			Text:  "memory privacy redaction",
			Gold:  map[string]bool{"bench_memory_privacy_decision": true},
			Limit: 4,
		},
		{
			Name:  "rollup fact",
			Text:  "episode rollup summary",
			Gold:  map[string]bool{"bench_rollup_fact_01": true},
			Limit: 3,
		},
	}, nil
}
