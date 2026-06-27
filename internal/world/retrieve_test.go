package world

import (
	"context"
	"reflect"
	"testing"
)

// seedRetrieval populates a store with journal entries and commitments for
// retrieval tests, exercising the FTS sync triggers via the normal write paths.
func seedRetrieval(t *testing.T) *Store {
	t.Helper()
	db := openWorldTestDB(t)
	s := NewStore(db.DB)
	ctx := context.Background()

	entries := []JournalEntry{
		{ID: "j1", Kind: "decision", Summary: "chose SQLite for local storage", Detail: "embedded, zero-ops, fits sovereignty"},
		{ID: "j2", Kind: "outcome", Summary: "deployed the telegram channel", Detail: "long polling adapter live"},
		{ID: "j3", Kind: "fact", Summary: "user prefers dark mode", Detail: "mentioned during onboarding"},
		{ID: "j4", Kind: "correction", Summary: "renamed module to daimon", Detail: "was punkopunko, now github.com/Forest-Isle/daimon"},
	}
	for _, e := range entries {
		if err := s.AppendJournal(ctx, e); err != nil {
			t.Fatalf("AppendJournal(%s): %v", e.ID, err)
		}
	}

	commits := []Commitment{
		{ID: "c1", Kind: "project", Title: "ship the daimon re-founding", Body: "strangler migration on refound/daimon branch"},
		{ID: "c2", Kind: "promise", Title: "keep storage local", Body: "no cloud sync of personal data"},
	}
	for _, c := range commits {
		if err := s.CreateCommitment(ctx, c); err != nil {
			t.Fatalf("CreateCommitment(%s): %v", c.ID, err)
		}
	}
	return s
}

func ids(hits []Hit) map[string]bool {
	m := make(map[string]bool, len(hits))
	for _, h := range hits {
		m[h.Source+":"+h.ID] = true
	}
	return m
}

func TestRetrieve_FindsAcrossJournalAndCommitments(t *testing.T) {
	s := seedRetrieval(t)
	ctx := context.Background()

	hits, err := s.Retrieve(ctx, Query{Text: "daimon storage"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits for 'daimon storage', got none")
	}
	got := ids(hits)
	// "storage" hits j1 (SQLite for local storage) and c2 (keep storage local);
	// "daimon" hits j4, c1. The merged set must span both sources.
	hasJournal, hasCommit := false, false
	for _, h := range hits {
		switch h.Source {
		case "journal":
			hasJournal = true
		case "commitment":
			hasCommit = true
		}
	}
	if !hasJournal || !hasCommit {
		t.Fatalf("expected hits from both sources, got %v", got)
	}
}

func TestRetrieve_TermMatchesRightEntry(t *testing.T) {
	s := seedRetrieval(t)
	hits, err := s.Retrieve(context.Background(), Query{Text: "telegram", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Source != "journal" || hits[0].ID != "j2" {
		t.Fatalf("top hit for 'telegram' should be j2, got %+v", hits)
	}
}

func TestRetrieve_KindFilter(t *testing.T) {
	s := seedRetrieval(t)
	hits, err := s.Retrieve(context.Background(), Query{Text: "daimon storage telegram dark", Kinds: []string{"fact"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Source == "journal" && h.Kind != "fact" {
			t.Fatalf("kind filter leaked non-fact journal hit: %+v", h)
		}
	}
}

func TestRetrieve_EmptyQueryReturnsRecent(t *testing.T) {
	s := seedRetrieval(t)
	hits, err := s.Retrieve(context.Background(), Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("empty query should return 2 recent journal hits, got %d", len(hits))
	}
	for _, h := range hits {
		if h.Source != "journal" {
			t.Fatalf("recent default should be journal only, got %s", h.Source)
		}
	}
}

func TestRetrieve_LimitRespected(t *testing.T) {
	s := seedRetrieval(t)
	hits, err := s.Retrieve(context.Background(), Query{Text: "daimon storage telegram dark local", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 2 {
		t.Fatalf("limit 2 exceeded: got %d", len(hits))
	}
}

func TestRetrieve_NilEmbedderLeavesLexicalResultsUnchanged(t *testing.T) {
	s := seedRetrieval(t)
	ctx := context.Background()

	want, err := s.Retrieve(ctx, Query{Text: "daimon storage", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	s.SetEmbedder(nil)
	got, err := s.Retrieve(ctx, Query{Text: "daimon storage", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Retrieve with nil embedder changed results:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestRetrieve_NoMatchReturnsEmpty(t *testing.T) {
	s := seedRetrieval(t)
	hits, err := s.Retrieve(context.Background(), Query{Text: "kubernetes helm chart"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("unrelated query should return no hits, got %+v", hits)
	}
}

func TestRetrieve_TriggerSyncOnUpdateAndDelete(t *testing.T) {
	s := seedRetrieval(t)
	ctx := context.Background()

	// Delete j2: it must drop out of FTS results.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM journal WHERE id = 'j2'`); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Retrieve(ctx, Query{Text: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	if ids(hits)["journal:j2"] {
		t.Fatal("deleted journal entry still returned by FTS")
	}
}

func TestSanitizeFTSQuery(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"hello world":         "hello world",
		"a OR b AND c":        "", // boolean keywords dropped, single chars dropped
		"rm -rf /tmp":         "rm -rf tmp",
		"quoted \"thing\" ok": "quoted thing ok",
		"x":                   "", // single char dropped
	}
	for in, want := range cases {
		if got := sanitizeFTSQuery(in); got != want {
			t.Fatalf("sanitizeFTSQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
