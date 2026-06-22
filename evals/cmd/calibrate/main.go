// Command calibrate scores an LLM judge against human ground-truth labels and
// prints the confusion matrix, TPR/TNR, raw agreement (with CI), and Cohen's
// kappa. It is the runnable face of Phase-1's second acceptance criterion. With
// -gate it exits non-zero when kappa is below the trust threshold.
//
// Labels are a JSONL file of {"id","human","judge","split"} produced by the
// operator: human is the ground truth; judge is the verdict from the judge
// under test (e.g. the existing replay.Judge run offline).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Forest-Isle/daimon/evals/judge/calibration"
)

func main() {
	var (
		labelsPath string
		split      string
		kappaMin   float64
		gate       bool
	)
	flag.StringVar(&labelsPath, "labels", "", "JSONL labels file (required)")
	flag.StringVar(&split, "split", "", "only score this split (e.g. test); empty = all")
	flag.Float64Var(&kappaMin, "kappa", 0.6, "minimum acceptable Cohen's kappa")
	flag.BoolVar(&gate, "gate", false, "exit non-zero if kappa is below -kappa")
	flag.Parse()

	code, err := run(labelsPath, split, kappaMin, gate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "calibrate:", err)
	}
	os.Exit(code)
}

// run loads labels, computes the calibration report, and returns the exit code.
func run(labelsPath, split string, kappaMin float64, gate bool) (int, error) {
	if labelsPath == "" {
		return 2, fmt.Errorf("-labels is required")
	}
	labels, err := calibration.LoadLabels(labelsPath)
	if err != nil {
		return 2, err
	}
	labels = calibration.Filter(labels, split)
	if len(labels) == 0 {
		return 2, fmt.Errorf("no labels to score (split=%q)", split)
	}

	r := calibration.Analyze(labels)
	r.Render(os.Stdout)

	meets := r.Meets(kappaMin)
	fmt.Printf("kappa >= %.2f   %v\n", kappaMin, meets)
	if gate && !meets {
		return 1, nil
	}
	return 0, nil
}
