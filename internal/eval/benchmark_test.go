package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSWEBenchAdapter_LoadTasks(t *testing.T) {
	tasks := []sweBenchTask{
		{InstanceID: "test-1", Repo: "owner/repo", BaseCommit: "abc12345", Problem: "Fix bug", TestPatch: "patch"},
		{InstanceID: "test-2", Repo: "owner/repo2", BaseCommit: "def67890", Problem: "Another bug", Difficulty: "easy"},
	}
	data, _ := json.Marshal(tasks)
	path := filepath.Join(t.TempDir(), "swe.json")
	os.WriteFile(path, data, 0o644)

	adapter := &SWEBenchAdapter{}
	loaded, err := adapter.LoadTasks(path)
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(loaded))
	}
	if loaded[0].ID != "swe-test-1" {
		t.Errorf("expected swe-test-1, got %s", loaded[0].ID)
	}
	if loaded[0].Dimension != DimTaskExecution {
		t.Errorf("expected task_execution dimension")
	}
	if loaded[1].Complexity != "moderate" {
		t.Errorf("expected moderate for easy task, got %s", loaded[1].Complexity)
	}
}

func TestHumanEvalAdapter_LoadTasks(t *testing.T) {
	tasks := []humanEvalTask{
		{TaskID: "HumanEval/0", Prompt: "def has_close_elements(numbers):", EntryPoint: "has_close_elements", Test: "assert True"},
	}
	data, _ := json.Marshal(tasks)
	path := filepath.Join(t.TempDir(), "he.json")
	os.WriteFile(path, data, 0o644)

	adapter := &HumanEvalAdapter{}
	loaded, err := adapter.LoadTasks(path)
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 task, got %d", len(loaded))
	}
	if loaded[0].VerifyMethod != VerifyDeterministic {
		t.Errorf("expected deterministic verify")
	}
}

func TestGAIAAdapter_LoadTasks(t *testing.T) {
	tasks := []gaiaTask{
		{TaskID: "q-001", Question: "What is 2+2?", Level: 1, FinalAnswer: "4"},
		{TaskID: "q-002", Question: "Complex question", Level: 3, FinalAnswer: "answer"},
	}
	data, _ := json.Marshal(tasks)
	path := filepath.Join(t.TempDir(), "gaia.json")
	os.WriteFile(path, data, 0o644)

	adapter := &GAIAAdapter{}
	loaded, err := adapter.LoadTasks(path)
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(loaded))
	}
	if loaded[0].VerifyMethod != VerifyHybrid {
		t.Errorf("expected hybrid verify")
	}
	if loaded[1].Complexity != "complex" {
		t.Errorf("expected complex for level 3, got %s", loaded[1].Complexity)
	}
	if loaded[0].Reference == nil || loaded[0].Reference.Answer != "4" {
		t.Error("expected reference answer '4'")
	}
}

func TestAllBenchmarkAdapters(t *testing.T) {
	adapters := AllBenchmarkAdapters()
	if len(adapters) != 3 {
		t.Fatalf("expected 3 adapters, got %d", len(adapters))
	}
	for name, a := range adapters {
		if a.Name() != name {
			t.Errorf("adapter name mismatch: key=%s, Name()=%s", name, a.Name())
		}
	}
}

func TestComputeBenchmarkComparison(t *testing.T) {
	results := []EvalResult{
		{TaskID: "t1", Success: true, FinalScore: 0.9},
		{TaskID: "t2", Success: false, FinalScore: 0.3},
		{TaskID: "t3", Success: true, FinalScore: 0.7},
	}
	refs := SWEBenchReferences()
	comp := ComputeBenchmarkComparison("swe-bench", results, refs)
	if comp.TotalTasks != 3 {
		t.Errorf("expected 3 total, got %d", comp.TotalTasks)
	}
	if comp.PassedTasks != 2 {
		t.Errorf("expected 2 passed, got %d", comp.PassedTasks)
	}
	md := comp.FormatComparisonMarkdown()
	if md == "" {
		t.Error("empty markdown")
	}
}

func TestSWEBenchAdapter_FormatResult(t *testing.T) {
	results := []EvalResult{
		{TaskID: "swe-test-1", Success: true, FinalScore: 1.0},
	}
	adapter := &SWEBenchAdapter{}
	data, err := adapter.FormatResult(results)
	if err != nil {
		t.Fatalf("FormatResult: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty result")
	}
}

func TestBenchmarkReferences(t *testing.T) {
	if len(SWEBenchReferences()) == 0 {
		t.Error("no SWE-bench references")
	}
	if len(HumanEvalReferences()) == 0 {
		t.Error("no HumanEval references")
	}
	if len(GAIAReferences()) == 0 {
		t.Error("no GAIA references")
	}
}
