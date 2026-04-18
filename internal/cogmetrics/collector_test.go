package cogmetrics

import (
	"context"
	"math"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

func TestRollingAvg_Basic(t *testing.T) {
	ra := NewRollingAvg(3)
	ra.Add(1.0)
	ra.Add(2.0)
	ra.Add(3.0)
	if got := ra.Avg(); math.Abs(got-2.0) > 1e-9 {
		t.Errorf("avg = %f, want 2.0", got)
	}
	if ra.Count() != 3 {
		t.Errorf("count = %d, want 3", ra.Count())
	}
}

func TestRollingAvg_WindowOverflow(t *testing.T) {
	ra := NewRollingAvg(3)
	ra.Add(10.0)
	ra.Add(20.0)
	ra.Add(30.0)
	ra.Add(40.0) // pushes out 10.0

	if got := ra.Avg(); math.Abs(got-30.0) > 1e-9 {
		t.Errorf("avg = %f, want 30.0 (20+30+40)/3", got)
	}
	if ra.Count() != 3 {
		t.Errorf("count should be capped at 3, got %d", ra.Count())
	}
}

func TestRollingAvg_Empty(t *testing.T) {
	ra := NewRollingAvg(10)
	if ra.Avg() != 0 {
		t.Errorf("empty avg should be 0, got %f", ra.Avg())
	}
}

func TestCollector_OnReflectionComplete(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	c.OnReflectionComplete(ctx, evolution.ReflectionEvent{
		Complexity: "complex",
		Succeeded:  true,
		Confidence: 0.85,
	})
	c.OnReflectionComplete(ctx, evolution.ReflectionEvent{
		Complexity: "complex",
		Succeeded:  false,
		Confidence: 0.35,
	})

	snap := c.Snapshot()
	if snap.TotalReflections != 2 {
		t.Errorf("reflections = %d, want 2", snap.TotalReflections)
	}
	if math.Abs(snap.AvgConfidence.Value-0.6) > 1e-9 {
		t.Errorf("avg_confidence = %f, want 0.6", snap.AvgConfidence.Value)
	}
	if cs, ok := snap.ComplexitySuccess["complex"]; !ok || cs.Samples != 2 {
		t.Errorf("complex samples = %v, want 2", snap.ComplexitySuccess["complex"])
	}
}

func TestCollector_OnEpisodeComplete(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	c.OnEpisodeComplete(ctx, evolution.EpisodeEvent{Succeeded: true, ReplanCount: 0})
	c.OnEpisodeComplete(ctx, evolution.EpisodeEvent{Succeeded: true, ReplanCount: 2})
	c.OnEpisodeComplete(ctx, evolution.EpisodeEvent{Succeeded: false, ReplanCount: 1})

	snap := c.Snapshot()
	if snap.TotalEpisodes != 3 {
		t.Errorf("episodes = %d, want 3", snap.TotalEpisodes)
	}

	expectedReplanRate := 2.0 / 3.0
	if math.Abs(snap.ReplanRate.Value-expectedReplanRate) > 1e-9 {
		t.Errorf("replan_rate = %f, want %f", snap.ReplanRate.Value, expectedReplanRate)
	}

	if snap.ReplanEfficiency.WithoutReplan.Value != 1.0 {
		t.Errorf("no-replan success = %f, want 1.0", snap.ReplanEfficiency.WithoutReplan.Value)
	}
}

func TestCollector_OnToolExecuted(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	c.OnToolExecuted(ctx, evolution.ToolExecEvent{ToolName: "bash", Succeeded: true})
	c.OnToolExecuted(ctx, evolution.ToolExecEvent{ToolName: "bash", Succeeded: true})
	c.OnToolExecuted(ctx, evolution.ToolExecEvent{ToolName: "bash", Succeeded: false})
	c.OnToolExecuted(ctx, evolution.ToolExecEvent{ToolName: "http", Succeeded: true})

	snap := c.Snapshot()

	bashRel, ok := snap.ToolReliability["bash"]
	if !ok {
		t.Fatal("bash not in tool reliability")
	}
	if bashRel.Samples != 3 {
		t.Errorf("bash samples = %d, want 3", bashRel.Samples)
	}
	expected := 2.0 / 3.0
	if math.Abs(bashRel.Value-expected) > 1e-9 {
		t.Errorf("bash reliability = %f, want %f", bashRel.Value, expected)
	}
}

func TestCollector_RecordAssertionRate(t *testing.T) {
	c := NewCollector()
	c.RecordAssertionRate(1.0)
	c.RecordAssertionRate(0.5)

	snap := c.Snapshot()
	if math.Abs(snap.AssertionPassRate.Value-0.75) > 1e-9 {
		t.Errorf("assertion_pass_rate = %f, want 0.75", snap.AssertionPassRate.Value)
	}
}

func TestCollector_FormatMarkdown(t *testing.T) {
	c := NewCollector()
	ctx := context.Background()

	c.OnEpisodeComplete(ctx, evolution.EpisodeEvent{Succeeded: true})
	c.OnToolExecuted(ctx, evolution.ToolExecEvent{ToolName: "bash", Succeeded: true})

	snap := c.Snapshot()
	md := snap.FormatMarkdown()
	if md == "" {
		t.Error("expected non-empty markdown")
	}
}

func TestCollector_FormatJSON(t *testing.T) {
	c := NewCollector()
	snap := c.Snapshot()
	js, err := snap.FormatJSON()
	if err != nil {
		t.Fatal(err)
	}
	if js == "" {
		t.Error("expected non-empty JSON")
	}
}
