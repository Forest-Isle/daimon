package retrievaleval

import (
	"context"
	"fmt"
	"strings"

	"github.com/Forest-Isle/daimon/internal/world"
)

// LabeledQuery is one deterministic retrieval evaluation case.
type LabeledQuery struct {
	Name  string
	Text  string
	Gold  map[string]bool
	Limit int
}

// SystemReport summarizes retrieval quality over a labeled query set.
type SystemReport struct {
	Recall         float64
	Precision      float64
	F1             float64
	MRR            float64
	TokensPerQuery float64
	StoreSize      int
}

// Ranker retrieves world hits for a query.
type Ranker func(ctx context.Context, q world.Query) ([]world.Hit, error)

// Metrics computes deterministic lexical retrieval metrics against gold hit IDs.
func Metrics(hits []world.Hit, gold map[string]bool) (recall, precision, f1, mrr float64) {
	hitGold := 0
	firstRank := 0
	for i, hit := range hits {
		if gold[hit.ID] {
			hitGold++
			if firstRank == 0 {
				firstRank = i + 1
			}
		}
	}
	if len(gold) > 0 {
		recall = float64(hitGold) / float64(len(gold))
	}
	if len(hits) > 0 {
		precision = float64(hitGold) / float64(len(hits))
	}
	if recall+precision > 0 {
		f1 = 2 * recall * precision / (recall + precision)
	}
	if firstRank > 0 {
		mrr = 1 / float64(firstRank)
	}
	return recall, precision, f1, mrr
}

// Run evaluates a ranker over labeled queries and returns arithmetic mean metrics.
func Run(ctx context.Context, rank Ranker, qs []LabeledQuery, storeSize int) (SystemReport, error) {
	var report SystemReport
	report.StoreSize = storeSize
	if len(qs) == 0 {
		return report, nil
	}

	for _, q := range qs {
		hits, err := rank(ctx, world.Query{Text: q.Text, Limit: q.Limit})
		if err != nil {
			return SystemReport{}, fmt.Errorf("rank query %q: %w", q.Name, err)
		}
		recall, precision, f1, mrr := Metrics(hits, q.Gold)
		report.Recall += recall
		report.Precision += precision
		report.F1 += f1
		report.MRR += mrr

		var tokens int
		for _, hit := range hits {
			tokens += estimateTokens(hit.Title + " " + hit.Text)
		}
		report.TokensPerQuery += float64(tokens)
	}

	n := float64(len(qs))
	report.Recall /= n
	report.Precision /= n
	report.F1 /= n
	report.MRR /= n
	report.TokensPerQuery /= n
	return report, nil
}

// estimateTokens approximates token load with whitespace fields; the estimate is
// crude, but consistent across systems, so relative comparisons remain useful.
func estimateTokens(s string) int {
	return len(strings.Fields(s))
}
