package eval

import (
	"testing"
)

func TestEvolutionTasks_ComplexityLevels(t *testing.T) {
	tasks := EvolutionTasks()
	complexities := make(map[string]int)
	for _, task := range tasks {
		complexities[task.Complexity]++
	}
	if complexities["simple"] == 0 {
		t.Error("expected at least one simple task")
	}
	if complexities["moderate"] == 0 {
		t.Error("expected at least one moderate task")
	}
	if complexities["complex"] == 0 {
		t.Error("expected at least one complex task")
	}
}

func TestEvolutionTasks_AllHaveIDsAndGoals(t *testing.T) {
	for _, task := range EvolutionTasks() {
		if task.ID == "" {
			t.Errorf("task missing ID: %+v", task)
		}
		if task.Goal == "" {
			t.Errorf("task %q missing Goal", task.ID)
		}
	}
}

func TestEvalResult_RoutedModel(t *testing.T) {
	r := &EvalResult{RoutedModel: "claude-haiku"}
	if r.RoutedModel == "" {
		t.Error("RoutedModel should be set")
	}
}

func TestEvalResult_RoutedModel_Empty(t *testing.T) {
	r := &EvalResult{}
	if r.RoutedModel != "" {
		t.Errorf("RoutedModel should be empty by default, got %q", r.RoutedModel)
	}
}

func TestAggregateRouterDecisions(t *testing.T) {
	results := []EvalResult{
		{RoutedModel: "claude-haiku"},
		{RoutedModel: "claude-haiku"},
		{RoutedModel: "claude-sonnet"},
		{RoutedModel: ""},
	}
	decisions := aggregateRouterDecisions(results)
	if decisions["claude-haiku"] != 2 {
		t.Errorf("expected claude-haiku=2, got %d", decisions["claude-haiku"])
	}
	if decisions["claude-sonnet"] != 1 {
		t.Errorf("expected claude-sonnet=1, got %d", decisions["claude-sonnet"])
	}
	if _, ok := decisions[""]; ok {
		t.Error("empty RoutedModel should not appear in decisions map")
	}
}

func TestSuiteSummary_RouterDecisions(t *testing.T) {
	suite := &SuiteResult{
		Results: []EvalResult{
			{RoutedModel: "model-a", Success: true},
			{RoutedModel: "model-b", Success: false},
			{RoutedModel: "model-a", Success: true},
		},
	}
	sum := suite.Summary()
	if sum.RouterDecisions["model-a"] != 2 {
		t.Errorf("expected model-a=2, got %d", sum.RouterDecisions["model-a"])
	}
	if sum.RouterDecisions["model-b"] != 1 {
		t.Errorf("expected model-b=1, got %d", sum.RouterDecisions["model-b"])
	}
}

func TestEvolutionSnapshot_RouterDecisions(t *testing.T) {
	snap := &EvolutionSnapshot{
		RouterDecisions: map[string]int{
			"claude-haiku":  3,
			"claude-sonnet": 1,
		},
	}
	if snap.RouterDecisions["claude-haiku"] != 3 {
		t.Errorf("unexpected count for claude-haiku: %d", snap.RouterDecisions["claude-haiku"])
	}
}

func TestEvoSnapshotDiff_RouterModelChanges(t *testing.T) {
	before := &SuiteResult{
		EvoAfter: &EvolutionSnapshot{
			RouterDecisions: map[string]int{"model-a": 2, "model-b": 1},
		},
	}
	after := &SuiteResult{
		EvoAfter: &EvolutionSnapshot{
			RouterDecisions: map[string]int{"model-a": 3, "model-b": 1, "model-c": 2},
		},
	}

	report := Compare(before, after)
	if report.EvoSnapshot == nil {
		t.Fatal("expected EvoSnapshot to be populated")
	}
	changes := report.EvoSnapshot.RouterModelChanges
	if changes["model-a"] != 1 {
		t.Errorf("expected model-a delta=+1, got %d", changes["model-a"])
	}
	if _, ok := changes["model-b"]; ok {
		t.Error("model-b had no change, should not appear in RouterModelChanges")
	}
	if changes["model-c"] != 2 {
		t.Errorf("expected model-c delta=+2, got %d", changes["model-c"])
	}
}
