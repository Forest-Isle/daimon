package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileGuard_AllowedPath(t *testing.T) {
	dir := t.TempDir()
	g, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	target := filepath.Join(dir, "subdir", "file.txt")
	if err := g.ValidateAccess(target, false); err != nil {
		t.Errorf("expected read access allowed, got: %v", err)
	}
	if err := g.ValidateAccess(target, true); err != nil {
		t.Errorf("expected write access allowed, got: %v", err)
	}
}

func TestFileGuard_DeniedPath(t *testing.T) {
	dir := t.TempDir()
	g, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	if err := g.ValidateAccess("/etc/passwd", false); err == nil {
		t.Error("expected access denied for /etc/passwd")
	}
}

func TestFileGuard_ReadonlyWrite(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(roDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	g, err := NewFileGuard([]string{dir}, []string{roDir})
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	target := filepath.Join(roDir, "data.txt")
	if err := g.ValidateAccess(target, false); err != nil {
		t.Errorf("expected read access allowed, got: %v", err)
	}
	if err := g.ValidateAccess(target, true); err == nil {
		t.Error("expected write denied in readonly dir")
	}
}

func TestFileGuard_TraversalPrevention(t *testing.T) {
	dir := t.TempDir()
	g, err := NewFileGuard([]string{dir}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	traversal := filepath.Join(dir, "..", "..", "etc", "passwd")
	if err := g.ValidateAccess(traversal, false); err == nil {
		t.Error("expected access denied for traversal path")
	}
}

func TestFileGuard_SymlinkEscape(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()

	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	link := filepath.Join(allowed, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	g, err := NewFileGuard([]string{allowed}, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	target := filepath.Join(link, "secret.txt")
	if err := g.ValidateAccess(target, false); err == nil {
		t.Error("expected access denied for symlink escape")
	}
}

func TestFileGuard_Empty_NoRestriction(t *testing.T) {
	g, err := NewFileGuard(nil, nil)
	if err != nil {
		t.Fatalf("NewFileGuard: %v", err)
	}
	if err := g.ValidateAccess("/any/random/path", false); err != nil {
		t.Errorf("expected no restriction, got: %v", err)
	}
	if err := g.ValidateAccess("/another/path", true); err != nil {
		t.Errorf("expected no restriction on write, got: %v", err)
	}
}
