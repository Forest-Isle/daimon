package world

import (
	"context"
	"sort"
)

func (s *Store) searchSemanticJournal(ctx context.Context, text string, kinds []string, limit int) []rankedHit {
	if s.embedder == nil {
		return nil
	}
	queryVec, err := s.embedder.Embed(ctx, text)
	if err != nil || len(queryVec) == 0 {
		return nil
	}

	kindSQL, kindArgs := kindClause(kinds, "kind")
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, summary, detail, occurred_at, embedding
		FROM journal
		WHERE embedding IS NOT NULL`+kindSQL, kindArgs...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	type semanticHit struct {
		hit        Hit
		similarity float64
	}
	var candidates []semanticHit
	for rows.Next() {
		var id, kind, summary, detail, occurred string
		var blob []byte
		if err := rows.Scan(&id, &kind, &summary, &detail, &occurred, &blob); err != nil {
			continue
		}
		similarity := cosineSimilarity(queryVec, deserializeEmbedding(blob))
		if similarity <= 0 {
			continue
		}
		candidates = append(candidates, semanticHit{
			hit: Hit{
				Source: "journal", ID: id, Kind: kind,
				Title: summary, Text: detail, OccurredAt: occurred,
			},
			similarity: similarity,
		})
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].similarity != candidates[j].similarity {
			return candidates[i].similarity > candidates[j].similarity
		}
		return candidates[i].hit.ID < candidates[j].hit.ID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]rankedHit, len(candidates))
	for i, candidate := range candidates {
		out[i] = rankedHit{hit: candidate.hit, rank: i, relevance: clampRelevance(candidate.similarity)}
	}
	return out
}

func clampRelevance(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
