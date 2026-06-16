package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestEnsureRepoIdempotent(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()

	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf(".git stat: %v", err)
	}
}

func TestCommitStagesChangesAndSkipsNoop(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "profile.md", "one\n")
	sha, committed, err := Commit(ctx, dir, "world_edit: profile.md")
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("committed = false, want true")
	}
	if strings.TrimSpace(sha) == "" {
		t.Fatal("sha is empty")
	}

	sha, committed, err = Commit(ctx, dir, "world_edit: profile.md")
	if err != nil {
		t.Fatal(err)
	}
	if committed || sha != "" {
		t.Fatalf("noop Commit = (%q, %v), want empty false", sha, committed)
	}
}

func TestLogWithLimitAndPathFilter(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "profile.md", "one\n")
	if _, _, err := Commit(ctx, dir, "profile one"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "preferences/coding.md", "go\n")
	if _, _, err := Commit(ctx, dir, "coding one"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "profile.md", "two\n")
	if _, _, err := Commit(ctx, dir, "profile two"); err != nil {
		t.Fatal(err)
	}

	all, err := Log(ctx, dir, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all log len = %d, want 2", len(all))
	}
	if all[0].Subject != "profile two" || all[1].Subject != "coding one" {
		t.Fatalf("all subjects = %#v", all)
	}

	filtered, err := Log(ctx, dir, "preferences/coding.md", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Subject != "coding one" {
		t.Fatalf("filtered log = %#v, want only coding one", filtered)
	}
}

func TestLogEmptyRepoReturnsEmpty(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	commits, err := Log(ctx, dir, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Fatalf("commits len = %d, want 0", len(commits))
	}
}

func TestRevertFileToPreviousRestoresPriorContentAndCommits(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "profile.md", "one\n")
	if _, _, err := Commit(ctx, dir, "profile one"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "profile.md", "two\n")
	if _, _, err := Commit(ctx, dir, "profile two"); err != nil {
		t.Fatal(err)
	}

	if err := RevertFileToPrevious(ctx, dir, "profile.md"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "profile.md")); got != "one\n" {
		t.Fatalf("profile content = %q, want one\\n", got)
	}
	commits, err := Log(ctx, dir, "profile.md", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("profile commits len = %d, want 3", len(commits))
	}
	if commits[0].Subject != "world revert: profile.md" {
		t.Fatalf("latest subject = %q, want revert", commits[0].Subject)
	}
}

func TestRevertFileToPreviousUsesFileHistory(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.md", "v1\n")
	if _, _, err := Commit(ctx, dir, "a v1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "a.md", "v2\n")
	if _, _, err := Commit(ctx, dir, "a v2"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "b.md", "b1\n")
	if _, _, err := Commit(ctx, dir, "b v1"); err != nil {
		t.Fatal(err)
	}

	if err := RevertFileToPrevious(ctx, dir, "a.md"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "a.md")); got != "v1\n" {
		t.Fatalf("a content = %q, want v1\\n", got)
	}
	if got := readFile(t, filepath.Join(dir, "b.md")); got != "b1\n" {
		t.Fatalf("b content = %q, want b1\\n", got)
	}
	commits, err := Log(ctx, dir, "a.md", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("a commits len = %d, want 3", len(commits))
	}
	if commits[0].Subject != "world revert: a.md" {
		t.Fatalf("latest subject = %q, want revert", commits[0].Subject)
	}
}

func TestRevertFileToPreviousSingleHistoryDeletes(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "profile.md", "one\n")
	if _, _, err := Commit(ctx, dir, "profile one"); err != nil {
		t.Fatal(err)
	}

	if err := RevertFileToPrevious(ctx, dir, "profile.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profile.md")); !os.IsNotExist(err) {
		t.Fatalf("profile.md stat err = %v, want not exist", err)
	}
	commits, err := Log(ctx, dir, "profile.md", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("profile commits len = %d, want 2", len(commits))
	}
	if commits[0].Subject != "world revert: profile.md" {
		t.Fatalf("latest subject = %q, want revert", commits[0].Subject)
	}
}

func TestRevertFileToPreviousNoHistory(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "profile.md", "one\n")
	if _, _, err := Commit(ctx, dir, "profile one"); err != nil {
		t.Fatal(err)
	}

	err := RevertFileToPrevious(ctx, dir, "missing.md")
	if err == nil || !strings.Contains(err.Error(), `no history for "missing.md"`) {
		t.Fatalf("err = %v, want no history", err)
	}
}

func TestRevertFileToPreviousEmptyRepo(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}

	err := RevertFileToPrevious(ctx, dir, "profile.md")
	if err == nil || !strings.Contains(err.Error(), "no previous version") {
		t.Fatalf("err = %v, want no previous version", err)
	}
}

func TestCleanRelPathRejectsDashAndDotDot(t *testing.T) {
	clean, err := cleanRelPath("-x")
	if err != nil {
		t.Fatalf("cleanRelPath(-x) error = %v", err)
	}
	if clean != "-x" {
		t.Fatalf("cleanRelPath(-x) = %q, want -x", clean)
	}
	for _, path := range []string{"..", "../x", "/tmp/x"} {
		if _, err := cleanRelPath(path); err == nil {
			t.Fatalf("cleanRelPath(%q) error = nil, want error", path)
		}
	}
}

func TestCommitPathScoped(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	if err := EnsureRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b.txt", "b\n")
	if _, committed, err := Commit(ctx, dir, "a only", "a.txt"); err != nil {
		t.Fatal(err)
	} else if !committed {
		t.Fatal("committed = false, want true")
	}

	out, err := gitOutput(ctx, dir, "ls-files")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "a.txt" {
		t.Fatalf("ls-files = %q, want a.txt", got)
	}
	out, err = gitOutput(ctx, dir, "status", "--short", "--", "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "?? b.txt" {
		t.Fatalf("b.txt status = %q, want ?? b.txt", got)
	}
}
