package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReport_Aggregate(t *testing.T) {
	results := []Result{
		{TaskID: "a", Passed: true, Score: 1.0},
		{TaskID: "b", Passed: false, Score: 0.5},
		{TaskID: "c", Passed: true, Score: 1.0},
	}
	rep := NewReport(results)

	if rep.Total != 3 {
		t.Errorf("Total = %d, want 3", rep.Total)
	}
	if rep.Passed != 2 {
		t.Errorf("Passed = %d, want 2", rep.Passed)
	}
	if rep.PassRate < 0.66 || rep.PassRate > 0.67 {
		t.Errorf("PassRate = %v, want ~0.667", rep.PassRate)
	}
}

func TestReport_EmptyIsSafe(t *testing.T) {
	rep := NewReport(nil)
	if rep.Total != 0 || rep.PassRate != 0 {
		t.Errorf("empty report should be zero-valued, got %+v", rep)
	}
}

func TestReport_StringMentionsFailures(t *testing.T) {
	rep := NewReport([]Result{
		{TaskID: "ok-task", Passed: true, Score: 1.0},
		{TaskID: "bad-task", Passed: false, Score: 0.0},
	})
	s := rep.String()
	if !strings.Contains(s, "bad-task") {
		t.Errorf("report string should name the failing task, got:\n%s", s)
	}
	if !strings.Contains(s, "1/2") {
		t.Errorf("report string should show pass count 1/2, got:\n%s", s)
	}
}

func TestLoadSuite_LoadsAllTasks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"t1.json", "t2.json"} {
		body := `{"id":"` + name + `","prompt":"p","scorers":[]}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A non-json file must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}

	tasks, err := LoadSuite(dir)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("loaded %d tasks, want 2", len(tasks))
	}
}
