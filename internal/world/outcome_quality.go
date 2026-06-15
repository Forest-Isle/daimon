package world

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// OutcomeQuality is how well an episode's outcome turned out, derived from the
// outcome journal row's detail (written by claimOutcomeJournal) and summary
// (failEpisode markers). It is the canonical reading of the per-episode signals
// J11 (tool failures) and J12 (unverified governed actions) record, and the basis
// of the economy ROI-by-class report (blueprint §4.11): a class whose episodes are
// mostly degraded is buying little value for its tokens.
type OutcomeQuality int

const (
	// OutcomeClean: closed through episode_close with no tool failure and every
	// governed action verified — the only quality that counts as delivered value.
	OutcomeClean OutcomeQuality = iota
	// OutcomeToolFailures: closed cleanly but at least one tool call errored.
	OutcomeToolFailures
	// OutcomeUnverifiedActions: closed cleanly but took a governed action that was
	// not verified on this run (compensable/irreversible, or a failed reversible).
	OutcomeUnverifiedActions
	// OutcomeSalvaged: the model never called episode_close; the framework recovered.
	OutcomeSalvaged
	// OutcomeFailed: a framework-recorded failure (failEpisode: stream error, panic,
	// or world-write failure).
	OutcomeFailed
)

func (q OutcomeQuality) String() string {
	switch q {
	case OutcomeClean:
		return "clean"
	case OutcomeToolFailures:
		return "tool_failures"
	case OutcomeUnverifiedActions:
		return "unverified_actions"
	case OutcomeSalvaged:
		return "salvaged"
	case OutcomeFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ClassifyOutcome maps an outcome journal row's (detail, summary) to a quality
// bucket using the same contract the writers use. Precedence: a framework failure
// (summary marker, with empty detail) is checked first, then the single-valued
// detail in the order claimOutcomeJournal assigns it (salvaged > tool_failures >
// unverified_actions); anything else is clean. Only OutcomeClean represents a
// fully-verified delivered outcome.
func ClassifyOutcome(detail, summary string) OutcomeQuality {
	if isFailedOutcomeSummary(summary) {
		return OutcomeFailed
	}
	detail = strings.TrimSpace(detail)
	switch {
	case detail == "salvaged=true":
		return OutcomeSalvaged
	case positiveCount(detail, "tool_failures="):
		return OutcomeToolFailures
	case positiveCount(detail, "unverified_actions="):
		return OutcomeUnverifiedActions
	default:
		return OutcomeClean
	}
}

// positiveCount reports whether detail is "<prefix>N" with N > 0, so a "<key>=0"
// (or non-numeric) detail is treated as clean — mirroring the distiller's parse.
func positiveCount(detail, prefix string) bool {
	rest, ok := strings.CutPrefix(detail, prefix)
	if !ok {
		return false
	}
	n, err := strconv.Atoi(rest)
	return err == nil && n > 0
}

// isFailedOutcomeSummary reports whether an outcome summary is a framework-recorded
// failure (a failEpisode summary). failEpisode records an empty detail, so the
// failure signal lives only in the summary text.
func isFailedOutcomeSummary(summary string) bool {
	s := strings.ToLower(summary)
	return strings.Contains(s, "episode stream error") ||
		strings.Contains(s, "episode panic") ||
		strings.Contains(s, "world write failed")
}

// OutcomeQualityForEpisodes returns the quality of each given episode's outcome,
// keyed by episode id. Episodes with no outcome row are absent from the map (the
// caller treats them as unknown). It is safe to call from a read-only report.
//
// Each episode's outcome is identified by its CANONICAL row id
// "journal_outcome_<episodeID>" — the same primary-key marker ApplyOutcome writes
// (INSERT OR IGNORE) and OutcomeExists checks — not merely by (kind='outcome',
// episode_id). That exactness matters: a stray journal.append of another
// kind='outcome' row for the same episode could otherwise overwrite the map in
// nondeterministic order. Matching the PK gives exactly one row per episode. ids
// are looked up in chunks to stay within SQLite's bound-parameter limit.
func (s *Store) OutcomeQualityForEpisodes(ctx context.Context, ids []string) (map[string]OutcomeQuality, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	out := make(map[string]OutcomeQuality, len(ids))
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = "journal_outcome_" + id // canonical outcome marker (primary key)
		}
		query := fmt.Sprintf(
			`SELECT episode_id, detail, summary FROM journal
			 WHERE kind = 'outcome' AND id IN (%s)`,
			strings.Join(placeholders, ","))
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("outcome quality lookup: %w", err)
		}
		err = func() error {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var episodeID, detail, summary string
				if err := rows.Scan(&episodeID, &detail, &summary); err != nil {
					return fmt.Errorf("outcome quality scan: %w", err)
				}
				out[episodeID] = ClassifyOutcome(detail, summary)
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
