package main

import (
	"path/filepath"
	"testing"
)

func labelsFixture() string {
	return filepath.Join("..", "..", "judge", "calibration", "testdata", "labels.example.jsonl")
}

func TestRun_MissingLabels(t *testing.T) {
	if code, err := run("", "", 0.6, false); code != 2 || err == nil {
		t.Fatalf("missing -labels should be (2, err): code=%d err=%v", code, err)
	}
}

func TestRun_GatePassesBelowKappa(t *testing.T) {
	// kappa for the fixture is ~0.694: meets 0.65, exits 0.
	code, err := run(labelsFixture(), "test", 0.65, true)
	if err != nil || code != 0 {
		t.Fatalf("kappa 0.694 vs 0.65 gate: code=%d err=%v, want 0/nil", code, err)
	}
}

func TestRun_GateFailsAboveKappa(t *testing.T) {
	// 0.85 threshold is the misleading raw-agreement value; kappa misses it.
	code, err := run(labelsFixture(), "test", 0.85, true)
	if err != nil || code != 1 {
		t.Fatalf("kappa 0.694 vs 0.85 gate: code=%d err=%v, want 1/nil", code, err)
	}
}

func TestRun_NoGateAlwaysZero(t *testing.T) {
	// Without -gate, a low kappa still exits 0 (report-only).
	code, err := run(labelsFixture(), "test", 0.99, false)
	if err != nil || code != 0 {
		t.Fatalf("report-only should exit 0: code=%d err=%v", code, err)
	}
}
