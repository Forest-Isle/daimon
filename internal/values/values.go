// Package values is the value model: explicit, sourced, editable user values
// that serve as the permission source for autonomous action. Entries live as
// markdown files under <world>/values/<domain>/<slug>.md so they are part of the
// world git repo and hand-editable; the Store keeps an in-memory index for the
// action value gate (ask-once) and the episode composer (high-confidence digest).
package values

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Provenance records where a value came from: the episode that surfaced it, the
// date, and the user's own words.
type Provenance struct {
	Episode string `yaml:"episode,omitempty" json:"episode,omitempty"`
	Date    string `yaml:"date,omitempty" json:"date,omitempty"`
	Quote   string `yaml:"quote,omitempty" json:"quote,omitempty"`
}

// Entry is one value: a durable principle scoped to a domain, with a confidence
// and lifecycle state. Body is freeform markdown that follows the frontmatter.
type Entry struct {
	ID         string       `yaml:"id" json:"id"`
	Domain     string       `yaml:"domain" json:"domain"`
	Statement  string       `yaml:"statement" json:"statement"`
	Confidence float64      `yaml:"confidence" json:"confidence"`
	Provenance []Provenance `yaml:"provenance,omitempty" json:"provenance,omitempty"`
	State      string       `yaml:"state" json:"state"` // active | drifting | retired
	Body       string       `yaml:"-" json:"-"`
}

// Lifecycle states.
const (
	StateActive   = "active"
	StateDrifting = "drifting"
	StateRetired  = "retired"
)

// Store loads, indexes, and persists value entries. It is safe for concurrent
// use: the action gate reads (Lookup), the composer reads (Digest), and the
// values tool writes (Add).
type Store struct {
	root string

	mu       sync.RWMutex
	byID     map[string]Entry
	byDomain map[string][]string // domain -> entry ids, insertion order
}

// NewStore builds a value store rooted at dir (typically <world>/values). The
// directory is created lazily on first write.
func NewStore(dir string) *Store {
	return &Store{
		root:     dir,
		byID:     map[string]Entry{},
		byDomain: map[string][]string{},
	}
}

// Load walks the root directory, parses every *.md file as a value entry, and
// rebuilds the in-memory index. Unparseable files are skipped with a warning so
// one bad file does not blind the whole store. A missing root is not an error
// (an empty store is valid).
func (s *Store) Load(_ context.Context) error {
	if s == nil {
		return nil
	}
	byID := map[string]Entry{}
	byDomain := map[string][]string{}

	if _, err := os.Stat(s.root); os.IsNotExist(err) {
		s.swap(byID, byDomain)
		return nil
	}

	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Warn("values: read entry failed", "path", path, "err", readErr)
			return nil
		}
		entry, parseErr := parseEntry(raw)
		if parseErr != nil {
			slog.Warn("values: parse entry failed", "path", path, "err", parseErr)
			return nil
		}
		if entry.ID == "" {
			slog.Warn("values: entry missing id, skipped", "path", path)
			return nil
		}
		index(byID, byDomain, entry)
		return nil
	})
	if err != nil {
		return fmt.Errorf("values: load: %w", err)
	}
	s.swap(byID, byDomain)
	return nil
}

func (s *Store) swap(byID map[string]Entry, byDomain map[string][]string) {
	s.mu.Lock()
	s.byID = byID
	s.byDomain = byDomain
	s.mu.Unlock()
}

func index(byID map[string]Entry, byDomain map[string][]string, e Entry) {
	if _, seen := byID[e.ID]; !seen {
		byDomain[e.Domain] = append(byDomain[e.Domain], e.ID)
	}
	byID[e.ID] = e
}

// Add persists a new value entry and indexes it. The caller supplies domain,
// statement, confidence, and optional provenance; id/state/slug are derived. An
// existing id (recomputed from domain+statement) is overwritten in place, which
// makes Add idempotent for the same decision and lets the user refine wording.
func (s *Store) Add(_ context.Context, e Entry) (Entry, error) {
	if s == nil {
		return Entry{}, fmt.Errorf("values: store unavailable")
	}
	e.Domain = sanitizeSegment(strings.TrimSpace(e.Domain))
	e.Statement = strings.TrimSpace(e.Statement)
	if e.Domain == "" {
		return Entry{}, fmt.Errorf("values: domain is required")
	}
	if e.Statement == "" {
		return Entry{}, fmt.Errorf("values: statement is required")
	}
	if e.State == "" {
		e.State = StateActive
	}
	if e.Confidence <= 0 {
		e.Confidence = 0.8
	}
	if e.Confidence > 1 {
		e.Confidence = 1
	}
	slug := slugify(e.Statement)
	if slug == "" {
		slug = "value"
	}
	if e.ID == "" {
		e.ID = "v-" + e.Domain + "-" + slug
	}

	path := filepath.Join(s.root, e.Domain, slug+".md")
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Entry{}, fmt.Errorf("values: ensure domain dir: %w", err)
	}
	// Defense in depth: domain/slug are sanitized single segments, but a
	// pre-existing symlink under the root could still redirect the write outside
	// it. Resolve symlinks and refuse to write if the target dir escapes the root.
	if err := ensureWithinRoot(s.root, dir); err != nil {
		return Entry{}, err
	}
	data, err := marshalEntry(e)
	if err != nil {
		return Entry{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Entry{}, fmt.Errorf("values: write entry: %w", err)
	}

	s.mu.Lock()
	index(s.byID, s.byDomain, e)
	s.mu.Unlock()
	return e, nil
}

// Lookup returns the first active entry in a domain, the permission source for
// an autonomous action in that domain. Drifting/retired entries do not permit.
func (s *Store) Lookup(domain string) (Entry, bool) {
	if s == nil {
		return Entry{}, false
	}
	domain = sanitizeSegment(strings.TrimSpace(domain))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, id := range s.byDomain[domain] {
		if e, ok := s.byID[id]; ok && e.State == StateActive {
			return e, true
		}
	}
	return Entry{}, false
}

// List returns all entries sorted by id, for inspection and tests.
func (s *Store) List() []Entry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	out := make([]Entry, 0, len(s.byID))
	for _, e := range s.byID {
		out = append(out, e)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

const (
	defaultDigestMinConfidence = 0.6
	defaultDigestMax           = 12
)

// Digest renders the high-confidence active values for injection into the
// episode system prompt. Returns "" when there is nothing worth injecting.
func (s *Store) Digest() string {
	return s.DigestN(defaultDigestMinConfidence, defaultDigestMax)
}

// DigestN is Digest with explicit thresholds (exposed for tuning and tests).
func (s *Store) DigestN(minConfidence float64, max int) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	var picked []Entry
	for _, e := range s.byID {
		if e.State == StateActive && e.Confidence >= minConfidence {
			picked = append(picked, e)
		}
	}
	s.mu.RUnlock()

	sort.Slice(picked, func(i, j int) bool {
		if picked[i].Confidence != picked[j].Confidence {
			return picked[i].Confidence > picked[j].Confidence
		}
		return picked[i].ID < picked[j].ID
	})
	if max > 0 && len(picked) > max {
		picked = picked[:max]
	}

	var b strings.Builder
	for _, e := range picked {
		fmt.Fprintf(&b, "- [%s] %s (confidence %.2f)\n", e.Domain, compactLine(e.Statement), e.Confidence)
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- markdown frontmatter (de)serialization ---

const frontmatterFence = "---"

// parseEntry parses a value markdown file: a YAML frontmatter block fenced by
// "---" lines, followed by an optional freeform body.
func parseEntry(raw []byte) (Entry, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, frontmatterFence+"\n") {
		return Entry{}, fmt.Errorf("missing frontmatter fence")
	}
	rest := text[len(frontmatterFence)+1:]
	end := strings.Index(rest, "\n"+frontmatterFence)
	if end < 0 {
		return Entry{}, fmt.Errorf("unterminated frontmatter")
	}
	front := rest[:end]
	body := rest[end+len("\n"+frontmatterFence):]
	body = strings.TrimPrefix(body, "\n")

	var e Entry
	if err := yaml.Unmarshal([]byte(front), &e); err != nil {
		return Entry{}, fmt.Errorf("frontmatter yaml: %w", err)
	}
	e.Domain = strings.TrimSpace(e.Domain)
	e.Statement = strings.TrimSpace(e.Statement)
	e.State = strings.TrimSpace(e.State)
	if e.State == "" {
		e.State = StateActive
	}
	e.Body = strings.TrimRight(body, "\n")
	return e, nil
}

// marshalEntry renders an entry as a frontmatter markdown file. The Body field
// is emitted after the closing fence.
func marshalEntry(e Entry) ([]byte, error) {
	front, err := yaml.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("values: marshal frontmatter: %w", err)
	}
	var b strings.Builder
	b.WriteString(frontmatterFence + "\n")
	b.Write(front)
	b.WriteString(frontmatterFence + "\n")
	if body := strings.TrimRight(e.Body, "\n"); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// slugify turns a statement into a short, filesystem-safe slug (lowercase,
// hyphen-joined, first few words).
func slugify(s string) string {
	var words []string
	var cur strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur.WriteRune(r)
		default:
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	if len(words) > 6 {
		words = words[:6]
	}
	return strings.Join(words, "-")
}

// sanitizeSegment keeps a domain usable as a single path segment: ASCII
// [a-z0-9-_] only, no separators, no traversal, no Unicode confusables. It is
// defense-in-depth for the value file path.
func sanitizeSegment(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// ensureWithinRoot resolves symlinks on root and target and verifies target is
// contained within root, so a symlinked path segment cannot redirect a write
// outside the value store.
func ensureWithinRoot(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("values: resolve root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("values: resolve root symlinks: %w", err)
	}
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("values: resolve target symlinks: %w", err)
	}
	rel, err := filepath.Rel(rootReal, targetReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("values: domain path %q escapes the value store root", target)
	}
	return nil
}

func compactLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
