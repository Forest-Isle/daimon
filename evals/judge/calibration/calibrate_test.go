package calibration

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

// knownConfusion mirrors testdata/labels.example.jsonl: TP=10, TN=7, FP=1, FN=2.
var knownConfusion = Confusion{TP: 10, TN: 7, FP: 1, FN: 2}

func TestConfusionMetrics(t *testing.T) {
	c := knownConfusion
	if c.N() != 20 {
		t.Fatalf("N = %d, want 20", c.N())
	}
	if !approx(c.RawAgreement(), 0.85) {
		t.Fatalf("RawAgreement = %.4f, want 0.85", c.RawAgreement())
	}
	if !approx(c.TPR(), 10.0/12.0) {
		t.Fatalf("TPR = %.4f, want 0.8333", c.TPR())
	}
	if !approx(c.TNR(), 7.0/8.0) {
		t.Fatalf("TNR = %.4f, want 0.875", c.TNR())
	}
	if !approx(c.Kappa(), 0.6939) {
		t.Fatalf("Kappa = %.4f, want 0.6939", c.Kappa())
	}
}

func TestKappaDegenerate(t *testing.T) {
	// Single class, perfect agreement → kappa 1.
	if k := (Confusion{TP: 5}).Kappa(); !approx(k, 1) {
		t.Fatalf("all-pass perfect agreement kappa = %.4f, want 1", k)
	}
	// Single class but a disagreement → chance agreement near-total, kappa ~0.
	if k := (Confusion{TP: 9, FN: 1}).Kappa(); k > 0.001 {
		t.Fatalf("near-degenerate kappa = %.4f, want ~0", k)
	}
	// Empty set → 0, no divide-by-zero.
	if k := (Confusion{}).Kappa(); k != 0 {
		t.Fatalf("empty kappa = %.4f, want 0", k)
	}
}

func TestWilsonInterval(t *testing.T) {
	if got := WilsonInterval(0, 0, 1.96); got.Lo != 0 || got.Hi != 1 {
		t.Fatalf("n=0 interval = %+v, want [0,1]", got)
	}
	ci := WilsonInterval(17, 20, 1.96)
	if ci.Lo < 0 || ci.Hi > 1 {
		t.Fatalf("interval out of [0,1]: %+v", ci)
	}
	if !(ci.Lo < 0.85 && 0.85 < ci.Hi) {
		t.Fatalf("interval %+v should bracket the point estimate 0.85", ci)
	}
	// Perfect agreement: lower bound below 1, upper bound clamped to 1.
	full := WilsonInterval(10, 10, 1.96)
	if full.Hi != 1 || full.Lo >= 1 {
		t.Fatalf("perfect-agreement interval = %+v", full)
	}
}

func TestTabulate(t *testing.T) {
	labels := []Label{
		{Human: true, Judge: true},
		{Human: false, Judge: false},
		{Human: false, Judge: true},
		{Human: true, Judge: false},
	}
	c := Tabulate(labels)
	if c.TP != 1 || c.TN != 1 || c.FP != 1 || c.FN != 1 {
		t.Fatalf("tabulate wrong: %+v", c)
	}
}

func TestLoadAndAnalyze(t *testing.T) {
	labels, err := LoadLabels(filepath.Join("testdata", "labels.example.jsonl"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(labels) != 20 {
		t.Fatalf("loaded %d labels, want 20", len(labels))
	}
	r := Analyze(labels)
	if r.N != 20 || !approx(r.RawAgreement, 0.85) || !approx(r.Kappa, 0.6939) {
		t.Fatalf("analyze mismatch: %+v", r)
	}
	if !r.Meets(0.65) {
		t.Fatalf("kappa %.4f should meet 0.65 bar", r.Kappa)
	}
	if r.Meets(0.85) {
		t.Fatalf("kappa %.4f must NOT meet 0.85 bar (the misleading raw-agreement value)", r.Kappa)
	}
}

func TestLoadLabels_RejectsMissingVerdict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	// Second line omits "human" — must be rejected, not defaulted to false.
	content := `{"id":"a","human":true,"judge":true}
{"id":"b","judge":true}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLabels(path); err == nil {
		t.Fatal("missing human verdict must be a hard error")
	}
}

func TestFilter(t *testing.T) {
	labels := []Label{
		{ID: "a", Split: "test"},
		{ID: "b", Split: "train"},
		{ID: "c", Split: "test"},
	}
	if got := Filter(labels, "test"); len(got) != 2 {
		t.Fatalf("filter test = %d, want 2", len(got))
	}
	if got := Filter(labels, ""); len(got) != 3 {
		t.Fatalf("filter empty = %d, want all 3", len(got))
	}
}

// mockJudge passes iff the sample is exactly "good".
type mockJudge struct{}

func (mockJudge) Judge(_ context.Context, sample string) (bool, error) {
	return sample == "good", nil
}

func TestScoreItems(t *testing.T) {
	items := []Item{
		{ID: "1", Human: true, Sample: "good"},  // TP
		{ID: "2", Human: false, Sample: "bad"},  // TN
		{ID: "3", Human: false, Sample: "good"}, // FP (judge lenient)
		{ID: "4", Human: true, Sample: "bad"},   // FN (judge strict)
	}
	labels, err := ScoreItems(context.Background(), mockJudge{}, items)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	c := Tabulate(labels)
	if c.TP != 1 || c.TN != 1 || c.FP != 1 || c.FN != 1 {
		t.Fatalf("scored confusion wrong: %+v", c)
	}
}
