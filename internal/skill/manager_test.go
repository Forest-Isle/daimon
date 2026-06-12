package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("---\nname: " + name + "\ndescription: test\nversion: \"1.0\"\ntags: [test]\n---\n# Body\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir_SubdirWithSKILLMD(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "myskill", "SKILL.md"), "myskill")

	m := New()
	if err := m.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if got := len(m.All()); got != 1 {
		t.Fatalf("expected 1 skill, got %d", got)
	}
	if m.All()[0].Name != "myskill" {
		t.Fatalf("expected name 'myskill', got %q", m.All()[0].Name)
	}
}

func TestLoadDir_FlatMDAtTopLevel(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "quick.md"), "quick")

	m := New()
	if err := m.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if got := len(m.All()); got != 1 {
		t.Fatalf("expected 1 skill, got %d", got)
	}
}

func TestLoadDir_SubdirFlatMDFiles(t *testing.T) {
	dir := t.TempDir()
	drafts := filepath.Join(dir, "drafts")
	writeSkill(t, filepath.Join(drafts, "SKILL_bash+file_write.md"), "bash_file_write")
	writeSkill(t, filepath.Join(drafts, "SKILL_http_get.md"), "http_get")

	m := New()
	if err := m.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	all := m.All()
	if got := len(all); got != 2 {
		t.Fatalf("expected 2 skills from drafts/, got %d", got)
	}

	names := make(map[string]bool)
	for _, s := range all {
		names[s.Name] = true
	}
	if !names["bash_file_write"] || !names["http_get"] {
		t.Fatalf("expected skills bash_file_write and http_get, got %v", names)
	}
}

func TestLoadDir_SKILLMDTakesPrecedenceOverFlatFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "mysub")
	writeSkill(t, filepath.Join(sub, "SKILL.md"), "primary")
	writeSkill(t, filepath.Join(sub, "extra.md"), "extra")

	m := New()
	if err := m.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	all := m.All()
	if got := len(all); got != 1 {
		t.Fatalf("expected 1 skill (SKILL.md takes precedence), got %d", got)
	}
	if all[0].Name != "primary" {
		t.Fatalf("expected 'primary', got %q", all[0].Name)
	}
}

func TestLoadDir_DedupFirstLoadedWins(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "a.md"), "samename")
	writeSkill(t, filepath.Join(dir, "b.md"), "samename")

	m := New()
	if err := m.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	if got := len(m.All()); got != 1 {
		t.Fatalf("expected 1 skill (dedup), got %d", got)
	}
}

func TestLoadDir_NonExistentDir(t *testing.T) {
	m := New()
	if err := m.LoadDir("/tmp/daimon-does-not-exist-12345"); err != nil {
		t.Fatalf("non-existent dir should return nil, got %v", err)
	}
}
