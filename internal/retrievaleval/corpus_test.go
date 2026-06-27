package retrievaleval

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

func TestSeedCorpusDeterministic(t *testing.T) {
	ctx := context.Background()
	_, qs1 := seedBenchStore(t, ctx)
	_, qs2 := seedBenchStore(t, ctx)
	if !reflect.DeepEqual(qs1, qs2) {
		t.Fatalf("SeedCorpus queries differ:\n%#v\n%#v", qs1, qs2)
	}
}

func TestSeedCorpusBoostedReport(t *testing.T) {
	ctx := context.Background()
	ws, qs := seedBenchStore(t, ctx)

	report, err := Run(ctx, ws.Retrieve, qs, 13)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, q := range qs {
		hits, err := ws.Retrieve(ctx, world.Query{Text: q.Text, Limit: q.Limit})
		if err != nil {
			t.Fatalf("Retrieve(%s): %v", q.Name, err)
		}
		recall, _, _, _ := Metrics(hits, q.Gold)
		if recall < 1 {
			t.Fatalf("%s recall=%.3f, want 1.000 hits=%v", q.Name, recall, hitIDs(hits))
		}
		t.Logf("%s recall=%.3f hits=%v", q.Name, recall, hitIDs(hits))
	}

	baseline := SystemReport{
		Recall:         0.5,
		Precision:      0.20833333333333331,
		F1:             0.29166666666666663,
		MRR:            0.3333333333333333,
		TokensPerQuery: 31,
		StoreSize:      13,
	}
	if report.Recall <= baseline.Recall {
		t.Fatalf("Recall = %.3f, want > baseline %.3f", report.Recall, baseline.Recall)
	}
	if report.Precision <= baseline.Precision {
		t.Fatalf("Precision = %.3f, want > baseline %.3f", report.Precision, baseline.Precision)
	}
	if report.F1 <= baseline.F1 {
		t.Fatalf("F1 = %.3f, want > baseline %.3f", report.F1, baseline.F1)
	}

	assertReport(t, report, SystemReport{
		Recall:         1,
		Precision:      0.4583333333333333,
		F1:             0.625,
		MRR:            1,
		TokensPerQuery: 29.75,
		StoreSize:      13,
	})
}

func TestSeedParaphraseCorpusSemanticRecall(t *testing.T) {
	ctx := context.Background()
	lexicalStore, qs := seedParaphraseBenchStore(t, ctx)
	lexicalReport, err := Run(ctx, lexicalStore.Retrieve, qs, 6)
	if err != nil {
		t.Fatalf("Run lexical error = %v", err)
	}

	semanticStore, semanticQs := seedParaphraseBenchStore(t, ctx)
	if !reflect.DeepEqual(qs, semanticQs) {
		t.Fatalf("SeedParaphraseCorpus queries differ:\n%#v\n%#v", qs, semanticQs)
	}
	embedJournalEntries(t, ctx, semanticStore, ConceptEmbedder{})
	semanticStore.SetEmbedder(ConceptEmbedder{})
	semanticReport, err := Run(ctx, semanticStore.Retrieve, semanticQs, 6)
	if err != nil {
		t.Fatalf("Run semantic error = %v", err)
	}

	if lexicalReport.Recall >= 1 {
		t.Fatalf("lexical recall = %.3f, want < 1", lexicalReport.Recall)
	}
	if semanticReport.Recall != 1 {
		t.Fatalf("semantic recall = %.3f, want 1", semanticReport.Recall)
	}
	for _, q := range semanticQs {
		lexicalHits, err := lexicalStore.Retrieve(ctx, world.Query{Text: q.Text, Limit: q.Limit})
		if err != nil {
			t.Fatalf("lexical Retrieve(%s): %v", q.Name, err)
		}
		lexicalRecall, _, _, _ := Metrics(lexicalHits, q.Gold)
		if lexicalRecall >= 1 {
			t.Fatalf("%s lexical recall=%.3f, want < 1 hits=%v", q.Name, lexicalRecall, hitIDs(lexicalHits))
		}

		semanticHits, err := semanticStore.Retrieve(ctx, world.Query{Text: q.Text, Limit: q.Limit})
		if err != nil {
			t.Fatalf("semantic Retrieve(%s): %v", q.Name, err)
		}
		semanticRecall, _, _, _ := Metrics(semanticHits, q.Gold)
		if semanticRecall != 1 {
			t.Fatalf("%s semantic recall=%.3f, want 1 hits=%v", q.Name, semanticRecall, hitIDs(semanticHits))
		}
	}
}

func TestRankRobustness(t *testing.T) {
	ctx := context.Background()
	ws, qs := seedRobustnessBenchStore(t, ctx)

	for _, q := range qs {
		q := q
		t.Run(q.Name, func(t *testing.T) {
			hits, err := ws.Retrieve(ctx, world.Query{Text: q.Text, Limit: q.Limit})
			if err != nil {
				t.Fatalf("Retrieve(%s): %v", q.Name, err)
			}
			diagnosticHits, err := ws.Retrieve(ctx, world.Query{Text: q.Text, Limit: 3})
			if err != nil {
				t.Fatalf("diagnostic Retrieve(%s): %v", q.Name, err)
			}

			top1Gold := len(hits) > 0 && q.Gold[hits[0].ID]
			t.Logf("%s limit=%d top=%v gold_top1=%t", q.Name, q.Limit, hitDetails(hits, q.Gold), top1Gold)
			t.Logf("%s top3=%v", q.Name, hitDetails(diagnosticHits, q.Gold))
			if !top1Gold {
				t.Fatalf("%s top-1 = %v, want gold fact in top-1", q.Name, hitDetails(hits, q.Gold))
			}
		})
	}
}

func hitIDs(hits []world.Hit) []string {
	ids := make([]string, len(hits))
	for i, hit := range hits {
		ids[i] = hit.ID
	}
	return ids
}

func hitDetails(hits []world.Hit, gold map[string]bool) []string {
	details := make([]string, len(hits))
	for i, hit := range hits {
		marker := "distractor"
		if gold[hit.ID] {
			marker = "gold"
		}
		details[i] = hit.ID + "(" + hit.Kind + "," + marker + ")"
	}
	return details
}

func seedBenchStore(t *testing.T, ctx context.Context) (*world.Store, []LabeledQuery) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ws := world.NewStore(db.DB)
	qs, err := SeedCorpus(ctx, ws)
	if err != nil {
		t.Fatalf("SeedCorpus() error = %v", err)
	}
	return ws, qs
}

func seedRobustnessBenchStore(t *testing.T, ctx context.Context) (*world.Store, []LabeledQuery) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatalf("open robustness test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ws := world.NewStore(db.DB)
	qs, err := SeedRobustnessCorpus(ctx, ws)
	if err != nil {
		t.Fatalf("SeedRobustnessCorpus() error = %v", err)
	}
	return ws, qs
}

func seedParaphraseBenchStore(t *testing.T, ctx context.Context) (*world.Store, []LabeledQuery) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatalf("open paraphrase test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ws := world.NewStore(db.DB)
	qs, err := SeedParaphraseCorpus(ctx, ws)
	if err != nil {
		t.Fatalf("SeedParaphraseCorpus() error = %v", err)
	}
	return ws, qs
}

func embedJournalEntries(t *testing.T, ctx context.Context, ws *world.Store, embedder ConceptEmbedder) {
	t.Helper()
	entries, err := ws.ListJournal(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListJournal() error = %v", err)
	}
	for _, entry := range entries {
		vec, err := embedder.Embed(ctx, entry.Summary+" "+entry.Detail)
		if err != nil {
			t.Fatalf("Embed(%s) error = %v", entry.ID, err)
		}
		if err := ws.SetJournalEmbedding(ctx, entry.ID, vec); err != nil {
			t.Fatalf("SetJournalEmbedding(%s) error = %v", entry.ID, err)
		}
	}
}
