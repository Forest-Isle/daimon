package values

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddPersistsAndIndexes(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ctx := context.Background()

	e, err := s.Add(ctx, Entry{
		Domain:     "travel",
		Statement:  "Prefer paying up to 500 more over booking a red-eye flight",
		Confidence: 0.9,
		Provenance: []Provenance{{Episode: "ep-1", Date: "2026-06-14", Quote: "no red-eyes"}},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if e.ID == "" || e.State != StateActive {
		t.Fatalf("entry not finalized: %+v", e)
	}

	// File must exist under <root>/<domain>/<slug>.md.
	matches, _ := filepath.Glob(filepath.Join(dir, "travel", "*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 markdown file, got %v", matches)
	}

	got, ok := s.Lookup("travel")
	if !ok || got.ID != e.ID {
		t.Fatalf("Lookup(travel) = %+v, %v", got, ok)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ctx := context.Background()
	if _, err := s.Add(ctx, Entry{Domain: "bash", Statement: "May auto-run destructive shell in /tmp only", Confidence: 0.85}); err != nil {
		t.Fatal(err)
	}

	// Fresh store over the same dir must see the persisted entry.
	s2 := NewStore(dir)
	if err := s2.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := s2.Lookup("bash")
	if !ok {
		t.Fatalf("entry not reloaded: %+v", s2.List())
	}
	if !strings.Contains(got.Statement, "/tmp") || got.Confidence != 0.85 {
		t.Fatalf("reloaded entry mismatch: %+v", got)
	}
}

func TestLookupIgnoresNonActive(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ctx := context.Background()
	if _, err := s.Add(ctx, Entry{Domain: "spending", Statement: "retired rule", State: StateRetired, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("spending"); ok {
		t.Fatal("retired entry should not permit")
	}
	if _, err := s.Add(ctx, Entry{Domain: "spending", Statement: "drifting rule", State: StateDrifting, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("spending"); ok {
		t.Fatal("drifting entry should not permit")
	}
}

func TestDigestFiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ctx := context.Background()
	_, _ = s.Add(ctx, Entry{Domain: "a", Statement: "high confidence rule", Confidence: 0.95})
	_, _ = s.Add(ctx, Entry{Domain: "b", Statement: "low confidence rule", Confidence: 0.3})
	_, _ = s.Add(ctx, Entry{Domain: "c", Statement: "retired high rule", Confidence: 0.99, State: StateRetired})

	digest := s.Digest()
	if !strings.Contains(digest, "high confidence rule") {
		t.Fatalf("digest missing high-confidence entry:\n%s", digest)
	}
	if strings.Contains(digest, "low confidence rule") {
		t.Fatalf("digest should drop below-threshold entry:\n%s", digest)
	}
	if strings.Contains(digest, "retired high rule") {
		t.Fatalf("digest should drop retired entry:\n%s", digest)
	}
}

func TestAddRequiresFields(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Add(context.Background(), Entry{Statement: "no domain"}); err == nil {
		t.Fatal("expected error for missing domain")
	}
	if _, err := s.Add(context.Background(), Entry{Domain: "x"}); err == nil {
		t.Fatal("expected error for missing statement")
	}
}

func TestAddIdempotentBySlug(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ctx := context.Background()
	stmt := "Never deploy on Fridays without approval"
	if _, err := s.Add(ctx, Entry{Domain: "ops", Statement: stmt, Confidence: 0.8}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, Entry{Domain: "ops", Statement: stmt, Confidence: 0.95}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "ops", "*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected idempotent overwrite (1 file), got %v", matches)
	}
	got, _ := s.Lookup("ops")
	if got.Confidence != 0.95 {
		t.Fatalf("re-add should update confidence, got %v", got.Confidence)
	}
}

func TestSanitizeDomainPreventsTraversal(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Add(context.Background(), Entry{Domain: "../../etc", Statement: "escape attempt", Confidence: 0.9}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Nothing must be written outside the root.
	parent := filepath.Dir(dir)
	if _, err := os.Stat(filepath.Join(parent, "etc")); err == nil {
		t.Fatal("domain traversal escaped the store root")
	}
}

func TestAddRejectsSymlinkedDomainEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "values")
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a pre-existing symlink: root/escape -> outside.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	s := NewStore(root)
	_, err := s.Add(context.Background(), Entry{Domain: "escape", Statement: "should not write outside", Confidence: 0.9})
	if err == nil {
		t.Fatal("expected symlinked domain write to be rejected")
	}
	// Nothing should have been written into the symlink target.
	matches, _ := filepath.Glob(filepath.Join(outside, "*.md"))
	if len(matches) != 0 {
		t.Fatalf("write escaped the root via symlink: %v", matches)
	}
}

func TestParseEntryRejectsMalformed(t *testing.T) {
	if _, err := parseEntry([]byte("no frontmatter here")); err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
	if _, err := parseEntry([]byte("---\nid: x\n")); err == nil {
		t.Fatal("expected error for unterminated frontmatter")
	}
}
