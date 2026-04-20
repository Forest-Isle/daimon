package eval

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAdaptiveGenerator_NilProvider(t *testing.T) {
	g := NewAdaptiveGenerator(nil)
	_, err := g.Generate(context.Background(), &WeaknessReport{
		Weaknesses: []Weakness{{ID: "W-001"}},
	}, 2)
	if err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestAdaptiveGenerator_NoWeaknesses(t *testing.T) {
	g := NewAdaptiveGenerator(nil)
	tasks, err := g.Generate(context.Background(), &WeaknessReport{}, 2)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestAdaptiveGenerator_NilReport(t *testing.T) {
	g := NewAdaptiveGenerator(nil)
	tasks, err := g.Generate(context.Background(), nil, 2)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if tasks != nil {
		t.Errorf("expected nil tasks")
	}
}

func TestParseTasks_ValidJSON(t *testing.T) {
	g := &AdaptiveGenerator{}
	w := Weakness{ID: "W-001", Dimension: DimPlanning, Category: FailPlanningError}

	jsonInput := `[
		{
			"id": "adaptive-plan-1",
			"goal": "Test planning",
			"complexity": "complex",
			"tags": ["adaptive", "planning"],
			"expect_tools": ["bash"],
			"dimension": "planning",
			"verify_method": "hybrid",
			"must_contain": ["result"],
			"rationale": "Tests planning weakness"
		}
	]`

	tasks, err := g.parseTasks(jsonInput, w)
	if err != nil {
		t.Fatalf("parseTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "adaptive-plan-1" {
		t.Errorf("expected adaptive-plan-1, got %s", tasks[0].ID)
	}
	if tasks[0].TargetWeakness != "W-001" {
		t.Errorf("expected W-001, got %s", tasks[0].TargetWeakness)
	}
	if tasks[0].Reference == nil {
		t.Error("expected reference with MustContain")
	}
}

func TestParseTasks_MarkdownWrapped(t *testing.T) {
	g := &AdaptiveGenerator{}
	w := Weakness{ID: "W-002", Dimension: DimErrorRecovery}

	input := "```json\n[{\"id\": \"adaptive-err-1\", \"goal\": \"Test error\", \"complexity\": \"moderate\"}]\n```"

	tasks, err := g.parseTasks(input, w)
	if err != nil {
		t.Fatalf("parseTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Dimension != DimErrorRecovery {
		t.Errorf("expected error_recovery dimension, got %s", tasks[0].Dimension)
	}
}

func TestParseTasks_InvalidJSON(t *testing.T) {
	g := &AdaptiveGenerator{}
	w := Weakness{ID: "W-003"}

	_, err := g.parseTasks("not json at all", w)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRoundSnapshot(t *testing.T) {
	snap := RoundSnapshot{
		Round:        1,
		RunID:        "test-r1",
		TaskCount:    10,
		OverallScore: 0.75,
		FailedTasks:  3,
		Timestamp:    time.Now(),
	}
	if snap.Round != 1 {
		t.Error("wrong round")
	}
}

func TestAdaptiveSummary_FormatMarkdown(t *testing.T) {
	summary := &AdaptiveSummary{
		Rounds: []RoundSnapshot{
			{Round: 1, TaskCount: 10, OverallScore: 0.6, FailedTasks: 4, WeaknessCount: 5, GeneratedCount: 6},
			{Round: 2, TaskCount: 16, OverallScore: 0.7, FailedTasks: 3, WeaknessCount: 3, GeneratedCount: 4},
		},
		Converging:  []string{"task_execution"},
		Diverging:   []string{"planning"},
		GeneratedAt: time.Now(),
	}

	md := summary.FormatMarkdown()
	if md == "" {
		t.Fatal("empty markdown")
	}
	if !strings.Contains(md, "Adaptive Evaluation Summary") {
		t.Error("missing title")
	}
	if !strings.Contains(md, "Improving") {
		t.Error("missing converging section")
	}
	if !strings.Contains(md, "Declining") {
		t.Error("missing diverging section")
	}
}
