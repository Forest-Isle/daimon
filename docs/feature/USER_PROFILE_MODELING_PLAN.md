# 用户建模与个性化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有 Profiler 从全量重写单文件模式改造为多 Section 独立文件、按需增量更新模式，实现结构化用户画像。

**Architecture:** Profile 仍然是 memory 文件（`type: profile`），每个 section 一个独立文件。事实通过两层路由分发到对应 section 的缓冲区，达到阈值后独立触发增量更新。注入时通过专用 `LoadProfileSections` 加载所有 section 拼接为 system prompt 片段，搜索时排除 profile 文件防止重复注入。

**Tech Stack:** Go, SQLite (mattn/go-sqlite3), YAML frontmatter + Markdown

**Design Spec:** `docs/feature/USER_PROFILE_MODELING.md`

---

## Phase 1: Schema + 存储基础

### Task 1: 定义 Profile Section Schema

**Files:**
- Create: `internal/memory/profile_schema.go`
- Test: `internal/memory/profile_schema_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/memory/profile_schema_test.go
package memory

import "testing"

func TestProfileSectionRegistry(t *testing.T) {
	reg := NewProfileSectionRegistry()

	// Should have 6 sections
	sections := reg.All()
	if len(sections) != 6 {
		t.Fatalf("expected 6 sections, got %d", len(sections))
	}

	// P0 sections
	comm, ok := reg.Get("communication")
	if !ok {
		t.Fatal("communication section not found")
	}
	if comm.Priority != 0 {
		t.Errorf("communication priority: want 0, got %d", comm.Priority)
	}
	if comm.FactThreshold != 3 {
		t.Errorf("communication fact threshold: want 3, got %d", comm.FactThreshold)
	}

	ts, ok := reg.Get("tech_stack")
	if !ok {
		t.Fatal("tech_stack section not found")
	}
	if ts.Priority != 0 {
		t.Errorf("tech_stack priority: want 0, got %d", ts.Priority)
	}

	// P2 section
	fb, ok := reg.Get("feedback")
	if !ok {
		t.Fatal("feedback section not found")
	}
	if fb.Priority != 2 {
		t.Errorf("feedback priority: want 2, got %d", fb.Priority)
	}
	if fb.FactThreshold != 8 {
		t.Errorf("feedback fact threshold: want 8, got %d", fb.FactThreshold)
	}
}

func TestProfileSectionRegistry_ByPriority(t *testing.T) {
	reg := NewProfileSectionRegistry()
	sorted := reg.ByPriority()
	if len(sorted) < 2 {
		t.Fatal("expected at least 2 sections")
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Priority < sorted[i-1].Priority {
			t.Errorf("sections not sorted: %s (P%d) before %s (P%d)",
				sorted[i-1].ID, sorted[i-1].Priority, sorted[i].ID, sorted[i].Priority)
		}
	}
}

func TestRouteCategoryToSection(t *testing.T) {
	reg := NewProfileSectionRegistry()

	tests := []struct {
		category string
		want     string
	}{
		{"preference", "communication"},
		{"identity", "identity"},
		{"relationship", "identity"},
		{"task", "projects"},
	}
	for _, tt := range tests {
		got, ok := reg.RouteCategory(tt.category)
		if !ok {
			t.Errorf("RouteCategory(%q) returned not-ok", tt.category)
			continue
		}
		if got != tt.want {
			t.Errorf("RouteCategory(%q) = %q, want %q", tt.category, got, tt.want)
		}
	}

	// "fact" has no direct mapping
	_, ok := reg.RouteCategory("fact")
	if ok {
		t.Error("RouteCategory(\"fact\") should return not-ok")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestProfileSection -v ./internal/memory/`
Expected: FAIL — `NewProfileSectionRegistry` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/memory/profile_schema.go
package memory

import (
	"sort"
	"time"
)

// ProfileSection defines metadata for a single user profile section.
type ProfileSection struct {
	ID            string
	Name          string
	Priority      int           // 0 = highest
	FactThreshold int           // pending facts before triggering update
	TimeThreshold time.Duration // max time since last update before triggering
}

// ProfileSectionRegistry holds all known profile sections and their routing rules.
type ProfileSectionRegistry struct {
	sections     map[string]ProfileSection
	categoryMap  map[string]string // fact category -> section ID
}

// NewProfileSectionRegistry creates a registry with the default section definitions.
func NewProfileSectionRegistry() *ProfileSectionRegistry {
	r := &ProfileSectionRegistry{
		sections: map[string]ProfileSection{
			"communication": {
				ID: "communication", Name: "沟通偏好",
				Priority: 0, FactThreshold: 3, TimeThreshold: 1 * time.Hour,
			},
			"tech_stack": {
				ID: "tech_stack", Name: "技术栈画像",
				Priority: 0, FactThreshold: 3, TimeThreshold: 1 * time.Hour,
			},
			"work_pattern": {
				ID: "work_pattern", Name: "工作模式",
				Priority: 1, FactThreshold: 5, TimeThreshold: 4 * time.Hour,
			},
			"projects": {
				ID: "projects", Name: "项目上下文",
				Priority: 1, FactThreshold: 5, TimeThreshold: 4 * time.Hour,
			},
			"feedback": {
				ID: "feedback", Name: "反馈模式",
				Priority: 2, FactThreshold: 8, TimeThreshold: 24 * time.Hour,
			},
			"identity": {
				ID: "identity", Name: "身份画像",
				Priority: 2, FactThreshold: 8, TimeThreshold: 24 * time.Hour,
			},
		},
		categoryMap: map[string]string{
			"preference":   "communication",
			"identity":     "identity",
			"relationship": "identity",
			"task":         "projects",
		},
	}
	return r
}

// Get returns a section by ID.
func (r *ProfileSectionRegistry) Get(id string) (ProfileSection, bool) {
	s, ok := r.sections[id]
	return s, ok
}

// All returns all sections as a slice.
func (r *ProfileSectionRegistry) All() []ProfileSection {
	out := make([]ProfileSection, 0, len(r.sections))
	for _, s := range r.sections {
		out = append(out, s)
	}
	return out
}

// ByPriority returns sections sorted by priority (ascending), then by ID for stable ordering.
func (r *ProfileSectionRegistry) ByPriority() []ProfileSection {
	out := r.All()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RouteCategory maps a fact category to a section ID.
// Returns ("", false) if no direct mapping exists (caller should use LLM fallback).
func (r *ProfileSectionRegistry) RouteCategory(category string) (string, bool) {
	id, ok := r.categoryMap[category]
	return id, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestProfileSection -v ./internal/memory/`
Expected: PASS

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestRouteCategory -v ./internal/memory/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/profile_schema.go internal/memory/profile_schema_test.go
git commit -m "feat(memory): add profile section schema and registry"
```

---

### Task 2: SearchQuery 支持 ExcludeTypes

**Files:**
- Modify: `internal/memory/store.go:68-76`
- Modify: `internal/memory/file_store.go:169-290`
- Test: `internal/memory/file_store_test.go` (add test case)

- [ ] **Step 1: Write the failing test**

```go
// internal/memory/exclude_types_test.go
package memory

import "testing"

func TestSearchQuery_ExcludeTypes(t *testing.T) {
	q := SearchQuery{
		Text:         "test",
		ExcludeTypes: []string{"profile"},
	}
	if len(q.ExcludeTypes) != 1 || q.ExcludeTypes[0] != "profile" {
		t.Fatalf("ExcludeTypes not set correctly: %v", q.ExcludeTypes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSearchQuery_ExcludeTypes -v ./internal/memory/`
Expected: FAIL — `SearchQuery` has no `ExcludeTypes` field

- [ ] **Step 3: Add ExcludeTypes to SearchQuery**

In `internal/memory/store.go`, add the field to `SearchQuery`:

```go
// SearchQuery defines parameters for memory search.
type SearchQuery struct {
	Text         string
	Embedding    []float32
	Limit        int
	SessionID    string        // optional: scope to session
	UserID       string        // optional: scope to user
	Scopes       []MemoryScope // optional: filter by scope(s)
	TypeFilter   string        // optional: filter by memory type (e.g., "summary")
	ExcludeTypes []string      // optional: exclude memory types (e.g., "profile")
}
```

- [ ] **Step 4: Add ExcludeTypes filtering to FileMemoryStore.Search**

In `internal/memory/file_store.go`, in the `Search` method, add after the existing TypeFilter block (around line 219):

```go
// ExcludeTypes: exclude specific memory types
if len(query.ExcludeTypes) > 0 {
	placeholders := strings.Repeat("?,", len(query.ExcludeTypes))
	placeholders = placeholders[:len(placeholders)-1]
	whereClause = append(whereClause, fmt.Sprintf("memory_type NOT IN (%s)", placeholders))
	for _, t := range query.ExcludeTypes {
		args = append(args, t)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSearchQuery_ExcludeTypes -v ./internal/memory/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/memory/store.go internal/memory/file_store.go internal/memory/exclude_types_test.go
git commit -m "feat(memory): add ExcludeTypes filtering to SearchQuery"
```

---

## Phase 2: 路由 + 分区更新

### Task 3: SectionBuffer — 事实缓冲和触发判断

**Files:**
- Create: `internal/memory/section_buffer.go`
- Test: `internal/memory/section_buffer_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/memory/section_buffer_test.go
package memory

import (
	"testing"
	"time"
)

func TestSectionBuffer_AddAndShouldUpdate(t *testing.T) {
	sec := ProfileSection{
		ID: "communication", Priority: 0,
		FactThreshold: 3, TimeThreshold: 1 * time.Hour,
	}
	buf := NewSectionBuffer(sec)

	// Not enough facts yet
	buf.Add("fact 1")
	buf.Add("fact 2")
	if buf.ShouldUpdate() {
		t.Error("should not trigger with only 2 facts")
	}

	// Reaching threshold
	buf.Add("fact 3")
	if !buf.ShouldUpdate() {
		t.Error("should trigger with 3 facts (threshold=3)")
	}
}

func TestSectionBuffer_TimeThreshold(t *testing.T) {
	sec := ProfileSection{
		ID: "communication", Priority: 0,
		FactThreshold: 100, TimeThreshold: 50 * time.Millisecond,
	}
	buf := NewSectionBuffer(sec)
	buf.Add("fact 1")

	// Not enough time elapsed
	if buf.ShouldUpdate() {
		t.Error("should not trigger yet")
	}

	// Simulate time passing
	buf.lastUpdated = time.Now().Add(-100 * time.Millisecond)
	if !buf.ShouldUpdate() {
		t.Error("should trigger after time threshold")
	}
}

func TestSectionBuffer_Drain(t *testing.T) {
	sec := ProfileSection{
		ID: "tech_stack", Priority: 0,
		FactThreshold: 3, TimeThreshold: 1 * time.Hour,
	}
	buf := NewSectionBuffer(sec)
	buf.Add("fact A")
	buf.Add("fact B")
	buf.Add("fact C")

	facts := buf.Drain()
	if len(facts) != 3 {
		t.Fatalf("expected 3 drained facts, got %d", len(facts))
	}
	if facts[0] != "fact A" {
		t.Errorf("first fact: want %q, got %q", "fact A", facts[0])
	}

	// After drain, buffer should be empty
	if len(buf.pending) != 0 {
		t.Errorf("buffer should be empty after drain, got %d", len(buf.pending))
	}
	if buf.ShouldUpdate() {
		t.Error("should not trigger after drain")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSectionBuffer -v ./internal/memory/`
Expected: FAIL — `NewSectionBuffer` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/memory/section_buffer.go
package memory

import (
	"sync"
	"time"
)

// SectionBuffer accumulates facts for a single profile section and
// determines when to trigger an update based on count and time thresholds.
type SectionBuffer struct {
	section     ProfileSection
	mu          sync.Mutex
	pending     []string
	lastUpdated time.Time
}

// NewSectionBuffer creates a buffer for the given section.
func NewSectionBuffer(section ProfileSection) *SectionBuffer {
	return &SectionBuffer{
		section:     section,
		lastUpdated: time.Now(),
	}
}

// Add appends a fact to the pending buffer.
func (b *SectionBuffer) Add(fact string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, fact)
}

// ShouldUpdate returns true if the buffer has accumulated enough facts
// or enough time has passed since the last update.
func (b *SectionBuffer) ShouldUpdate() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.pending) == 0 {
		return false
	}
	if len(b.pending) >= b.section.FactThreshold {
		return true
	}
	if time.Since(b.lastUpdated) >= b.section.TimeThreshold {
		return true
	}
	return false
}

// Drain returns all pending facts and resets the buffer.
func (b *SectionBuffer) Drain() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]string, len(b.pending))
	copy(out, b.pending)
	b.pending = b.pending[:0]
	b.lastUpdated = time.Now()
	return out
}

// PendingCount returns the number of facts waiting in the buffer.
func (b *SectionBuffer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestSectionBuffer -v ./internal/memory/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/section_buffer.go internal/memory/section_buffer_test.go
git commit -m "feat(memory): add SectionBuffer for per-section fact accumulation"
```

---

### Task 4: 重构 Profiler — 事实路由 + Section 更新 + LoadProfileSections

**Files:**
- Modify: `internal/memory/profiler.go`
- Test: `internal/memory/profiler_test.go`

- [ ] **Step 1: Write the failing test for fact routing**

```go
// internal/memory/profiler_test.go
package memory

import (
	"context"
	"testing"
)

type mockCompleter struct {
	response string
	err      error
}

func (m *mockCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

func TestProfiler_RouteFactToSection_DirectMapping(t *testing.T) {
	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
	}
	// Initialize buffers
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	// Route a preference fact — should go to "communication"
	p.RouteFact(context.Background(), ExtractedFact{
		Content:  "user prefers concise answers",
		Category: "preference",
	})
	if p.buffers["communication"].PendingCount() != 1 {
		t.Error("preference fact should route to communication section")
	}

	// Route a task fact — should go to "projects"
	p.RouteFact(context.Background(), ExtractedFact{
		Content:  "user is working on IronClaw",
		Category: "task",
	})
	if p.buffers["projects"].PendingCount() != 1 {
		t.Error("task fact should route to projects section")
	}
}

func TestLoadProfileSections_Empty(t *testing.T) {
	dir := t.TempDir()
	result, err := LoadProfileSections(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result for empty dir, got %q", result)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestProfiler_RouteFact|TestLoadProfileSections" -v ./internal/memory/`
Expected: FAIL — `RouteFact`, `LoadProfileSections` not defined, `Profiler` missing new fields

- [ ] **Step 3: Refactor Profiler**

Rewrite `internal/memory/profiler.go` to add:
- `registry *ProfileSectionRegistry` and `buffers map[string]*SectionBuffer` fields to `Profiler`
- `RouteFact(ctx, fact)` — routes fact to section buffer using two-layer routing
- `UpdateSection(ctx, sectionID, userID)` — loads current section file, calls LLM for incremental update, saves new file
- `CheckAndUpdateSections(ctx, userID)` — iterates all buffers, triggers updates for those meeting threshold
- Preserve existing `OnReflectionCreated` but change it to call `CheckAndUpdateSections` instead of `GenerateProfile`
- Preserve `GenerateProfile` as a fallback/manual trigger
- Add `LoadProfileSections(baseDir string) (string, error)` — scans `user/` for `type: profile` files, sorts by priority, concatenates with confidence labels, applies 800 token budget

Key changes to `profiler.go`:

```go
// Updated Profiler struct
type Profiler struct {
	store     Store
	completer Completer
	db        *sql.DB
	baseDir   string
	cfg       MemoryConfig

	mu       sync.Mutex
	registry *ProfileSectionRegistry
	buffers  map[string]*SectionBuffer
}

// NewProfiler now initializes the section registry and buffers.
func NewProfiler(store Store, completer Completer, db *sql.DB, baseDir string, cfg MemoryConfig) *Profiler {
	reg := NewProfileSectionRegistry()
	buffers := make(map[string]*SectionBuffer, len(reg.All()))
	for _, sec := range reg.All() {
		buffers[sec.ID] = NewSectionBuffer(sec)
	}
	return &Profiler{
		store:     store,
		completer: completer,
		db:        db,
		baseDir:   baseDir,
		cfg:       cfg,
		registry:  reg,
		buffers:   buffers,
	}
}

// RouteFact routes an extracted fact to the appropriate section buffer.
// Layer 1: category direct mapping. Layer 2: LLM classification (for "fact" category).
func (p *Profiler) RouteFact(ctx context.Context, fact ExtractedFact) {
	sectionID, ok := p.registry.RouteCategory(fact.Category)
	if !ok {
		// Layer 2: LLM classification
		sectionID = p.classifyFactByLLM(ctx, fact.Content)
		if sectionID == "" {
			return // "none" — not a profile-relevant fact
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if buf, exists := p.buffers[sectionID]; exists {
		buf.Add(fact.Content)
	}
}

// classifyFactByLLM asks the LLM to route a fact to a section ID.
func (p *Profiler) classifyFactByLLM(ctx context.Context, content string) string {
	if p.completer == nil {
		return ""
	}
	prompt := `Given this fact about a user, determine which profile section it belongs to.
Sections: communication, tech_stack, work_pattern, projects, feedback, identity, none
Reply with ONLY the section name, nothing else.
If the fact doesn't belong to any profile section, reply "none".`

	resp, err := p.completer.Complete(ctx, prompt, content)
	if err != nil {
		return ""
	}
	resp = strings.TrimSpace(strings.ToLower(resp))
	if _, ok := p.registry.Get(resp); ok {
		return resp
	}
	return ""
}

// CheckAndUpdateSections checks all buffers and triggers updates for sections meeting their threshold.
func (p *Profiler) CheckAndUpdateSections(ctx context.Context, userID string) error {
	for sectionID, buf := range p.buffers {
		if buf.ShouldUpdate() {
			if err := p.UpdateSection(ctx, sectionID, userID); err != nil {
				slog.Warn("profile section update failed", "section", sectionID, "error", err)
			}
		}
	}
	return nil
}

// UpdateSection performs an incremental update of a single profile section.
func (p *Profiler) UpdateSection(ctx context.Context, sectionID, userID string) error {
	buf, exists := p.buffers[sectionID]
	if !exists {
		return fmt.Errorf("unknown section: %s", sectionID)
	}

	facts := buf.Drain()
	if len(facts) == 0 {
		return nil
	}

	section, _ := p.registry.Get(sectionID)
	profileID := fmt.Sprintf("profile_%s", sectionID)
	profilePath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", sectionID))

	// Load existing section content
	var currentContent string
	var currentEvidenceCount int
	if existing, err := parseMemoryFile(profilePath); err == nil {
		currentContent = existing.Content
		if ecStr, ok := existing.Metadata["evidence_count"]; ok {
			currentEvidenceCount, _ = strconv.Atoi(ecStr)
		}
	}

	// Build LLM prompt for incremental update
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("当前 %s 画像:\n", section.Name))
	if currentContent != "" {
		sb.WriteString(currentContent)
	} else {
		sb.WriteString("(空 — 首次建立)")
	}
	sb.WriteString(fmt.Sprintf("\n\n新观察 (%d 条):\n", len(facts)))
	for _, f := range facts {
		sb.WriteString("- ")
		sb.WriteString(f)
		sb.WriteString("\n")
	}

	systemPrompt := fmt.Sprintf(`你是用户画像维护助手。根据以下新观察，更新用户的「%s」画像。

规则:
1. 保留当前画像中仍然成立的信息
2. 整合新观察，如有矛盾以更近期的观察为准
3. 用简洁的要点列表格式输出
4. 如果新观察不改变当前画像，原样返回当前画像内容
5. 只输出画像内容，不要输出其他文字`, section.Name)

	newContent, err := p.completer.Complete(ctx, systemPrompt, sb.String())
	if err != nil {
		// Re-queue facts on failure
		for _, f := range facts {
			buf.Add(f)
		}
		return fmt.Errorf("LLM section update: %w", err)
	}

	// Calculate new metadata
	newEvidenceCount := currentEvidenceCount + len(facts)
	confidence := float64(newEvidenceCount) * 0.1
	if confidence > 1.0 {
		confidence = 1.0
	}

	now := time.Now()
	mf := MemoryFile{
		ID:        profileID,
		Scope:     "user",
		Type:      "profile",
		CreatedAt: now,
		UpdatedAt: now,
		Strength:  1.0,
		Metadata: map[string]string{
			"type":           "profile",
			"section":        sectionID,
			"priority":       strconv.Itoa(section.Priority),
			"confidence":     fmt.Sprintf("%.1f", confidence),
			"evidence_count": strconv.Itoa(newEvidenceCount),
		},
		Content: strings.TrimSpace(newContent),
	}

	// Preserve original created_at if file exists
	if existing, err := parseMemoryFile(profilePath); err == nil {
		mf.CreatedAt = existing.CreatedAt
		// Archive old version
		archivedPath := filepath.Join(p.baseDir, "archived", filepath.Base(profilePath))
		_ = os.Rename(profilePath, archivedPath)
	}

	// Write new file
	if err := writeProfileAtomic(profilePath, mf); err != nil {
		return fmt.Errorf("write profile section: %w", err)
	}

	// Sync to index
	entry := Entry{
		ID:        profileID,
		Scope:     ScopeUser,
		Content:   mf.Content,
		CreatedAt: mf.CreatedAt,
		UpdatedAt: now,
		Metadata:  mf.Metadata,
	}
	return p.store.Save(ctx, entry)
}

// LoadProfileSections scans for all type=profile memory files, sorts by priority,
// and returns a formatted string for system prompt injection.
func LoadProfileSections(baseDir string) (string, error) {
	userDir := filepath.Join(baseDir, "user")
	files, err := filepath.Glob(filepath.Join(userDir, "profile_*.md"))
	if err != nil || len(files) == 0 {
		return "", err
	}

	type sectionEntry struct {
		priority   int
		confidence float64
		name       string
		content    string
	}

	var sections []sectionEntry
	for _, f := range files {
		mf, err := parseMemoryFile(f)
		if err != nil || mf.Type != "profile" {
			continue
		}
		priority := 99
		if p, ok := mf.Metadata["priority"]; ok {
			priority, _ = strconv.Atoi(p)
		}
		confidence := 0.0
		if c, ok := mf.Metadata["confidence"]; ok {
			confidence, _ = strconv.ParseFloat(c, 64)
		}
		name := mf.Metadata["section"]
		if name == "" {
			name = mf.ID
		}
		sections = append(sections, sectionEntry{
			priority:   priority,
			confidence: confidence,
			name:       name,
			content:    mf.Content,
		})
	}

	if len(sections) == 0 {
		return "", nil
	}

	// Sort by priority ascending
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].priority != sections[j].priority {
			return sections[i].priority < sections[j].priority
		}
		return sections[i].name < sections[j].name
	})

	// Build output with token budget (~800 tokens ≈ 3200 chars)
	const maxChars = 3200
	var out strings.Builder
	for _, s := range sections {
		label := ""
		if s.confidence < 0.5 {
			label = " (初步观察)"
		}
		header := fmt.Sprintf("## %s%s\n", s.name, label)
		entry := header + s.content + "\n\n"
		if out.Len()+len(entry) > maxChars {
			break
		}
		out.WriteString(entry)
	}

	return strings.TrimSpace(out.String()), nil
}
```

- [ ] **Step 4: Update OnReflectionCreated to use new routing**

Change `OnReflectionCreated` to call `CheckAndUpdateSections` after the reflection count threshold is reached, instead of `GenerateProfile`:

```go
func (p *Profiler) OnReflectionCreated(ctx context.Context, userID string, level int) error {
	if level != 1 {
		return nil
	}
	return p.CheckAndUpdateSections(ctx, userID)
}
```

- [ ] **Step 5: Run all profiler tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run "TestProfiler|TestLoadProfileSections" -v ./internal/memory/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/memory/profiler.go internal/memory/profiler_test.go
git commit -m "feat(memory): refactor Profiler with section routing, buffered updates, and LoadProfileSections"
```

---

## Phase 3: 注入闭环

### Task 5: Simple mode 注入改造

**Files:**
- Modify: `internal/agent/runtime.go:606-630`

- [ ] **Step 1: Modify buildSystemPrompt to use LoadProfileSections**

In `internal/agent/runtime.go`, replace the existing profile loading block:

Old code (lines 621-630):

```go
// 5. User profile (loaded from memory base dir)
if r.memoryBaseDir != "" {
	profileContent, err := memory.LoadUserProfile(r.memoryBaseDir, "default")
	if err == nil && profileContent != "" {
		sb.WriteString("\n\n## User Context\n")
		sb.WriteString(profileContent)
	}
}
```

New code:

```go
// 5. User profile (loaded from profile sections)
if r.memoryBaseDir != "" {
	profileContent, err := memory.LoadProfileSections(r.memoryBaseDir)
	if err == nil && profileContent != "" {
		sb.WriteString("\n\n## User Profile\n")
		sb.WriteString(profileContent)
	}
}
```

- [ ] **Step 2: Add ExcludeTypes to the memory search in buildSystemPrompt**

Also update the memory search (lines 607-618) to exclude profile type:

Old code:

```go
results, err := r.memStore.Search(ctx, memory.SearchQuery{Text: userText, Limit: 5})
```

New code:

```go
results, err := r.memStore.Search(ctx, memory.SearchQuery{
	Text:         userText,
	Limit:        5,
	ExcludeTypes: []string{"profile"},
})
```

- [ ] **Step 3: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
git add internal/agent/runtime.go
git commit -m "feat(agent): simple mode uses LoadProfileSections with ExcludeTypes"
```

---

### Task 6: Cognitive mode 注入改造

**Files:**
- Modify: `internal/agent/cognitive_types.go:68-87`
- Modify: `internal/agent/perceive.go:77-96`
- Modify: `internal/agent/cognitive_prompts.go:40-68`
- Modify: `internal/agent/plan.go:161-207`

- [ ] **Step 1: Add UserProfile field to CognitiveState**

In `internal/agent/cognitive_types.go`, add after the `StrategyHints` field (line 82):

```go
UserProfile     string   // structured user profile sections for prompt injection
```

- [ ] **Step 2: Load profile sections in Perceiver.Run**

In `internal/agent/perceive.go`, after the memory retrieval block (after line 96), add:

```go
// Load user profile sections for dedicated injection
var userProfile string
if p.memBaseDir != "" {
	profileContent, err := memory.LoadProfileSections(p.memBaseDir)
	if err != nil {
		slog.Warn("perceive: load profile sections failed", "err", err)
	} else {
		userProfile = profileContent
	}
}
```

Then in the `CognitiveState` construction (where fields are set), add:

```go
UserProfile: userProfile,
```

The `Perceiver` struct needs a `memBaseDir string` field. Add it and update `NewPerceiver` to accept it.

- [ ] **Step 3: Add ExcludeTypes to PERCEIVE memory search**

In `internal/agent/perceive.go`, update the Search call:

```go
memories, err = p.memStore.Search(ctx, memory.SearchQuery{
	Text:         userMsg,
	Limit:        5,
	UserID:       userID,
	Scopes:       []memory.MemoryScope{memory.ScopeSession, memory.ScopeUser},
	ExcludeTypes: []string{"profile"},
})
```

- [ ] **Step 4: Add {{USER_PROFILE}} to PlanUserPromptTemplate**

In `internal/agent/cognitive_prompts.go`, add a new section to the template between `GIT STATE` and `RECENT CONVERSATION`:

```
USER PROFILE:
{{USER_PROFILE}}
```

Update the comment on line 41:

```go
// Placeholders: {{USER_REQUEST}}, {{TOOLS}}, {{MEMORIES}}, {{HISTORY}}, {{KNOWLEDGE}}, {{GRAPH}}, {{PROJECT_CONTEXT}}, {{GIT_STATE}}, {{USER_PROFILE}}
```

- [ ] **Step 5: Substitute {{USER_PROFILE}} in buildPlanUserMessage**

In `internal/agent/plan.go`, add after the `{{STRATEGY}}` replacement (around line 207):

```go
// User profile section
userProfile := "(none)"
if state.UserProfile != "" {
	userProfile = state.UserProfile
}
msg = strings.ReplaceAll(msg, "{{USER_PROFILE}}", userProfile)
```

- [ ] **Step 6: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 7: Commit**

```bash
git add internal/agent/cognitive_types.go internal/agent/perceive.go internal/agent/cognitive_prompts.go internal/agent/plan.go
git commit -m "feat(agent): cognitive mode injects user profile via dedicated template variable"
```

---

### Task 7: Gateway 接线 — Profiler 路由集成

**Files:**
- Modify: `internal/gateway/init_memory.go:74-94`
- Modify: `internal/agent/runtime.go` (add RouteFact call in fact processing)

- [ ] **Step 1: Wire Profiler into fact processing pipeline**

The key integration point is where extracted facts flow into the lifecycle manager. After `LifecycleManager.Process` succeeds, the fact should also be routed to the Profiler. 

In `internal/agent/runtime.go`, find the fact processing loop (around line 443-477 where `lifecycleMgr.Process` is called), and add after each fact is processed:

```go
if r.profiler != nil {
	r.profiler.RouteFact(ctx, fact)
}
```

Add a `profiler` field and `SetProfiler` method to the runtime:

```go
func (r *Runtime) SetProfiler(p *memory.Profiler) {
	r.profiler = p
}
```

- [ ] **Step 2: Wire Profiler in gateway init**

In `internal/gateway/init_memory.go`, after creating the profiler (line 92), wire it to the runtime:

```go
profiler := memory.NewProfiler(gw.memStore, completer, gw.db.DB, storageDir, memCfg)
reflector.SetProfilerCallback(profiler)
gw.runtime.SetProfiler(profiler)
slog.Info("memory: profiler created and wired to reflection tracker and runtime")
```

- [ ] **Step 3: Wire memBaseDir to Perceiver**

In the gateway code where `NewPerceiver` is called, pass `storageDir` so the PERCEIVE phase can load profile sections.

- [ ] **Step 4: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/init_memory.go internal/agent/runtime.go
git commit -m "feat(gateway): wire Profiler fact routing into runtime and perceiver"
```

---

## Phase 4: 冷启动 + 打磨

### Task 8: 冷启动探查模式

**Files:**
- Modify: `internal/memory/profiler.go`
- Test: `internal/memory/profiler_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestProfiler_ColdStartPrompt(t *testing.T) {
	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
		baseDir:  t.TempDir(),
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	prompt := p.ColdStartPrompt()
	if prompt == "" {
		t.Error("expected non-empty cold start prompt for empty profile dir")
	}

	// Create a profile file to simulate non-empty state
	userDir := filepath.Join(p.baseDir, "user")
	os.MkdirAll(userDir, 0755)
	mf := MemoryFile{
		ID: "profile_communication", Scope: "user", Type: "profile",
		CreatedAt: time.Now(), UpdatedAt: time.Now(), Strength: 1.0,
		Metadata: map[string]string{
			"type": "profile", "section": "communication",
			"confidence": "0.8", "evidence_count": "10",
		},
		Content: "test content",
	}
	writeProfileAtomic(filepath.Join(userDir, "profile_communication.md"), mf)

	prompt = p.ColdStartPrompt()
	// With at least one high-confidence section, cold start should not trigger
	// (exact logic depends on threshold, but concept is testable)
}
```

- [ ] **Step 2: Implement ColdStartPrompt**

```go
// ColdStartPrompt returns a system prompt snippet to guide the agent in learning
// about the user during early interactions. Returns "" if the profile is already
// sufficiently populated.
func (p *Profiler) ColdStartPrompt() string {
	userDir := filepath.Join(p.baseDir, "user")
	files, _ := filepath.Glob(filepath.Join(userDir, "profile_*.md"))

	highConfCount := 0
	for _, f := range files {
		mf, err := parseMemoryFile(f)
		if err != nil || mf.Type != "profile" {
			continue
		}
		if c, ok := mf.Metadata["confidence"]; ok {
			conf, _ := strconv.ParseFloat(c, 64)
			if conf >= 0.5 {
				highConfCount++
			}
		}
	}

	if highConfCount >= 3 {
		return ""
	}

	return `[Profile Building Mode]
你对当前用户的了解还很少。在自然对话中，注意观察并记录以下信息：
- 用户使用的语言和沟通风格
- 提到的技术栈和工具
- 工作方式和偏好
不要直接询问这些信息，而是从交互中自然提取。`
}
```

- [ ] **Step 3: Integrate ColdStartPrompt into system prompt building**

In `internal/agent/runtime.go` `buildSystemPrompt`, after the profile injection block, add:

```go
if r.profiler != nil {
	if coldStart := r.profiler.ColdStartPrompt(); coldStart != "" {
		sb.WriteString("\n\n")
		sb.WriteString(coldStart)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" -run TestProfiler_ColdStart -v ./internal/memory/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/profiler.go internal/memory/profiler_test.go internal/agent/runtime.go
git commit -m "feat(memory): add cold-start prompt for profile building mode"
```

---

### Task 9: 现有单文件 Profile 迁移

**Files:**
- Modify: `internal/memory/profiler.go`

- [ ] **Step 1: Write migration function**

```go
// MigrateLegacyProfile converts a legacy single-file profile (profile_<userID>.md)
// into the new multi-section format. Existing content is split into sections
// based on the old header format (## Identity, ## Preferences, ## Current Focus).
func (p *Profiler) MigrateLegacyProfile(ctx context.Context, userID string) error {
	oldPath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", userID))
	mf, err := parseMemoryFile(oldPath)
	if err != nil {
		return nil // no legacy profile, nothing to migrate
	}

	// Map old sections to new section IDs
	sectionMap := map[string]string{
		"Identity":      "identity",
		"Preferences":   "communication",
		"Current Focus": "projects",
	}

	parts := strings.Split(mf.Content, "## ")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lines := strings.SplitN(part, "\n", 2)
		header := strings.TrimSpace(lines[0])
		content := ""
		if len(lines) > 1 {
			content = strings.TrimSpace(lines[1])
		}
		if sectionID, ok := sectionMap[header]; ok && content != "" {
			sec, _ := p.registry.Get(sectionID)
			profileID := fmt.Sprintf("profile_%s", sectionID)
			profilePath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", sectionID))
			now := time.Now()
			newMF := MemoryFile{
				ID: profileID, Scope: "user", Type: "profile",
				CreatedAt: now, UpdatedAt: now, Strength: 1.0,
				Metadata: map[string]string{
					"type": "profile", "section": sectionID,
					"priority":       strconv.Itoa(sec.Priority),
					"confidence":     "0.3",
					"evidence_count": "3",
				},
				Content: content,
			}
			if err := writeProfileAtomic(profilePath, newMF); err != nil {
				slog.Warn("migration: failed to save section", "section", sectionID, "error", err)
			}
		}
	}

	// Archive the old profile
	archivedPath := filepath.Join(p.baseDir, "archived", filepath.Base(oldPath))
	_ = os.Rename(oldPath, archivedPath)

	slog.Info("legacy profile migrated", "user_id", userID)
	return nil
}
```

- [ ] **Step 2: Call migration on startup**

In `internal/gateway/init_memory.go`, after creating the profiler, trigger migration:

```go
if err := profiler.MigrateLegacyProfile(context.Background(), "default"); err != nil {
	slog.Warn("memory: legacy profile migration failed", "err", err)
}
```

- [ ] **Step 3: Build and verify**

Run: `CGO_ENABLED=1 go build -tags "fts5" ./cmd/ironclaw/`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
git add internal/memory/profiler.go internal/gateway/init_memory.go
git commit -m "feat(memory): migrate legacy single-file profile to multi-section format"
```

---

### Task 10: 全量测试 + 文档更新

- [ ] **Step 1: Run all tests**

Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/memory/ -v`
Run: `CGO_ENABLED=1 go test -tags "fts5" ./internal/agent/ -v`
Expected: All PASS

- [ ] **Step 2: Run lint**

Run: `make lint`
Expected: No new lint errors

- [ ] **Step 3: Update CLAUDE.md**

Add to the Memory System section:

```markdown
**User Profile**: Structured multi-section profile stored as `user/profile_<section>.md` files (type: profile). Sections: communication, tech_stack, work_pattern, projects, feedback, identity. Facts route to sections via category mapping + LLM fallback. Each section updates independently based on priority-specific thresholds. Profile injected into system prompt via `LoadProfileSections()`, excluded from regular memory search via `ExcludeTypes`.
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md with user profile system description"
```
