package world

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"unicode"
)

// Query is a retrieval request against the world model.
type Query struct {
	Text  string   // free text; empty returns the most recent journal entries
	Limit int      // max hits to return; defaults to 8
	Kinds []string // optional journal-kind filter (outcome|decision|correction|fact)
}

// Hit is one retrieved world item, ranked across sources.
type Hit struct {
	Source     string  `json:"source"` // "journal" | "commitment"
	ID         string  `json:"id"`
	Kind       string  `json:"kind,omitempty"`
	Title      string  `json:"title"` // journal summary / commitment title
	Text       string  `json:"text,omitempty"`
	OccurredAt string  `json:"occurred_at,omitempty"`
	Score      float64 `json:"score"`
}

const defaultRetrieveLimit = 8

// Retrieve does a hybrid lexical search across the journal and commitments and
// returns the top hits fused by reciprocal-rank. It prefers FTS5 (BM25) and
// falls back to LIKE per source when FTS5 is unavailable or the query is
// malformed, so retrieval degrades gracefully rather than failing. An empty
// query returns the most recent journal entries (a useful default context).
//
// This is the lexical foundation; a vector channel can be fused in later without
// changing the signature (the RRF merge already takes ranked id lists).
func (s *Store) Retrieve(ctx context.Context, q Query) ([]Hit, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultRetrieveLimit
	}

	text := sanitizeFTSQuery(q.Text)
	if text == "" {
		return s.recentJournal(ctx, q.Kinds, limit)
	}

	journalRanked := s.searchJournal(ctx, text, q.Kinds, limit*3)
	commitRanked := s.searchCommitments(ctx, text, limit*3)

	hits := rrfMerge(journalRanked, commitRanked)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// rankedHit pairs a hit with its within-source rank (0-based) for RRF fusion.
type rankedHit struct {
	hit  Hit
	rank int
}

// rrfMerge fuses per-source ranked lists into one ordering by reciprocal-rank
// fusion (k=60, matching the memory retriever), then sorts by the fused score.
func rrfMerge(lists ...[]rankedHit) []Hit {
	const k = 60.0
	type acc struct {
		hit   Hit
		score float64
	}
	merged := make(map[string]*acc)
	for _, list := range lists {
		for _, rh := range list {
			key := rh.hit.Source + ":" + rh.hit.ID
			a, ok := merged[key]
			if !ok {
				a = &acc{hit: rh.hit}
				merged[key] = a
			}
			a.score += 1.0 / (k + float64(rh.rank+1))
		}
	}
	out := make([]Hit, 0, len(merged))
	for _, a := range merged {
		a.hit.Score = a.score
		out = append(out, a.hit)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		// Stable tiebreak: newer first, then id for determinism.
		if out[i].OccurredAt != out[j].OccurredAt {
			return out[i].OccurredAt > out[j].OccurredAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Store) searchJournal(ctx context.Context, ftsText string, kinds []string, limit int) []rankedHit {
	// FTS5 first. OR the terms so any one can match (recall-oriented for memory
	// retrieval); RRF + bm25 rank handle relevance ordering.
	rows, err := s.db.QueryContext(ctx, `
		SELECT j.id, j.kind, j.summary, j.detail, j.occurred_at
		FROM journal_fts f
		JOIN journal j ON j.id = f.journal_id
		WHERE journal_fts MATCH ?
		ORDER BY f.rank
		LIMIT ?`, ftsOrExpr(ftsText), limit)
	if err != nil {
		return s.likeJournal(ctx, ftsText, kinds, limit)
	}
	defer func() { _ = rows.Close() }()
	hits := scanJournalHits(rows, kinds, limit)
	if err := rows.Err(); err != nil {
		return s.likeJournal(ctx, ftsText, kinds, limit)
	}
	return hits
}

func (s *Store) likeJournal(ctx context.Context, text string, kinds []string, limit int) []rankedHit {
	like := "%" + strings.ReplaceAll(text, " ", "%") + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, summary, detail, occurred_at
		FROM journal
		WHERE (summary LIKE ? OR detail LIKE ?)
		ORDER BY occurred_at DESC
		LIMIT ?`, like, like, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	return scanJournalHits(rows, kinds, limit)
}

func scanJournalHits(rows *sql.Rows, kinds []string, limit int) []rankedHit {
	allow := kindSet(kinds)
	var out []rankedHit
	for rows.Next() {
		var id, kind, summary, detail, occurred string
		if err := rows.Scan(&id, &kind, &summary, &detail, &occurred); err != nil {
			continue
		}
		if allow != nil && !allow[kind] {
			continue
		}
		out = append(out, rankedHit{
			hit: Hit{
				Source: "journal", ID: id, Kind: kind,
				Title: summary, Text: detail, OccurredAt: occurred,
			},
			rank: len(out),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) searchCommitments(ctx context.Context, ftsText string, limit int) []rankedHit {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.kind, c.title, c.body
		FROM commitments_fts f
		JOIN commitments c ON c.id = f.commitment_id
		WHERE commitments_fts MATCH ?
		ORDER BY f.rank
		LIMIT ?`, ftsOrExpr(ftsText), limit)
	if err != nil {
		return s.likeCommitments(ctx, ftsText, limit)
	}
	defer func() { _ = rows.Close() }()
	hits := scanCommitmentHits(rows, limit)
	if err := rows.Err(); err != nil {
		return s.likeCommitments(ctx, ftsText, limit)
	}
	return hits
}

func (s *Store) likeCommitments(ctx context.Context, text string, limit int) []rankedHit {
	like := "%" + strings.ReplaceAll(text, " ", "%") + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, title, body
		FROM commitments
		WHERE (title LIKE ? OR body LIKE ?)
		ORDER BY updated_at DESC
		LIMIT ?`, like, like, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	return scanCommitmentHits(rows, limit)
}

func scanCommitmentHits(rows *sql.Rows, limit int) []rankedHit {
	var out []rankedHit
	for rows.Next() {
		var id, kind, title, body string
		if err := rows.Scan(&id, &kind, &title, &body); err != nil {
			continue
		}
		out = append(out, rankedHit{
			hit: Hit{
				Source: "commitment", ID: id, Kind: kind,
				Title: title, Text: body,
			},
			rank: len(out),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// recentJournal returns the newest journal entries when there is no query text.
func (s *Store) recentJournal(ctx context.Context, kinds []string, limit int) ([]Hit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, summary, detail, occurred_at
		FROM journal
		ORDER BY occurred_at DESC
		LIMIT ?`, limit*3)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ranked := scanJournalHits(rows, kinds, limit)
	out := make([]Hit, 0, len(ranked))
	for i, rh := range ranked {
		// Recency-ordered: score by position so callers can treat it uniformly.
		rh.hit.Score = 1.0 / float64(i+1)
		out = append(out, rh.hit)
	}
	return out, nil
}

func kindSet(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	set := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		set[k] = true
	}
	return set
}

// ftsOrExpr turns a sanitized space-separated term list into an FTS5 OR
// expression ("daimon storage" -> "daimon OR storage"), so a row matching any
// term is retrieved. The caller guarantees the input is already sanitized
// (no FTS operators), so the only token added is OR.
func ftsOrExpr(sanitized string) string {
	fields := strings.Fields(sanitized)
	if len(fields) == 0 {
		return sanitized
	}
	return strings.Join(fields, " OR ")
}

// sanitizeFTSQuery strips FTS5 operators and punctuation, dropping bare boolean
// keywords and single characters, so arbitrary user text is a safe MATCH query.
func sanitizeFTSQuery(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	words := strings.Fields(b.String())
	clean := make([]string, 0, len(words))
	for _, w := range words {
		switch strings.ToUpper(w) {
		case "AND", "OR", "NOT", "NEAR":
			continue
		}
		if len([]rune(w)) >= 2 {
			clean = append(clean, w)
		}
	}
	return strings.Join(clean, " ")
}
