package main

import (
	"testing"

	"github.com/Forest-Isle/daimon/internal/economy"
	"github.com/Forest-Isle/daimon/internal/world"
)

// TestFoldClassCosts covers the per-class fold the by-class cost table builds on:
// summing tokens across a class's models, pricing each model at its own rate, and
// propagating "unpriced" when any of the class's models has no configured rate.
func TestFoldClassCosts(t *testing.T) {
	prices := economy.Prices{
		"opus":  {OutputPerMTok: 75},
		"haiku": {OutputPerMTok: 5},
	}
	rows := []economy.ClassModelTotals{
		{Class: "chat", Model: "opus", Totals: economy.Totals{Episodes: 1, OutputTokens: 1_000_000}},
		{Class: "chat", Model: "haiku", Totals: economy.Totals{Episodes: 2, OutputTokens: 1_000_000}},
		{Class: "heartbeat", Model: "opus", Totals: economy.Totals{Episodes: 1, OutputTokens: 1_000_000}},
		{Class: "heartbeat", Model: "gpt", Totals: economy.Totals{Episodes: 1, OutputTokens: 500_000}}, // unpriced
	}

	out := foldClassCosts(rows, prices)
	if len(out) != 2 {
		t.Fatalf("want 2 classes, got %d: %+v", len(out), out)
	}
	// chat output 2M > heartbeat output 1.5M ⇒ chat first (output desc).
	if out[0].class != "chat" || out[1].class != "heartbeat" {
		t.Fatalf("order = %q,%q, want chat,heartbeat", out[0].class, out[1].class)
	}
	// chat fully priced: opus 1M@75 + haiku 1M@5 = $80; tokens summed across models.
	if out[0].anyUnpriced {
		t.Fatal("chat is fully priced, must not be marked unpriced")
	}
	if out[0].usd != 80 {
		t.Fatalf("chat usd = %v, want 80", out[0].usd)
	}
	if out[0].totals.Episodes != 3 || out[0].totals.OutputTokens != 2_000_000 {
		t.Fatalf("chat totals = %+v", out[0].totals)
	}
	// heartbeat has an unpriced model (gpt) ⇒ incomplete ⇒ anyUnpriced. The unpriced
	// model's tokens are still folded into the class totals, and the priced model's
	// dollars still accumulate (opus 1M@75 = $75; gpt contributes tokens, no $).
	if !out[1].anyUnpriced {
		t.Fatal("heartbeat has an unpriced model, must be marked unpriced")
	}
	if out[1].totals.Episodes != 2 || out[1].totals.OutputTokens != 1_500_000 {
		t.Fatalf("heartbeat totals = %+v (unpriced model's tokens must still fold in)", out[1].totals)
	}
	if out[1].usd != 75 {
		t.Fatalf("heartbeat usd = %v, want 75 (priced model still accumulates)", out[1].usd)
	}
}

// TestFoldClassCostsDeterministicTieBreak verifies that classes with equal output
// tokens are ordered by class name (not Go's random map iteration), and that an
// empty price table marks every class unpriced.
func TestFoldClassCostsDeterministicTieBreak(t *testing.T) {
	rows := []economy.ClassModelTotals{
		{Class: "zeta", Model: "m", Totals: economy.Totals{Episodes: 1, OutputTokens: 100}},
		{Class: "alpha", Model: "m", Totals: economy.Totals{Episodes: 1, OutputTokens: 100}},
	}
	out := foldClassCosts(rows, economy.Prices{}) // empty prices ⇒ all unpriced
	if len(out) != 2 {
		t.Fatalf("want 2 classes, got %d", len(out))
	}
	if out[0].class != "alpha" || out[1].class != "zeta" {
		t.Fatalf("tie-break order = %q,%q, want alpha,zeta", out[0].class, out[1].class)
	}
	if !out[0].anyUnpriced || !out[1].anyUnpriced {
		t.Fatal("empty prices must mark every class unpriced")
	}
	if out[0].usd != 0 || out[1].usd != 0 {
		t.Fatalf("unpriced classes usd = %v,%v, want 0,0", out[0].usd, out[1].usd)
	}
}

// TestFoldROI covers the ROI overlay: clean-outcome counts are bucketed per class
// from each episode's quality, layered onto the folded cost spine (which carries
// episodes, dollars, and order). Episodes with no quality entry count toward
// episodes but not clean; an unpriced class is marked not-priced.
func TestFoldROI(t *testing.T) {
	folded := []classCostRow{
		{class: "chat", totals: economy.Totals{Episodes: 3}, usd: 80, anyUnpriced: false},
		{class: "heartbeat", totals: economy.Totals{Episodes: 2}, usd: 0, anyUnpriced: true},
	}
	episodeCosts := []economy.EpisodeClassCost{
		{EpisodeID: "c1", Class: "chat"},
		{EpisodeID: "c2", Class: "chat"},
		{EpisodeID: "c3", Class: "chat"}, // no quality entry → counts as not-clean
		{EpisodeID: "h1", Class: "heartbeat"},
		{EpisodeID: "h2", Class: "heartbeat"},
	}
	quality := map[string]world.OutcomeQuality{
		"c1": world.OutcomeClean,
		"c2": world.OutcomeClean,
		// c3 absent (no outcome row)
		"h1": world.OutcomeClean,
		"h2": world.OutcomeToolFailures,
	}

	out := foldROI(folded, episodeCosts, quality)
	if len(out) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(out), out)
	}
	// Order follows the folded spine (chat first).
	if out[0].class != "chat" || out[1].class != "heartbeat" {
		t.Fatalf("order = %q,%q, want chat,heartbeat", out[0].class, out[1].class)
	}
	// chat: 3 episodes, 2 clean (c3 has no outcome → not clean), priced $80.
	if out[0].episodes != 3 || out[0].clean != 2 || out[0].usd != 80 || !out[0].priced {
		t.Fatalf("chat row = %+v, want episodes=3 clean=2 usd=80 priced", out[0])
	}
	// heartbeat: 2 episodes, 1 clean (h2 is a tool-failure), unpriced.
	if out[1].episodes != 2 || out[1].clean != 1 || out[1].priced {
		t.Fatalf("heartbeat row = %+v, want episodes=2 clean=1 not-priced", out[1])
	}
}
