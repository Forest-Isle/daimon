package rl

import (
	"context"
	"testing"
)

type mockTrainerBuffer struct {
	experiences []Experience
}

func (m *mockTrainerBuffer) AddExperience(exp Experience) {
	m.experiences = append(m.experiences, exp)
}

func TestMemoryRLHandlerOnAdd(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryAdd(context.Background(), "fact1", "user likes Go", 7)
	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	exp := buf.experiences[0]
	if exp.Level != LevelBandit {
		t.Errorf("level: got %s, want %s", exp.Level, LevelBandit)
	}
	if exp.Reward != 0.1 {
		t.Errorf("reward: got %f, want 0.1", exp.Reward)
	}
	if !exp.Done {
		t.Error("expected done=true")
	}
}

func TestMemoryRLHandlerOnUpdate(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryUpdate(context.Background(), "old1", "new1", "updated info")
	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	if buf.experiences[0].Reward != 0.3 {
		t.Errorf("reward: got %f, want 0.3", buf.experiences[0].Reward)
	}
}

func TestMemoryRLHandlerOnDelete(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryDelete(context.Background(), "fact1")
	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	if buf.experiences[0].Reward != 0.2 {
		t.Errorf("reward: got %f, want 0.2", buf.experiences[0].Reward)
	}
}

func TestMemoryRLHandlerOnConflict(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryConflict(context.Background(), "fact1", []string{"c1", "c2"})
	if len(buf.experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(buf.experiences))
	}
	if buf.experiences[0].Reward != -0.5 {
		t.Errorf("reward: got %f, want -0.5", buf.experiences[0].Reward)
	}
}

func TestMemoryRLHandlerNilBuffer(t *testing.T) {
	h := NewMemoryRLHandler(nil, DefaultMemoryRLRewards())
	h.OnMemoryAdd(context.Background(), "fact1", "content", 5)
	// No panic = pass
}

func TestMemoryRLHandlerExperienceStructure(t *testing.T) {
	buf := &mockTrainerBuffer{}
	h := NewMemoryRLHandler(buf, DefaultMemoryRLRewards())
	h.OnMemoryAdd(context.Background(), "fact1", "content", 5)

	exp := buf.experiences[0]
	if exp.State == nil {
		t.Error("expected non-nil State")
	}
	if len(exp.Action) == 0 {
		t.Error("expected non-empty Action vector")
	}
}

func TestMemoryRLHandlerAllEventsNilBuffer(t *testing.T) {
	h := NewMemoryRLHandler(nil, DefaultMemoryRLRewards())
	// All methods must be safe to call with nil adder.
	h.OnMemoryUpdate(context.Background(), "old", "new", "content")
	h.OnMemoryDelete(context.Background(), "fact1")
	h.OnMemoryConflict(context.Background(), "fact1", []string{"c1"})
	// No panic = pass
}

func TestDefaultMemoryRLRewards(t *testing.T) {
	r := DefaultMemoryRLRewards()
	if r.AddReward != 0.1 {
		t.Errorf("AddReward: got %f, want 0.1", r.AddReward)
	}
	if r.UpdateReward != 0.3 {
		t.Errorf("UpdateReward: got %f, want 0.3", r.UpdateReward)
	}
	if r.DeleteReward != 0.2 {
		t.Errorf("DeleteReward: got %f, want 0.2", r.DeleteReward)
	}
	if r.ConflictReward != -0.5 {
		t.Errorf("ConflictReward: got %f, want -0.5", r.ConflictReward)
	}
}

func TestMemoryRLHandlerCustomRewards(t *testing.T) {
	buf := &mockTrainerBuffer{}
	custom := MemoryRLRewards{
		AddReward:      1.0,
		UpdateReward:   2.0,
		DeleteReward:   3.0,
		ConflictReward: -1.0,
	}
	h := NewMemoryRLHandler(buf, custom)

	h.OnMemoryAdd(context.Background(), "id1", "c", 1)
	h.OnMemoryUpdate(context.Background(), "old", "new", "c")
	h.OnMemoryDelete(context.Background(), "id1")
	h.OnMemoryConflict(context.Background(), "new", []string{"old"})

	rewards := []float64{1.0, 2.0, 3.0, -1.0}
	for i, want := range rewards {
		if buf.experiences[i].Reward != want {
			t.Errorf("experience[%d] reward: got %f, want %f", i, buf.experiences[i].Reward, want)
		}
	}
}
