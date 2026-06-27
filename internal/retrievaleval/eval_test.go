package retrievaleval

import (
	"context"
	"errors"
	"testing"

	"github.com/Forest-Isle/daimon/internal/world"
)

func TestMetrics(t *testing.T) {
	cases := []struct {
		name          string
		hits          []world.Hit
		gold          map[string]bool
		wantRecall    float64
		wantPrecision float64
		wantF1        float64
		wantMRR       float64
	}{
		{
			name: "empty gold",
			hits: []world.Hit{{ID: "a"}},
			gold: map[string]bool{},
		},
		{
			name: "empty hits",
			gold: map[string]bool{"a": true},
		},
		{
			name:          "all hit",
			hits:          []world.Hit{{ID: "a"}, {ID: "b"}},
			gold:          map[string]bool{"a": true, "b": true},
			wantRecall:    1,
			wantPrecision: 1,
			wantF1:        1,
			wantMRR:       1,
		},
		{
			name:          "partial hit",
			hits:          []world.Hit{{ID: "x"}, {ID: "b"}, {ID: "z"}},
			gold:          map[string]bool{"a": true, "b": true},
			wantRecall:    0.5,
			wantPrecision: 1.0 / 3.0,
			wantF1:        0.4,
			wantMRR:       0.5,
		},
		{
			name:          "mrr first gold rank",
			hits:          []world.Hit{{ID: "x"}, {ID: "y"}, {ID: "a"}},
			gold:          map[string]bool{"a": true},
			wantRecall:    1,
			wantPrecision: 1.0 / 3.0,
			wantF1:        0.5,
			wantMRR:       1.0 / 3.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRecall, gotPrecision, gotF1, gotMRR := Metrics(tc.hits, tc.gold)
			assertFloat(t, "recall", gotRecall, tc.wantRecall)
			assertFloat(t, "precision", gotPrecision, tc.wantPrecision)
			assertFloat(t, "f1", gotF1, tc.wantF1)
			assertFloat(t, "mrr", gotMRR, tc.wantMRR)
		})
	}
}

func TestRun(t *testing.T) {
	wantErr := errors.New("rank failed")
	cases := []struct {
		name      string
		rank      Ranker
		queries   []LabeledQuery
		storeSize int
		want      SystemReport
		wantErr   bool
	}{
		{
			name: "empty queries",
			rank: func(context.Context, world.Query) ([]world.Hit, error) {
				t.Fatal("rank should not be called")
				return nil, nil
			},
			storeSize: 3,
			want:      SystemReport{StoreSize: 3},
		},
		{
			name: "averages metrics and tokens",
			rank: func(ctx context.Context, q world.Query) ([]world.Hit, error) {
				switch q.Text {
				case "one":
					return []world.Hit{{ID: "a", Title: "alpha beta", Text: "gamma"}}, nil
				case "two":
					return []world.Hit{{ID: "x", Title: "miss"}, {ID: "b", Text: "hit words"}}, nil
				default:
					t.Fatalf("unexpected query %q", q.Text)
					return nil, nil
				}
			},
			queries: []LabeledQuery{
				{Name: "one", Text: "one", Gold: map[string]bool{"a": true}, Limit: 1},
				{Name: "two", Text: "two", Gold: map[string]bool{"b": true}, Limit: 2},
			},
			storeSize: 9,
			want: SystemReport{
				Recall:         1,
				Precision:      0.75,
				F1:             5.0 / 6.0,
				MRR:            0.75,
				TokensPerQuery: 3,
				StoreSize:      9,
			},
		},
		{
			name: "rank error",
			rank: func(context.Context, world.Query) ([]world.Hit, error) {
				return nil, wantErr
			},
			queries: []LabeledQuery{{Name: "bad", Text: "bad"}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Run(context.Background(), tc.rank, tc.queries, tc.storeSize)
			if tc.wantErr {
				if err == nil {
					t.Fatal("Run() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			assertReport(t, got, tc.want)
		})
	}
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	const epsilon = 0.000001
	if got < want-epsilon || got > want+epsilon {
		t.Fatalf("%s = %.9f, want %.9f", name, got, want)
	}
}

func assertReport(t *testing.T, got, want SystemReport) {
	t.Helper()
	assertFloat(t, "Recall", got.Recall, want.Recall)
	assertFloat(t, "Precision", got.Precision, want.Precision)
	assertFloat(t, "F1", got.F1, want.F1)
	assertFloat(t, "MRR", got.MRR, want.MRR)
	assertFloat(t, "TokensPerQuery", got.TokensPerQuery, want.TokensPerQuery)
	if got.StoreSize != want.StoreSize {
		t.Fatalf("StoreSize = %d, want %d", got.StoreSize, want.StoreSize)
	}
}
