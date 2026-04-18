package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitContextProvider_InGitRepo(t *testing.T) {
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"HOME="+dir,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create and commit a file
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "hello.go")
	run("commit", "-m", "initial commit")

	// Create an uncommitted file
	if err := os.WriteFile(filepath.Join(dir, "uncommitted.txt"), []byte("wip\n"), 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewGitContextProvider()
	state := provider.Collect(dir)

	if state == nil {
		t.Fatal("expected non-nil GitState in git repo")
	}
	if state.Branch == "" {
		t.Error("expected non-empty branch")
	}
	if len(state.UncommittedFiles) == 0 {
		t.Error("expected non-empty uncommitted files")
	}
	if len(state.RecentCommits) == 0 {
		t.Error("expected non-empty recent commits")
	}
	if state.RawContent == "" {
		t.Error("expected non-empty RawContent")
	}
}

func TestGitContextProvider_NotGitRepo(t *testing.T) {
	dir := t.TempDir()

	provider := NewGitContextProvider()
	state := provider.Collect(dir)

	if state != nil {
		t.Fatalf("expected nil GitState for non-git dir, got %+v", state)
	}
}
