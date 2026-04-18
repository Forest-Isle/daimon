package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectContextScanner_GoProject(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/example/myapp\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# My App\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	makefile := "build:\n\tgo build ./...\ntest:\n\tgo test ./...\nlint:\n\tgolangci-lint run\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewProjectContextScanner()
	ctx := scanner.Scan(dir)
	if ctx == nil {
		t.Fatal("expected non-nil ProjectContext for Go project")
	}

	if ctx.Name != "github.com/example/myapp" {
		t.Errorf("Name = %q, want %q", ctx.Name, "github.com/example/myapp")
	}
	if ctx.Language != "go" {
		t.Errorf("Language = %q, want %q", ctx.Language, "go")
	}
	if !ctx.HasReadme {
		t.Error("HasReadme should be true")
	}

	wantCmds := []string{"go build ./...", "go test ./..."}
	for _, want := range wantCmds {
		found := false
		for _, got := range ctx.BuildCommands {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("BuildCommands missing %q, got %v", want, ctx.BuildCommands)
		}
	}

	hasMakeTargets := false
	for _, cmd := range ctx.BuildCommands {
		if strings.HasPrefix(cmd, "make ") {
			hasMakeTargets = true
			break
		}
	}
	if !hasMakeTargets {
		t.Errorf("BuildCommands should include make targets, got %v", ctx.BuildCommands)
	}

	wantDirs := map[string]bool{"cmd": false, "internal": false}
	for _, d := range ctx.KeyDirectories {
		if _, ok := wantDirs[d]; ok {
			wantDirs[d] = true
		}
	}
	for d, found := range wantDirs {
		if !found {
			t.Errorf("KeyDirectories missing %q, got %v", d, ctx.KeyDirectories)
		}
	}

	if ctx.RawContent == "" {
		t.Error("RawContent should be non-empty")
	}
}

func TestProjectContextScanner_NodeProject(t *testing.T) {
	dir := t.TempDir()

	pkg := `{"name": "my-node-app", "scripts": {"build": "tsc", "test": "jest", "dev": "vite"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}

	scanner := NewProjectContextScanner()
	ctx := scanner.Scan(dir)
	if ctx == nil {
		t.Fatal("expected non-nil ProjectContext for Node project")
	}

	if ctx.Name != "my-node-app" {
		t.Errorf("Name = %q, want %q", ctx.Name, "my-node-app")
	}
	if ctx.Language != "javascript" {
		t.Errorf("Language = %q, want %q", ctx.Language, "javascript")
	}
	if len(ctx.BuildCommands) < 3 {
		t.Errorf("expected at least 3 build commands for build/test/dev scripts, got %v", ctx.BuildCommands)
	}

	foundSrc := false
	for _, d := range ctx.KeyDirectories {
		if d == "src" {
			foundSrc = true
		}
	}
	if !foundSrc {
		t.Errorf("KeyDirectories missing 'src', got %v", ctx.KeyDirectories)
	}
}

func TestProjectContextScanner_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	scanner := NewProjectContextScanner()
	ctx := scanner.Scan(dir)
	if ctx != nil {
		t.Errorf("expected nil for empty dir, got %+v", ctx)
	}
}

func TestProjectContextScanner_Caching(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/cached\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewProjectContextScanner()
	first := scanner.Scan(dir)
	second := scanner.Scan(dir)

	if first != second {
		t.Error("expected same pointer from cached call")
	}

	scanner.Invalidate(dir)
	third := scanner.Scan(dir)
	if third == first {
		t.Error("expected new pointer after Invalidate")
	}
	if third.Name != first.Name {
		t.Errorf("content should match after re-scan: %q vs %q", third.Name, first.Name)
	}
}
