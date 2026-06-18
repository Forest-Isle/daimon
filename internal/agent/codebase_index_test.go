package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recordingEmbeddingProvider struct {
	calls   atomic.Int64
	active  atomic.Int64
	peak    atomic.Int64
	failSub string
	delay   time.Duration
}

func (p *recordingEmbeddingProvider) Embed(ctx context.Context, text string) ([]float64, error) {
	p.calls.Add(1)
	active := p.active.Add(1)
	defer p.active.Add(-1)

	for {
		peak := p.peak.Load()
		if active <= peak || p.peak.CompareAndSwap(peak, active) {
			break
		}
	}

	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.failSub != "" && strings.Contains(text, p.failSub) {
		return nil, errors.New("embed failed")
	}
	return []float64{1, 0, 0}, nil
}

func TestCodebaseIndexIndexDirectoryConcurrent(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(path, []byte("package main\nfunc f() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	provider := &recordingEmbeddingProvider{delay: 20 * time.Millisecond}
	idx := NewCodebaseIndex(provider, IndexConfig{ChunkSize: 20, Concurrency: 4})

	if err := idx.IndexDirectoryContext(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != int64(len(paths)) {
		t.Fatalf("expected %d embed calls, got %d", len(paths), provider.calls.Load())
	}
	if provider.peak.Load() <= 1 {
		t.Fatalf("expected concurrent embed calls, peak was %d", provider.peak.Load())
	}
	for _, path := range paths {
		if got := len(idx.chunkStore[path]); got == 0 {
			t.Fatalf("expected chunks for %s, got %d", path, got)
		}
	}
}

func TestCodebaseIndexIndexDirectorySkipsFailedFile(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.go")
	failPath := filepath.Join(dir, "fail.go")
	otherPath := filepath.Join(dir, "other.go")
	if err := os.WriteFile(okPath, []byte("package main\nconst ok = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(failPath, []byte("package main\nconst fail_marker = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("package main\nconst other = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := &recordingEmbeddingProvider{failSub: "fail_marker", delay: 5 * time.Millisecond}
	idx := NewCodebaseIndex(provider, IndexConfig{ChunkSize: 20, Concurrency: 2})

	if err := idx.IndexDirectoryContext(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if len(idx.chunkStore[okPath]) == 0 {
		t.Fatalf("expected chunks for %s", okPath)
	}
	if len(idx.chunkStore[otherPath]) == 0 {
		t.Fatalf("expected chunks for %s", otherPath)
	}
	if _, ok := idx.chunkStore[failPath]; ok {
		t.Fatalf("expected failed file %s to be skipped", failPath)
	}
}
