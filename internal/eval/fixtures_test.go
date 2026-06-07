package eval

import (
	"testing"
)

// TestSeedFixturesAreValid keeps the seed corpus honest: every task file shipped
// in testdata/tasks must parse and its scorers must materialize. This is the
// guard that prevents a malformed example task from rotting unnoticed.
func TestSeedFixturesAreValid(t *testing.T) {
	tasks, err := LoadSuite("testdata/tasks")
	if err != nil {
		t.Fatalf("LoadSuite(testdata/tasks): %v", err)
	}
	if len(tasks) < 2 {
		t.Fatalf("expected at least 2 seed tasks, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.Prompt == "" {
			t.Errorf("task %s has empty prompt", task.ID)
		}
		if len(task.Scorers) == 0 {
			t.Errorf("task %s has no scorers", task.ID)
		}
		if _, err := task.BuildScorers(); err != nil {
			t.Errorf("task %s scorers invalid: %v", task.ID, err)
		}
	}
}
