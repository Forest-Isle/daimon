package memory

import "testing"

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

// TODO: Tasks 3.11-3.12 (consolidation integration tests), 4.12-4.14 (knowledge graph
// integration tests), and 5.17-5.18 (privacy integration tests) require complex DB setup
// and LLM mocking. These should be added when a test harness with proper fixtures is available.
