package hook

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestGitContextInjectorInGitRepo(t *testing.T) {
	// This test runs in the IronClaw repo which IS a git repo
	g := &GitContextInjector{TimeoutMs: 5000}
	result, err := g.OnUserMessage(context.Background(), OnUserMessageEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.InjectedContext) == 0 {
		t.Skip("not in a git repo")
	}
	ctx := result.InjectedContext[0]
	if !strings.Contains(ctx, "Git:") {
		t.Errorf("expected Git: prefix, got %q", ctx)
	}
	if !strings.Contains(ctx, "Branch:") {
		t.Errorf("expected Branch: info, got %q", ctx)
	}
}

func TestGitContextInjectorNotGitRepo(t *testing.T) {
	// Run in /tmp which is not a git repo
	origDir, _ := os.Getwd()
	os.Chdir(os.TempDir())
	defer os.Chdir(origDir)

	g := &GitContextInjector{TimeoutMs: 2000}
	result, err := g.OnUserMessage(context.Background(), OnUserMessageEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.InjectedContext) > 0 {
		t.Error("should return empty context for non-git dir")
	}
}

func TestWorkdirContextInjectorBasic(t *testing.T) {
	w := &WorkdirContextInjector{IncludeLS: false, MaxFiles: 20}
	result, err := w.OnUserMessage(context.Background(), OnUserMessageEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.InjectedContext) == 0 {
		t.Fatal("expected context")
	}
	if !strings.Contains(result.InjectedContext[0], "CWD:") {
		t.Error("expected CWD in context")
	}
}

func TestWorkdirContextInjectorWithLS(t *testing.T) {
	// Create temp dir with some files
	tmpDir := t.TempDir()
	os.WriteFile(tmpDir+"/a.txt", []byte("a"), 0o644)
	os.WriteFile(tmpDir+"/b.txt", []byte("b"), 0o644)
	os.Mkdir(tmpDir+"/subdir", 0o755)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	w := &WorkdirContextInjector{IncludeLS: true, MaxFiles: 10}
	result, err := w.OnUserMessage(context.Background(), OnUserMessageEvent{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := result.InjectedContext[0]
	if !strings.Contains(ctx, "Files:") {
		t.Error("expected Files: section")
	}
	if !strings.Contains(ctx, "a.txt") {
		t.Error("expected a.txt in listing")
	}
	if !strings.Contains(ctx, "d subdir") {
		t.Error("expected directory marker for subdir")
	}
}

func TestWorkdirContextInjectorMaxFiles(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 5; i++ {
		os.WriteFile(tmpDir+"/"+string(rune('a'+i))+".txt", []byte("x"), 0o644)
	}

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	w := &WorkdirContextInjector{IncludeLS: true, MaxFiles: 3}
	result, err := w.OnUserMessage(context.Background(), OnUserMessageEvent{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := result.InjectedContext[0]
	if !strings.Contains(ctx, "more files not shown") {
		t.Error("expected truncation notice")
	}
}
