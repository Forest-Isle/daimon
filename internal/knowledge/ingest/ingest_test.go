package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if len(r.ingesters) == 0 {
		t.Fatal("expected ingesters to be registered")
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()
	before := len(r.ingesters)
	r.Register(&MarkdownIngester{})
	if len(r.ingesters) != before+1 {
		t.Errorf("expected %d ingesters, got %d", before+1, len(r.ingesters))
	}
}

func TestRegistry_Extract_UnknownType(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.Extract(context.Background(), "test.xyz", "unknown")
	if err == nil {
		t.Error("expected error for unknown source type")
	}
}

func TestDetectSourceType(t *testing.T) {
	tests := []struct {
		uri      string
		expected string
	}{
		{"https://example.com", "web"},
		{"http://example.com/page.html", "web"},
		{"document.pdf", "pdf"},
		{"readme.md", "markdown"},
		{"doc.markdown", "markdown"},
		{"main.go", "code"},
		{"main.py", "code"},
		{"index.js", "code"},
		{"styles.css", "text"},
		{"config.toml", "code"},
		{"notes.txt", "text"},
		{"data.json", "code"},
		{"config.yaml", "code"},
		{"README", "text"},
		{"unknown.xyz", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got := DetectSourceType(tt.uri)
			if got != tt.expected {
				t.Errorf("DetectSourceType(%q) = %q, want %q", tt.uri, got, tt.expected)
			}
		})
	}
}

func TestScanDir(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "file1.md"), []byte("# test"), 0644)
	os.WriteFile(filepath.Join(dir, "file2.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0644)

	files, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}

	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	// Verify each file has a detected source type
	for _, f := range files {
		if f.SourceType == "" {
			t.Errorf("file %q has no source type", f.Path)
		}
	}
}

func TestScanDir_SkipHidden(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	os.WriteFile(filepath.Join(dir, ".hidden", "doc.md"), []byte("# hidden"), 0644)
	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("# visible"), 0644)
	os.MkdirAll(filepath.Join(dir, "vendor"), 0755)
	os.WriteFile(filepath.Join(dir, "vendor", "lib.go"), []byte("package lib"), 0644)

	files, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}

	for _, f := range files {
		if filepath.Base(filepath.Dir(f.Path)) == ".hidden" ||
			filepath.Base(filepath.Dir(f.Path)) == "vendor" {
			t.Errorf("hidden/vendor file should be skipped: %s", f.Path)
		}
	}
	if len(files) != 1 {
		t.Errorf("expected 1 visible file, got %d", len(files))
	}
}

func TestScanDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	files, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir on empty dir: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestIsCodeFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"file.go", true},
		{"file.py", true},
		{"file.js", true},
		{"file.ts", true},
		{"file.java", true},
		{"file.rs", true},
		{"file.yaml", true},
		{"file.json", true},
		{"file.txt", false},
		{"file.md", false},
		{"file.pdf", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isCodeFile(tt.path)
			if got != tt.expected {
				t.Errorf("isCodeFile(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}
