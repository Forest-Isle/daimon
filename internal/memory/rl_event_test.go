package memory

import (
	"context"
	"testing"
)

// mockRLEventHandler records calls for testing.
type mockRLEventHandler struct {
	addCalls      []rlAddEvent
	updateCalls   []rlUpdateEvent
	deleteCalls   []rlDeleteEvent
	conflictCalls []rlConflictEvent
}

type rlAddEvent struct {
	factID, content string
	importance      int
}

type rlUpdateEvent struct {
	oldID, newID, content string
}

type rlDeleteEvent struct {
	factID string
}

type rlConflictEvent struct {
	factID      string
	conflictIDs []string
}

func (m *mockRLEventHandler) OnMemoryAdd(_ context.Context, factID, content string, importance int) {
	m.addCalls = append(m.addCalls, rlAddEvent{factID, content, importance})
}

func (m *mockRLEventHandler) OnMemoryUpdate(_ context.Context, oldID, newID, content string) {
	m.updateCalls = append(m.updateCalls, rlUpdateEvent{oldID, newID, content})
}

func (m *mockRLEventHandler) OnMemoryDelete(_ context.Context, factID string) {
	m.deleteCalls = append(m.deleteCalls, rlDeleteEvent{factID})
}

func (m *mockRLEventHandler) OnMemoryConflict(_ context.Context, factID string, conflictIDs []string) {
	m.conflictCalls = append(m.conflictCalls, rlConflictEvent{factID, conflictIDs})
}

func TestMockRLEventHandlerSatisfiesInterface(t *testing.T) {
	var h RLEventHandler = &mockRLEventHandler{}
	if h == nil {
		t.Fatal("mock does not implement RLEventHandler")
	}
	h.OnMemoryAdd(context.Background(), "id1", "test content", 5)
	h.OnMemoryUpdate(context.Background(), "id1", "id2", "updated content")
	h.OnMemoryDelete(context.Background(), "id1")
	h.OnMemoryConflict(context.Background(), "id1", []string{"id2", "id3"})
}

// TestLifecycleManagerEmitsRLEvents verifies SetRLEventHandler stores the handler.
func TestLifecycleManagerEmitsRLEvents(t *testing.T) {
	handler := &mockRLEventHandler{}
	lm := &LifecycleManager{}
	lm.SetRLEventHandler(handler)
	if lm.rlHandler != handler {
		t.Fatal("rlHandler not set")
	}
}
