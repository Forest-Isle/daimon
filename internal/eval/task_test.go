package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTask_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.json")
	content := `{
		"id": "fix-typo",
		"prompt": "Fix the typo in greeting.txt",
		"scorers": [
			{"type": "file_contains", "path": "greeting.txt", "substr": "hello"},
			{"type": "command_succeeds", "command": "test -f greeting.txt"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	task, err := LoadTask(path)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if task.ID != "fix-typo" {
		t.Errorf("ID = %q, want fix-typo", task.ID)
	}
	if len(task.Scorers) != 2 {
		t.Fatalf("scorers = %d, want 2", len(task.Scorers))
	}
	// Spec must materialize into real Scorer implementations.
	concrete, err := task.BuildScorers()
	if err != nil {
		t.Fatalf("BuildScorers: %v", err)
	}
	if len(concrete) != 2 {
		t.Fatalf("built scorers = %d, want 2", len(concrete))
	}
	if _, ok := concrete[0].(FileContains); !ok {
		t.Errorf("scorer[0] = %T, want FileContains", concrete[0])
	}
	if _, ok := concrete[1].(CommandSucceeds); !ok {
		t.Errorf("scorer[1] = %T, want CommandSucceeds", concrete[1])
	}
}

func TestLoadTask_UnknownScorerType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.json")
	content := `{"id":"x","prompt":"p","scorers":[{"type":"bogus"}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := LoadTask(path)
	if err != nil {
		t.Fatalf("LoadTask should parse even with unknown scorer: %v", err)
	}
	if _, err := task.BuildScorers(); err == nil {
		t.Error("BuildScorers should reject unknown scorer type")
	}
}

func TestLoadTask_MissingFile(t *testing.T) {
	if _, err := LoadTask(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected error for missing task file")
	}
}
