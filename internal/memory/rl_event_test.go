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
	content     string
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

func (m *mockRLEventHandler) OnMemoryConflict(_ context.Context, content string, conflictIDs []string) {
	m.conflictCalls = append(m.conflictCalls, rlConflictEvent{content, conflictIDs})
}

func TestMockRLEventHandlerSatisfiesInterface(t *testing.T) {
	// Compile-time interface check.
	var _ RLEventHandler = (*mockRLEventHandler)(nil)

	h := &mockRLEventHandler{}
	h.OnMemoryAdd(context.Background(), "id1", "test content", 5)
	h.OnMemoryUpdate(context.Background(), "id1", "id2", "updated content")
	h.OnMemoryDelete(context.Background(), "id1")
	h.OnMemoryConflict(context.Background(), "conflicting fact content", []string{"id2", "id3"})

	if len(h.addCalls) != 1 || h.addCalls[0].factID != "id1" {
		t.Errorf("OnMemoryAdd not recorded correctly: %+v", h.addCalls)
	}
	if len(h.updateCalls) != 1 || h.updateCalls[0].oldID != "id1" {
		t.Errorf("OnMemoryUpdate not recorded correctly: %+v", h.updateCalls)
	}
	if len(h.deleteCalls) != 1 || h.deleteCalls[0].factID != "id1" {
		t.Errorf("OnMemoryDelete not recorded correctly: %+v", h.deleteCalls)
	}
	if len(h.conflictCalls) != 1 || len(h.conflictCalls[0].conflictIDs) != 2 {
		t.Errorf("OnMemoryConflict not recorded correctly: %+v", h.conflictCalls)
	}
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
