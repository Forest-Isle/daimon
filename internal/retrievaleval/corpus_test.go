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

func hitIDs(hits []world.Hit) []string {
	ids := make([]string, len(hits))
	for i, hit := range hits {
		ids[i] = hit.ID
	}
	return ids
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
