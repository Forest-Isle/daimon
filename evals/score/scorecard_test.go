package score

import (
	"path/filepath"
	"strings"
	"testing"
)

func sample() Scorecard {
	return Scorecard{
		Sessions:         52,
		ToolCalls:        95,
		Failures:         39,
		GovernanceDenied: 33,
		AgentError:       5,
		EnvError:         1,
		Salvaged:         1,
		DeniedByTool:     map[string]int{"memory": 26, "bash": 4, "http": 2, "values": 1},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last.json")

	if _, existed, err := Load(path); err != nil || existed {
		t.Fatalf("missing file should be (zero,false,nil): existed=%v err=%v", existed, err)
	}

	want := sample()
	if err := Save(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, existed, err := Load(path)
	if err != nil || !existed {
		t.Fatalf("load after save: existed=%v err=%v", existed, err)
	}
	if got.Failures != want.Failures || got.GovernanceDenied != want.GovernanceDenied {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
	if got.DeniedByTool["memory"] != 26 {
		t.Fatalf("map not persisted: %+v", got.DeniedByTool)
	}
}

func TestRender_NoBaseline(t *testing.T) {
	var b strings.Builder
	Render(&b, sample(), nil)
	out := b.String()
	if !strings.Contains(out, "governance_denied") || !strings.Contains(out, "33") {
		t.Fatalf("table missing core metric:\n%s", out)
	}
	// No baseline → Δ column is "-".
	if !strings.Contains(out, "\t-") && !strings.Contains(out, " -") {
		t.Fatalf("expected '-' deltas without baseline:\n%s", out)
	}
	if !strings.Contains(out, "memory") {
		t.Fatalf("FM-1 by-tool breakdown missing:\n%s", out)
	}
}

func TestRender_WithDelta(t *testing.T) {
	prev := sample()
	cur := sample()
	cur.AgentError = 8        // +3
	cur.GovernanceDenied = 30 // -3

	var b strings.Builder
	Render(&b, cur, &prev)
	out := b.String()
	if !strings.Contains(out, "+3") {
		t.Fatalf("expected +3 delta for agent_error:\n%s", out)
	}
	if !strings.Contains(out, "-3") {
		t.Fatalf("expected -3 delta for governance_denied:\n%s", out)
	}
}

func TestSortedCounts_Deterministic(t *testing.T) {
	got := sortedCounts(map[string]int{"a": 1, "b": 2, "c": 2})
	// descending by count, ties broken by key asc → b(2), c(2), a(1)
	if got[0].key != "b" || got[1].key != "c" || got[2].key != "a" {
		t.Fatalf("ordering wrong: %+v", got)
	}
}
