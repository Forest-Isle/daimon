package retrievaleval

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Forest-Isle/daimon/internal/world"
)

const conceptDimensions = 3

// ConceptEmbedder is a deterministic test embedder that maps synonyms onto
// shared concept dimensions.
type ConceptEmbedder struct{}

// Embed returns a sparse concept-count vector for recognized keywords.
func (ConceptEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	_ = ctx
	concepts := map[string]int{
		"reside":      0,
		"residence":   0,
		"relocated":   0,
		"relocation":  0,
		"moved":       0,
		"home":        0,
		"lives":       0,
		"live":        0,
		"credential":  1,
		"credentials": 1,
		"password":    1,
		"rotation":    1,
		"renewal":     1,
		"schedule":    2,
		"calendar":    2,
		"dawn":        2,
		"morning":     2,
	}
	vec := make([]float32, conceptDimensions)
	for _, token := range conceptTokens(text) {
		if dim, ok := concepts[token]; ok {
			vec[dim]++
		}
	}
	return vec, nil
}

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

// SeedParaphraseCorpus writes a deterministic paraphrase corpus where lexical
// retrieval misses the gold rows but ConceptEmbedder can recover them.
func SeedParaphraseCorpus(ctx context.Context, ws *world.Store) ([]LabeledQuery, error) {
	entries := []world.JournalEntry{
		{
			ID:         "bench_para_residence_correction",
			Kind:       "correction",
			Summary:    "residence correction",
			Detail:     "correction says the user now resides in Hangzhou after relocation",
			OccurredAt: "2026-06-14 09:00:00",
		},
		{
			ID:         "bench_para_laptop_fact",
			Kind:       "fact",
			Summary:    "laptop setup note",
			Detail:     "laptop bag inventory mentioned city stickers and chargers",
			OccurredAt: "2026-06-01 09:00:00",
		},
		{
			ID:         "bench_para_security_decision",
			Kind:       "decision",
			Summary:    "credential handling decision",
			Detail:     "decision keeps credential rotation weekly for local operator access",
			OccurredAt: "2026-06-15 10:00:00",
		},
		{
			ID:         "bench_para_keyboard_fact",
			Kind:       "fact",
			Summary:    "keyboard layout note",
			Detail:     "keyboard firmware notes for local macros and typing setup",
			OccurredAt: "2026-06-02 10:00:00",
		},
		{
			ID:         "bench_para_schedule_correction",
			Kind:       "correction",
			Summary:    "schedule correction",
			Detail:     "correction says focused work starts at dawn after the routine change",
			OccurredAt: "2026-06-16 11:00:00",
		},
		{
			ID:         "bench_para_planner_fact",
			Kind:       "fact",
			Summary:    "planner export note",
			Detail:     "planner export checked color labels and sidebar ordering",
			OccurredAt: "2026-06-03 11:00:00",
		},
	}
	for _, entry := range entries {
		if err := ws.AppendJournal(ctx, entry); err != nil {
			return nil, fmt.Errorf("seed paraphrase journal %s: %w", entry.ID, err)
		}
	}

	return []LabeledQuery{
		{
			Name:  "residence paraphrase",
			Text:  "home laptop",
			Gold:  map[string]bool{"bench_para_residence_correction": true},
			Limit: 1,
		},
		{
			Name:  "security paraphrase",
			Text:  "password keyboard",
			Gold:  map[string]bool{"bench_para_security_decision": true},
			Limit: 1,
		},
		{
			Name:  "schedule paraphrase",
			Text:  "morning planner",
			Gold:  map[string]bool{"bench_para_schedule_correction": true},
			Limit: 1,
		},
	}, nil
}

func conceptTokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
