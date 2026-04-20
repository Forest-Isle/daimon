package eval

import (
	"context"
	"testing"
	"time"
)

func TestDiagnose_Basic(t *testing.T) {
	suite := &SuiteResult{
		RunID: "test-run",
		Results: []EvalResult{
			{TaskID: "t1", Dimension: DimPlanning, Success: true, FinalScore: 0.9},
			{TaskID: "t2", Dimension: DimPlanning, Success: false, FinalScore: 0.3, FailureCategory: "planning_error"},
			{TaskID: "t3", Dimension: DimErrorRecovery, Success: false, FinalScore: 0.4, FailureCategory: "error_no_recovery"},
			{TaskID: "t4", Dimension: DimTaskExecution, Success: true, FinalScore: 0.95},
		},
	}

	report := Diagnose(context.Background(), suite, nil)

	if report == nil {
		t.Fatal("report is nil")
	}
	if report.TotalTasks != 4 {
		t.Errorf("expected 4 total tasks, got %d", report.TotalTasks)
	}
	if report.FailedTasks != 2 {
		t.Errorf("expected 2 failed tasks, got %d", report.FailedTasks)
	}
	if report.DimReport == nil {
		t.Fatal("dimension report is nil")
	}
	if len(report.DimReport.Dimensions) < 2 {
		t.Errorf("expected at least 2 dimensions, got %d", len(report.DimReport.Dimensions))
	}
}

func TestDiagnose_WithClassifier(t *testing.T) {
	classifier := NewFailureClassifier(nil, 1*time.Minute)
	tasks := []TaskCase{
		{ID: "t1", Dimension: DimPlanning, Complexity: "complex"},
		{ID: "t2", Dimension: DimErrorRecovery},
	}
	suite := &SuiteResult{
		RunID: "test-run",
		Results: []EvalResult{
			{TaskID: "t1", Dimension: DimPlanning, FinalScore: 0.2},
			{TaskID: "t2", Dimension: DimErrorRecovery, FinalScore: 0.3, Error: "failed", ReplanCount: 0},
		},
	}

	report := Diagnose(context.Background(), suite, &DiagnoseOptions{
		Classifier: classifier,
		Tasks:      tasks,
	})

	if report == nil {
		t.Fatal("report is nil")
	}
	if len(report.Weaknesses) == 0 {
		t.Error("expected weaknesses to be identified")
	}
}

func TestDiagnose_Weaknesses_SortedBySeverity(t *testing.T) {
	suite := &SuiteResult{
		RunID: "test-run",
		Results: []EvalResult{
			{TaskID: "t1", Dimension: DimPlanning, FinalScore: 0.2, FailureCategory: "planning_error"},
			{TaskID: "t2", Dimension: DimPlanning, FinalScore: 0.3, FailureCategory: "planning_error"},
			{TaskID: "t3", Dimension: DimPlanning, FinalScore: 0.1, FailureCategory: "planning_error"},
			{TaskID: "t4", Dimension: DimErrorRecovery, FinalScore: 0.4, FailureCategory: "error_no_recovery"},
		},
	}

	report := Diagnose(context.Background(), suite, nil)

	if len(report.Weaknesses) == 0 {
		t.Fatal("expected weaknesses")
	}
	if report.Weaknesses[0].Severity != "critical" {
		t.Errorf("first weakness should be critical, got %s", report.Weaknesses[0].Severity)
	}
}

func TestDiagnose_Recommendations(t *testing.T) {
	suite := &SuiteResult{
		RunID: "test-run",
		Results: []EvalResult{
			{TaskID: "t1", Dimension: DimPlanning, FinalScore: 0.3, FailureCategory: "planning_error"},
			{TaskID: "t2", Dimension: DimErrorRecovery, FinalScore: 0.4, FailureCategory: "error_loop_retry"},
		},
	}

	report := Diagnose(context.Background(), suite, nil)

	if len(report.Recommendations) == 0 {
		t.Fatal("expected recommendations")
	}
	for _, rec := range report.Recommendations {
		if rec.Action == "" || rec.Component == "" {
			t.Errorf("recommendation missing action or component: %+v", rec)
		}
	}
}

func TestDiagnose_FormatMarkdown(t *testing.T) {
	suite := &SuiteResult{
		RunID: "test-run",
		Results: []EvalResult{
			{TaskID: "t1", Dimension: DimPlanning, Success: true, FinalScore: 0.9},
			{TaskID: "t2", Dimension: DimPlanning, FinalScore: 0.3, FailureCategory: "planning_error"},
			{TaskID: "t3", Dimension: DimErrorRecovery, FinalScore: 0.4, FailureCategory: "error_no_recovery"},
		},
	}

	report := Diagnose(context.Background(), suite, nil)
	md := report.FormatMarkdown()

	if md == "" {
		t.Fatal("markdown output is empty")
	}
	if !contains(md, "Weakness Diagnosis Report") {
		t.Error("missing report title")
	}
	if !contains(md, "Dimension Scores") {
		t.Error("missing dimension scores section")
	}
	if !contains(md, "Weaknesses") {
		t.Error("missing weaknesses section")
	}
	if !contains(md, "Optimization Recommendations") {
		t.Error("missing recommendations section")
	}
}

func TestDiagnose_EmptyResults(t *testing.T) {
	suite := &SuiteResult{RunID: "empty"}
	report := Diagnose(context.Background(), suite, nil)
	if report == nil {
		t.Fatal("report is nil")
	}
	if report.OverallScore != 0 {
		t.Errorf("expected 0 overall score, got %.2f", report.OverallScore)
	}
	if len(report.Weaknesses) != 0 {
		t.Errorf("expected no weaknesses, got %d", len(report.Weaknesses))
	}
}

func TestWeaknessReport_FormatMarkdown_AllSections(t *testing.T) {
	report := &WeaknessReport{
		GeneratedAt:  time.Now(),
		OverallScore: 0.65,
		TotalTasks:   10,
		FailedTasks:  4,
		DimReport: &DimensionReport{
			Dimensions: []DimensionScore{
				{Dimension: DimPlanning, TaskCount: 5, SuccessRate: 0.6, AvgScore: 0.55, AvgReplan: 2.0,
					TopFailures: []FailureCategory{FailPlanningError}},
				{Dimension: DimTaskExecution, TaskCount: 5, SuccessRate: 0.8, AvgScore: 0.85, AvgReplan: 0.5},
			},
			FailureDistribution: map[FailureCategory]int{
				FailPlanningError:   3,
				FailErrorNoRecovery: 1,
			},
		},
		Weaknesses: []Weakness{
			{ID: "W-001", Severity: "critical", Category: FailPlanningError, Dimension: DimPlanning,
				Description: "Planning errors", Evidence: []string{"t1", "t2", "t3"}, Frequency: 3},
		},
		Recommendations: []Recommendation{
			{TargetWeakness: "W-001", Priority: 1, Action: "Fix planning", Component: "agent", Detail: "details"},
		},
	}

	md := report.FormatMarkdown()
	if !contains(md, "0.65") {
		t.Error("missing overall score")
	}
	if !contains(md, "planning_error") {
		t.Error("missing failure category")
	}
	if !contains(md, "Failure Distribution") {
		t.Error("missing failure distribution section")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
