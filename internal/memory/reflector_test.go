package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestReflectionCountTrigger(t *testing.T) {
	rt := &ReflectionTracker{
		cfg: MemoryConfig{ReflectionCountThreshold: 5},
	}
	rt.unreflectedFactCount = 4
	if rt.shouldTrigger() {
		t.Error("should not trigger at 4")
	}
	rt.unreflectedFactCount = 5
	if !rt.shouldTrigger() {
		t.Error("should trigger at 5")
	}
}

func TestReflectionDriftTrigger(t *testing.T) {
	rt := &ReflectionTracker{
		cfg: MemoryConfig{
			ReflectionCountThreshold: 100, // high, so count doesn't trigger
			ReflectionDriftThreshold: 0.7,
		},
	}
	rt.unreflectedFactCount = 3
	// Orthogonal vectors -> cosine sim ~ 0, well below 0.7 threshold
	rt.runningTopicEmbedding = []float32{1, 0, 0}
	rt.lastReflectionTopic = []float32{0, 1, 0}
	if !rt.shouldTrigger() {
		t.Error("should trigger on drift")
	}
}

func TestReflectionNoDriftTrigger(t *testing.T) {
	rt := &ReflectionTracker{
		cfg: MemoryConfig{
			ReflectionCountThreshold: 100,
			ReflectionDriftThreshold: 0.7,
		},
	}
	rt.unreflectedFactCount = 3
	// Same direction -> cosine sim = 1.0, above 0.7 threshold
	rt.runningTopicEmbedding = []float32{1, 0, 0}
	rt.lastReflectionTopic = []float32{1, 0, 0}
	if rt.shouldTrigger() {
		t.Error("should not trigger - no drift")
	}
}

func TestReflectionDefaultThresholds(t *testing.T) {
	rt := &ReflectionTracker{
		cfg: MemoryConfig{}, // zero values -> defaults to 10
	}
	rt.unreflectedFactCount = 9
	if rt.shouldTrigger() {
		t.Error("should not trigger at 9 with default 10")
	}
	rt.unreflectedFactCount = 10
	if !rt.shouldTrigger() {
		t.Error("should trigger at 10 with default 10")
	}
}

func TestUpdateTopicEmbedding(t *testing.T) {
	rt := &ReflectionTracker{}
	// First update initializes
	rt.updateTopicEmbedding([]float32{1.0, 0.0, 0.0})
	if rt.runningTopicEmbedding[0] != 1.0 {
		t.Error("first update should initialize")
	}
	// Second update applies EMA (alpha=0.3):
	// new[0] = 0.3*0.0 + 0.7*1.0 = 0.7
	// new[1] = 0.3*1.0 + 0.7*0.0 = 0.3
	rt.updateTopicEmbedding([]float32{0.0, 1.0, 0.0})
	if rt.runningTopicEmbedding[0] < 0.69 || rt.runningTopicEmbedding[0] > 0.71 {
		t.Errorf("EMA wrong for [0]: got %f, want ~0.7", rt.runningTopicEmbedding[0])
	}
	if rt.runningTopicEmbedding[1] < 0.29 || rt.runningTopicEmbedding[1] > 0.31 {
		t.Errorf("EMA wrong for [1]: got %f, want ~0.3", rt.runningTopicEmbedding[1])
	}
}

// ---------------------------------------------------------------------------
// Mock implementations for reflection tests.
// These are separate from lifecycle_test.go mocks to avoid naming collisions
// while providing the same interface satisfaction.
// ---------------------------------------------------------------------------

// reflectorMockCompleter routes responses by system prompt so that L1 and L2
// reflections can return distinguishable content.
type reflectorMockCompleter struct {
	l1Response string
	l2Response string
}

func (m *reflectorMockCompleter) Complete(_ context.Context, systemPrompt, _ string) (string, error) {
	switch systemPrompt {
	case reflectionSystemPrompt:
		return m.l1Response, nil
	case l2ReflectionSystemPrompt:
		return m.l2Response, nil
	default:
		return "", fmt.Errorf("unexpected system prompt: %s", systemPrompt[:40])
	}
}

// reflectorMockEmbedder returns deterministic embeddings of a fixed dimension.
type reflectorMockEmbedder struct {
	dim   int
	value float32
}

func (m *reflectorMockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, m.dim)
	for i := range v {
		v[i] = m.value
	}
	return v, nil
}
func (m *reflectorMockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		e, _ := m.Embed(context.Background(), texts[i])
		out[i] = e
	}
	return out, nil
}
func (m *reflectorMockEmbedder) Dimensions() int { return m.dim }

// reflectorMockStore records Save calls and supports look-up by ID in Search.
type reflectorMockStore struct {
	saved []Entry
}

func (s *reflectorMockStore) Save(_ context.Context, e Entry) error {
	s.saved = append(s.saved, e)
	return nil
}
func (s *reflectorMockStore) Search(_ context.Context, q SearchQuery) ([]SearchResult, error) {
	// Search by text matching saved entry IDs (used by L2 to load L1 content).
	for _, e := range s.saved {
		if e.ID == q.Text {
			return []SearchResult{{Entry: e, Score: 1.0}}, nil
		}
	}
	return nil, nil
}
func (s *reflectorMockStore) ListByScope(_ context.Context, _ MemoryScope, _ string) ([]Entry, error) {
	return nil, nil
}
func (s *reflectorMockStore) Update(_ context.Context, _ string, _ string, _ int) error { return nil }
func (s *reflectorMockStore) Delete(_ context.Context, _ string) error                  { return nil }

// reflectorMockProfiler records profiler callback invocations.
type reflectorMockProfiler struct {
	calls []struct {
		UserID string
		Level  int
	}
}

func (p *reflectorMockProfiler) OnReflectionCreated(_ context.Context, userID string, level int) error {
	p.calls = append(p.calls, struct {
		UserID string
		Level  int
	}{userID, level})
	return nil
}

// reflectorFailingEmbedder always returns an error.
type reflectorFailingEmbedder struct{ dim int }

func (e *reflectorFailingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, fmt.Errorf("embedding service unavailable")
}
func (e *reflectorFailingEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, fmt.Errorf("embedding service unavailable")
}
func (e *reflectorFailingEmbedder) Dimensions() int { return e.dim }

// ---------------------------------------------------------------------------
// L1 reflection tests
// ---------------------------------------------------------------------------

func TestReflectionL1TriggersAtCountThreshold(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{
		l1Response: "User shows strong preference for Go programming.",
	}

	cfg := MemoryConfig{
		ReflectionCountThreshold: 5,
		ReflectionL2Trigger:      100, // prevent L2
	}
	tracker := NewReflectionTracker(store, comp, nil, cfg, nil)

	ctx := context.Background()

	// Track (threshold-1) facts — no reflection yet.
	for i := 0; i < 4; i++ {
		if err := tracker.Track(ctx, fmt.Sprintf("f%d", i), "some fact", "user1"); err != nil {
			t.Fatalf("Track[%d]: %v", i, err)
		}
	}
	if len(store.saved) != 0 {
		t.Fatalf("expected 0 reflections before threshold, got %d", len(store.saved))
	}

	// The 5th fact crosses the threshold → L1 fires.
	if err := tracker.Track(ctx, "f4", "likes microservices", "user1"); err != nil {
		t.Fatalf("Track[4]: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 L1 reflection, got %d", len(store.saved))
	}

	refl := store.saved[0]
	if refl.Metadata["type"] != "reflection" {
		t.Errorf("metadata type = %q, want 'reflection'", refl.Metadata["type"])
	}
	if refl.Metadata["level"] != "1" {
		t.Errorf("metadata level = %q, want '1'", refl.Metadata["level"])
	}
	if refl.UserID != "user1" {
		t.Errorf("UserID = %q, want 'user1'", refl.UserID)
	}
	if refl.Scope != ScopeUser {
		t.Errorf("Scope = %q, want %q", refl.Scope, ScopeUser)
	}
	if refl.Content != comp.l1Response {
		t.Errorf("Content = %q, want %q", refl.Content, comp.l1Response)
	}
	// source_facts metadata should list all tracked fact IDs.
	srcFacts := refl.Metadata["source_facts"]
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("f%d", i)
		if !strings.Contains(srcFacts, id) {
			t.Errorf("source_facts missing %q: %s", id, srcFacts)
		}
	}
}

func TestReflectionL1ResetsCounterAfterTrigger(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{l1Response: "insight"}
	cfg := MemoryConfig{
		ReflectionCountThreshold: 2,
		ReflectionL2Trigger:      100,
	}
	tracker := NewReflectionTracker(store, comp, nil, cfg, nil)
	ctx := context.Background()

	// First batch → 1 reflection.
	for i := 0; i < 2; i++ {
		_ = tracker.Track(ctx, fmt.Sprintf("a%d", i), "fact", "u1")
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 reflection after first batch, got %d", len(store.saved))
	}

	// Second batch → 2nd reflection (counter was reset).
	for i := 0; i < 2; i++ {
		_ = tracker.Track(ctx, fmt.Sprintf("b%d", i), "fact", "u1")
	}
	if len(store.saved) != 2 {
		t.Fatalf("expected 2 reflections after second batch, got %d", len(store.saved))
	}
}

// ---------------------------------------------------------------------------
// L2 reflection tests
// ---------------------------------------------------------------------------

func TestReflectionL2TriggersAfterL1Threshold(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{
		l1Response: "User focuses on backend patterns.",
		l2Response: "User is an experienced backend developer valuing clean architecture.",
	}
	cfg := MemoryConfig{
		ReflectionCountThreshold: 1, // L1 fires on every fact
		ReflectionL2Trigger:      3, // L2 fires after 3 L1s
	}
	tracker := NewReflectionTracker(store, comp, nil, cfg, nil)
	ctx := context.Background()

	// Generate 3 facts → 3 L1 reflections → 1 L2 reflection.
	for i := 0; i < 3; i++ {
		if err := tracker.Track(ctx, fmt.Sprintf("f%d", i), "content", "user1"); err != nil {
			t.Fatalf("Track[%d]: %v", i, err)
		}
	}

	var l1Count, l2Count int
	for _, e := range store.saved {
		if e.Metadata["type"] != "reflection" {
			continue
		}
		switch e.Metadata["level"] {
		case "1":
			l1Count++
		case "2":
			l2Count++
		}
	}

	if l1Count != 3 {
		t.Errorf("expected 3 L1 reflections, got %d", l1Count)
	}
	if l2Count != 1 {
		t.Errorf("expected 1 L2 reflection, got %d", l2Count)
	}

	// Verify L2 entry references source L1 reflections.
	for _, e := range store.saved {
		if e.Metadata["level"] == "2" {
			src := e.Metadata["source_reflections"]
			if src == "" {
				t.Error("L2 reflection missing source_reflections metadata")
			}
			if e.Content != comp.l2Response {
				t.Errorf("L2 content = %q, want %q", e.Content, comp.l2Response)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Embedder integration tests
// ---------------------------------------------------------------------------

func TestReflectionTrackWithEmbedder(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{l1Response: "Synthetic insight."}
	emb := &reflectorMockEmbedder{dim: 64, value: 0.42}

	cfg := MemoryConfig{
		ReflectionCountThreshold: 2,
		ReflectionL2Trigger:      100,
	}
	tracker := NewReflectionTracker(store, comp, emb, cfg, nil)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := tracker.Track(ctx, fmt.Sprintf("f%d", i), "fact", "user1"); err != nil {
			t.Fatalf("Track[%d]: %v", i, err)
		}
	}

	if len(store.saved) != 1 {
		t.Fatalf("expected 1 reflection, got %d", len(store.saved))
	}

	refl := store.saved[0]
	if len(refl.Embedding) != 64 {
		t.Errorf("embedding dim = %d, want 64", len(refl.Embedding))
	}
	// Topic embedding should have been updated during tracking.
	if len(tracker.runningTopicEmbedding) != 64 {
		t.Errorf("running topic embedding dim = %d, want 64", len(tracker.runningTopicEmbedding))
	}
}

// ---------------------------------------------------------------------------
// Embedder failure graceful degradation
// ---------------------------------------------------------------------------

func TestReflectionTrackGracefulOnEmbedderFailure(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{l1Response: "insight despite embed failure"}
	emb := &reflectorFailingEmbedder{dim: 64}

	cfg := MemoryConfig{
		ReflectionCountThreshold: 2,
		ReflectionL2Trigger:      100,
	}
	tracker := NewReflectionTracker(store, comp, emb, cfg, nil)
	ctx := context.Background()

	// Embedder errors should be logged but not block tracking or reflection.
	for i := 0; i < 2; i++ {
		if err := tracker.Track(ctx, fmt.Sprintf("f%d", i), "fact", "user1"); err != nil {
			t.Fatalf("Track[%d] should not fail on embed error: %v", i, err)
		}
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 reflection despite embed failure, got %d", len(store.saved))
	}
	// Saved reflection should have no embedding (embed failed).
	if len(store.saved[0].Embedding) != 0 {
		t.Errorf("expected empty embedding on save, got dim %d", len(store.saved[0].Embedding))
	}
}

// ---------------------------------------------------------------------------
// Drift trigger via Track with embedder
// ---------------------------------------------------------------------------

func TestReflectionDriftTriggerViaTrack(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{l1Response: "drift insight"}

	// Use embedder that alternates embedding direction to simulate topic drift.
	callIdx := 0
	driftEmbedder := &reflectorToggleEmbedder{callIdx: &callIdx}

	cfg := MemoryConfig{
		ReflectionCountThreshold: 1000, // high: count won't trigger
		ReflectionDriftThreshold: 0.95, // strict: even small drift triggers
		ReflectionL2Trigger:      100,
	}
	tracker := NewReflectionTracker(store, comp, driftEmbedder, cfg, nil)
	ctx := context.Background()

	// First fact — initialises running topic; triggers don't fire (no lastReflectionTopic).
	if err := tracker.Track(ctx, "f0", "Go programming", "user1"); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatal("first fact should not trigger reflection")
	}

	// Manually set lastReflectionTopic to the initial direction (simulates a
	// prior reflection) so that drift detection has a baseline.
	tracker.mu.Lock()
	tracker.lastReflectionTopic = make([]float32, 3)
	copy(tracker.lastReflectionTopic, tracker.runningTopicEmbedding)
	tracker.mu.Unlock()

	// Track facts with orthogonal embeddings → drift below threshold → triggers.
	if err := tracker.Track(ctx, "f1", "cooking recipes", "user1"); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 {
		t.Errorf("expected drift-triggered reflection, got %d saved", len(store.saved))
	}
}

// reflectorToggleEmbedder produces alternating orthogonal embeddings to
// simulate topic drift.
type reflectorToggleEmbedder struct{ callIdx *int }

func (e *reflectorToggleEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	idx := *e.callIdx
	*e.callIdx++
	if idx%2 == 0 {
		return []float32{1, 0, 0}, nil
	}
	return []float32{0, 1, 0}, nil
}
func (e *reflectorToggleEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, nil
}
func (e *reflectorToggleEmbedder) Dimensions() int { return 3 }

// ---------------------------------------------------------------------------
// Profiler callback tests
// ---------------------------------------------------------------------------

func TestReflectionProfilerCallbackInvoked(t *testing.T) {
	store := &reflectorMockStore{}
	comp := &reflectorMockCompleter{
		l1Response: "insight",
		l2Response: "deep insight",
	}
	profiler := &reflectorMockProfiler{}
	cfg := MemoryConfig{
		ReflectionCountThreshold: 1,
		ReflectionL2Trigger:      2,
	}
	tracker := NewReflectionTracker(store, comp, nil, cfg, nil)
	tracker.SetProfilerCallback(profiler)
	ctx := context.Background()

	// 2 facts → 2 L1 reflections → 1 L2 reflection → 3 profiler calls.
	for i := 0; i < 2; i++ {
		_ = tracker.Track(ctx, fmt.Sprintf("f%d", i), "fact", "user1")
	}

	// Expect: L1 callback, L1 callback, L2 callback.
	if len(profiler.calls) != 3 {
		t.Fatalf("expected 3 profiler calls, got %d", len(profiler.calls))
	}
	if profiler.calls[0].Level != 1 || profiler.calls[1].Level != 1 {
		t.Errorf("first two calls should be level 1, got %d and %d",
			profiler.calls[0].Level, profiler.calls[1].Level)
	}
	if profiler.calls[2].Level != 2 {
		t.Errorf("third call should be level 2, got %d", profiler.calls[2].Level)
	}
}

// TODO: Consolidation integration tests (session→user promotion), knowledge graph
// integration tests, and privacy integration tests require file-system fixtures
// and a fully wired LifecycleManager+Store stack. They should be added when a
// shared test harness providing temp directories and pre-seeded Markdown files
// is available.
