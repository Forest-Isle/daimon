package evolution

import (
	"strings"
	"testing"
)

func TestModelRouter_Disabled(t *testing.T) {
	mr := NewModelRouter(RouterConfig{Enabled: false})
	rr := mr.SelectModel("simple")
	if rr.Routed {
		t.Error("expected not routed when disabled")
	}
}

func TestModelRouter_SelectModel(t *testing.T) {
	cfg := RouterConfig{
		Enabled:  true,
		Simple:   ModelRoute{Model: "cheap-model", MaxTokens: 2048},
		Moderate: ModelRoute{Model: ""},
		Complex:  ModelRoute{Model: "strong-model"},
	}
	mr := NewModelRouter(cfg)

	rr := mr.SelectModel("simple")
	if !rr.Routed {
		t.Fatal("expected routed for simple")
	}
	if rr.Model != "cheap-model" {
		t.Errorf("model = %q, want cheap-model", rr.Model)
	}
	if rr.MaxTokens != 2048 {
		t.Errorf("max_tokens = %d, want 2048", rr.MaxTokens)
	}

	rr = mr.SelectModel("moderate")
	if rr.Routed {
		t.Error("expected not routed for moderate (empty model)")
	}

	rr = mr.SelectModel("complex")
	if !rr.Routed || rr.Model != "strong-model" {
		t.Errorf("complex route: routed=%v model=%q", rr.Routed, rr.Model)
	}

	rr = mr.SelectModel("unknown")
	if rr.Routed {
		t.Error("expected not routed for unknown complexity")
	}
}

func TestModelRouter_RecordOutcome(t *testing.T) {
	cfg := RouterConfig{
		Enabled: true,
		Simple:  ModelRoute{Model: "cheap"},
	}
	mr := NewModelRouter(cfg)

	mr.SelectModel("simple")
	mr.SelectModel("simple")
	mr.RecordOutcome("simple", true)
	mr.RecordOutcome("simple", false)

	stats := mr.Stats()
	if !strings.Contains(stats, "simple") {
		t.Error("stats should mention simple")
	}
	if !strings.Contains(stats, "2 uses") {
		t.Errorf("stats should show 2 uses, got:\n%s", stats)
	}
}

func TestModelRouter_StatsEmpty(t *testing.T) {
	mr := NewModelRouter(RouterConfig{Enabled: true})
	stats := mr.Stats()
	if !strings.Contains(stats, "no routing data") {
		t.Errorf("expected 'no routing data', got: %s", stats)
	}
}
