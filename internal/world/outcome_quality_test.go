package world

import (
	"context"
	"testing"
)

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name    string
		detail  string
		summary string
		want    OutcomeQuality
	}{
		{"clean_empty", "", "Handled the request.", OutcomeClean},
		{"salvaged", "salvaged=true", "recovered work", OutcomeSalvaged},
		{"tool_failures", "tool_failures=2", "recovered after errors", OutcomeToolFailures},
		{"unverified_actions", "unverified_actions=1", "took a governed action", OutcomeUnverifiedActions},
		// "=0" is not a failure — must read as clean (mirrors the distiller's parse).
		{"tool_failures_zero", "tool_failures=0", "fine", OutcomeClean},
		{"unverified_zero", "unverified_actions=0", "fine", OutcomeClean},
		// failEpisode records empty detail; the failure signal is only in the summary.
		{"failed_stream", "", "episode stream error: boom", OutcomeFailed},
		{"failed_panic", "", "episode panic: nil deref", OutcomeFailed},
		{"failed_write", "", "world write failed: bad op", OutcomeFailed},
		// A failure summary wins even if a detail is somehow present (defensive).
		{"failed_over_detail", "tool_failures=1", "episode stream error: x", OutcomeFailed},
		// Whitespace tolerance.
		{"clean_blank_detail", "   ", "ok", OutcomeClean},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyOutcome(tc.detail, tc.summary); got != tc.want {
				t.Fatalf("ClassifyOutcome(%q,%q) = %v, want %v", tc.detail, tc.summary, got, tc.want)
			}
		})
	}
}

func TestOutcomeQualityForEpisodes(t *testing.T) {
	ctx := context.Background()
	db := openWorldTestDB(t)
	ws := NewStore(db.DB)

	seed := func(episodeID, detail, summary string) {
		t.Helper()
		if err := ws.AppendJournal(ctx, JournalEntry{
			ID: "journal_outcome_" + episodeID, EpisodeID: episodeID, Kind: "outcome",
			Summary: summary, Detail: detail,
		}); err != nil {
			t.Fatalf("seed %s: %v", episodeID, err)
		}
	}
	seed("ep_clean", "", "all good")
	seed("ep_tf", "tool_failures=1", "recovered")
	seed("ep_unv", "unverified_actions=2", "governed action")
	seed("ep_salv", "salvaged=true", "salvaged")
	seed("ep_fail", "", "episode stream error: x")
	// A non-outcome journal row with the same episode must be ignored.
	if err := ws.AppendJournal(ctx, JournalEntry{ID: "d1", EpisodeID: "ep_clean", Kind: "decision", Summary: "a decision"}); err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	// A STRAY kind='outcome' row for ep_clean with a NON-canonical id must NOT
	// override the canonical clean outcome (we match the canonical PK, not just
	// kind+episode_id). If this leaked in, ep_clean could be misread as a failure.
	if err := ws.AppendJournal(ctx, JournalEntry{ID: "stray_outcome_ep_clean", EpisodeID: "ep_clean", Kind: "outcome", Summary: "episode stream error: spoof", Detail: "tool_failures=9"}); err != nil {
		t.Fatalf("seed stray outcome: %v", err)
	}

	got, err := ws.OutcomeQualityForEpisodes(ctx, []string{
		"ep_clean", "ep_tf", "ep_unv", "ep_salv", "ep_fail", "ep_missing",
	})
	if err != nil {
		t.Fatalf("OutcomeQualityForEpisodes: %v", err)
	}
	want := map[string]OutcomeQuality{
		"ep_clean": OutcomeClean,
		"ep_tf":    OutcomeToolFailures,
		"ep_unv":   OutcomeUnverifiedActions,
		"ep_salv":  OutcomeSalvaged,
		"ep_fail":  OutcomeFailed,
		// ep_missing has no outcome row → absent (treated as unknown by callers).
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for id, q := range want {
		if got[id] != q {
			t.Fatalf("quality[%s] = %v, want %v", id, got[id], q)
		}
	}
	if _, ok := got["ep_missing"]; ok {
		t.Fatal("ep_missing should be absent from the map")
	}

	// Empty id list → empty map, no error.
	empty, err := ws.OutcomeQualityForEpisodes(ctx, nil)
	if err != nil {
		t.Fatalf("empty lookup: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty lookup = %+v, want empty", empty)
	}

	if _, err := (*Store)(nil).OutcomeQualityForEpisodes(ctx, []string{"x"}); err == nil {
		t.Fatal("nil store must error")
	}
}
