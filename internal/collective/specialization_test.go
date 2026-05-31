package collective

import (
	"testing"
)

func TestNewSpecializationEngine(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)
	if se == nil {
		t.Fatal("NewSpecializationEngine returned nil")
	}
	if se.minTasks != 5 {
		t.Errorf("minTasks = %d, want 5", se.minTasks)
	}
	if se.minScore != 0.65 {
		t.Errorf("minScore = %f, want 0.65", se.minScore)
	}
}

func TestDetectRoles_NoAgents(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)
	roles := se.DetectRoles(nil)
	if len(roles) != 0 {
		t.Errorf("expected 0 roles for nil agents, got %d", len(roles))
	}

	roles = se.DetectRoles([]string{})
	if len(roles) != 0 {
		t.Errorf("expected 0 roles for empty agents, got %d", len(roles))
	}
}

func TestDetectRoles_NotEnoughTasks(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// Only 3 tasks (minTasks is 5)
	for i := 0; i < 3; i++ {
		rep.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	}

	roles := se.DetectRoles([]string{"agent-1"})
	if len(roles) != 0 {
		t.Errorf("expected 0 roles when tasks < minTasks, got %d", len(roles))
	}
}

func TestDetectRoles_EnoughTasksBelowThreshold(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// 5 tasks in coding but with low quality
	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-1", "t1", "coding", true, 0.4)
	}

	roles := se.DetectRoles([]string{"agent-1"})
	if len(roles) != 0 {
		t.Errorf("expected 0 roles when score < minScore, got %d", len(roles))
	}
}

func TestDetectRoles_DetectsSpecialization(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// 5+ successful coding tasks with high quality
	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	}

	roles := se.DetectRoles([]string{"agent-1"})
	if len(roles) == 0 {
		t.Fatal("expected at least 1 role")
	}

	found := false
	for _, r := range roles {
		if r.AgentID == "agent-1" && r.Category == "coding" {
			found = true
			if r.Label != "Code Architect" {
				t.Errorf("Label = %q, want 'Code Architect'", r.Label)
			}
			if r.Confidence < 0.65 {
				t.Errorf("Confidence = %f, should be >= 0.65", r.Confidence)
			}
			break
		}
	}
	if !found {
		t.Error("expected a coding role for agent-1")
	}
}

func TestDetectRoles_MultipleAgents(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// Agent 1: coding specialist
	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	}

	// Agent 2: analysis specialist
	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-2", "t2", "analysis", true, 0.85)
	}

	roles := se.DetectRoles([]string{"agent-1", "agent-2"})
	if len(roles) < 2 {
		t.Errorf("expected at least 2 roles across agents, got %d", len(roles))
	}
}

func TestDetectRoles_UnknownCategoryLabel(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-1", "t1", "unknown_category", true, 0.9)
	}

	roles := se.DetectRoles([]string{"agent-1"})
	if len(roles) == 0 {
		t.Fatal("expected a role for unknown category")
	}
	if roles[0].Label != "unknown_category" {
		t.Errorf("Label = %q, want 'unknown_category' (fallback)", roles[0].Label)
	}
}

func TestSuggestAgent(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// Agent 1: good coder
	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
	}
	// Agent 2: decent coder
	for i := 0; i < 3; i++ {
		rep.RecordOutcome("agent-2", "t2", "coding", true, 0.6)
	}

	agent, score := se.SuggestAgent([]string{"agent-1", "agent-2"}, "coding")
	if agent != "agent-1" {
		t.Errorf("expected 'agent-1' for coding, got '%s'", agent)
	}
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
}

func TestSuggestAgent_FallbackToOverallReputation(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// Neither agent has category records — fallback to overall reputation
	rep.RecordOutcome("agent-1", "t1", "general", true, 1.0)
	rep.RecordOutcome("agent-2", "t2", "general", false, 0)

	agent, score := se.SuggestAgent([]string{"agent-1", "agent-2"}, "unknown_category")
	if agent == "" {
		t.Error("expected a fallback agent")
	}
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
}

func TestSuggestAgent_EmptyAgents(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	agent, score := se.SuggestAgent(nil, "coding")
	if agent != "" {
		t.Errorf("expected empty agent for nil list, got '%s'", agent)
	}
	if score != 0 {
		t.Errorf("expected 0 score, got %f", score)
	}

	agent, score = se.SuggestAgent([]string{}, "coding")
	if agent != "" {
		t.Errorf("expected empty agent for empty list, got '%s'", agent)
	}
}

func TestRoleLabels(t *testing.T) {
	tests := []struct {
		category string
		want     string
	}{
		{"coding", "Code Architect"},
		{"analysis", "Data Analyst"},
		{"debate", "Devil's Advocate"},
		{"review", "Quality Guardian"},
		{"browser", "Web Explorer"},
		{"writing", "Content Writer"},
		{"planning", "Strategic Planner"},
		{"research", "Research Specialist"},
		{"simple", "Quick Task Handler"},
		{"moderate", "General Problem Solver"},
		{"complex", "Complex System Architect"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := roleLabel(tt.category)
		if got != tt.want {
			t.Errorf("roleLabel(%q) = %q, want %q", tt.category, got, tt.want)
		}
	}
}

func TestDetectRoles_MultipleCategories(t *testing.T) {
	rep := NewReputationSystem(0.05)
	se := NewSpecializationEngine(rep)

	// 5 tasks in coding, 5 in analysis — both should emerge
	for i := 0; i < 5; i++ {
		rep.RecordOutcome("agent-1", "t1", "coding", true, 0.9)
		rep.RecordOutcome("agent-1", "t2", "analysis", true, 0.8)
	}

	roles := se.DetectRoles([]string{"agent-1"})
	if len(roles) < 2 {
		t.Errorf("expected at least 2 roles for agent-1, got %d", len(roles))
	}
}
