package calibration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Label is one calibration sample: the human ground-truth verdict and the
// judge's verdict for the same item (true = "pass"). Split optionally marks
// train/dev/test membership.
type Label struct {
	ID    string `json:"id"`
	Human bool   `json:"human"`
	Judge bool   `json:"judge"`
	Split string `json:"split,omitempty"`
}

// Tabulate folds labels into a confusion matrix ("pass" is positive).
func Tabulate(labels []Label) Confusion {
	var c Confusion
	for _, l := range labels {
		switch {
		case l.Human && l.Judge:
			c.TP++
		case !l.Human && !l.Judge:
			c.TN++
		case !l.Human && l.Judge:
			c.FP++
		default: // l.Human && !l.Judge
			c.FN++
		}
	}
	return c
}

// LoadLabels reads a JSONL label file (one Label per line). Blank lines are
// skipped; a malformed line is a hard error so a typo never silently shrinks the
// calibration set.
func LoadLabels(path string) ([]Label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open labels: %w", err)
	}
	defer f.Close()

	var labels []Label
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var l Label
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			return nil, fmt.Errorf("labels line %d: %w", line, err)
		}
		labels = append(labels, l)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read labels: %w", err)
	}
	return labels, nil
}

// Filter returns the labels in the named split (e.g. "test"). An empty split
// returns all labels.
func Filter(labels []Label, split string) []Label {
	if split == "" {
		return labels
	}
	var out []Label
	for _, l := range labels {
		if l.Split == split {
			out = append(out, l)
		}
	}
	return out
}

// BinaryJudge is any judge that renders a pass/fail verdict on a sample. The
// production judge is the existing replay.Judge (an LLM call); tests use a
// deterministic stand-in so the score loop is verifiable without a provider.
type BinaryJudge interface {
	Judge(ctx context.Context, sample string) (bool, error)
}

// Item is a calibration sample carrying its human verdict and the payload the
// judge will score.
type Item struct {
	ID     string
	Human  bool
	Sample string
}

// ScoreItems runs the judge over each item and pairs its verdict with the
// human label, producing the Label set the calibration metrics consume. A judge
// error aborts so a partial run never masquerades as a full calibration.
func ScoreItems(ctx context.Context, judge BinaryJudge, items []Item) ([]Label, error) {
	out := make([]Label, 0, len(items))
	for _, it := range items {
		verdict, err := judge.Judge(ctx, it.Sample)
		if err != nil {
			return nil, fmt.Errorf("judge item %s: %w", it.ID, err)
		}
		out = append(out, Label{ID: it.ID, Human: it.Human, Judge: verdict})
	}
	return out, nil
}
