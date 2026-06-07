package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileContainsScorer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := (FileContains{Path: path, Substr: "world"}).Score(dir, ""); !got.Passed {
		t.Errorf("expected pass when substring present, got %+v", got)
	}
	if got := (FileContains{Path: path, Substr: "missing"}).Score(dir, ""); got.Passed {
		t.Errorf("expected fail when substring absent, got %+v", got)
	}
	if got := (FileContains{Path: filepath.Join(dir, "nope.txt"), Substr: "x"}).Score(dir, ""); got.Passed {
		t.Errorf("expected fail when file missing, got %+v", got)
	}
}

func TestFileExistsScorer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "present.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := (FileExists{Path: path}).Score(dir, ""); !got.Passed {
		t.Errorf("expected pass for existing file, got %+v", got)
	}
	if got := (FileExists{Path: filepath.Join(dir, "absent.txt")}).Score(dir, ""); got.Passed {
		t.Errorf("expected fail for absent file, got %+v", got)
	}
}

func TestOutputContainsScorer(t *testing.T) {
	if got := (OutputContains{Substr: "DONE"}).Score("", "task DONE here"); !got.Passed {
		t.Errorf("expected pass when output contains substr, got %+v", got)
	}
	if got := (OutputContains{Substr: "nope"}).Score("", "task DONE here"); got.Passed {
		t.Errorf("expected fail when output lacks substr, got %+v", got)
	}
}

func TestScorerNamesAreDescriptive(t *testing.T) {
	scorers := []Scorer{
		FileContains{Path: "a", Substr: "b"},
		FileExists{Path: "a"},
		OutputContains{Substr: "b"},
		CommandSucceeds{Command: "true"},
	}
	for _, s := range scorers {
		if s.Name() == "" {
			t.Errorf("%T returned empty Name()", s)
		}
	}
}
