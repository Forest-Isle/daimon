package agent

import (
	"testing"
)

func TestNewTeam(t *testing.T) {
	lead := TeamMember{Name: "lead-1", AgentID: "a1"}
	team := NewTeam("test-team", lead)

	if team.Name != "test-team" {
		t.Errorf("Name = %q, want %q", team.Name, "test-team")
	}
	if team.Lead.Role != RoleLead {
		t.Errorf("Lead.Role = %q, want %q", team.Lead.Role, RoleLead)
	}
	if !team.Lead.Active {
		t.Error("Lead should be active")
	}
	if len(team.Members) != 1 {
		t.Errorf("Members = %d, want 1", len(team.Members))
	}
}

func TestTeam_AddRemoveMember(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)

	team.AddMember(TeamMember{Name: "dev-1", AgentID: "a2"})
	team.AddMember(TeamMember{Name: "dev-2", AgentID: "a3"})

	if len(team.Members) != 3 {
		t.Fatalf("Members = %d, want 3", len(team.Members))
	}

	m := team.GetMember("dev-1")
	if m == nil {
		t.Fatal("GetMember(dev-1) = nil")
	}
	if m.Role != RoleTeammate {
		t.Errorf("dev-1 Role = %q, want %q", m.Role, RoleTeammate)
	}
	if !m.Active {
		t.Error("dev-1 should be active")
	}

	team.RemoveMember("dev-1")
	if team.GetMember("dev-1") != nil {
		t.Error("dev-1 should be removed")
	}
	if len(team.Members) != 2 {
		t.Errorf("Members = %d, want 2", len(team.Members))
	}
}

func TestTeam_RemoveNonexistent(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)

	// Should not panic
	team.RemoveMember("nonexistent")
	if len(team.Members) != 1 {
		t.Errorf("Members = %d, want 1", len(team.Members))
	}
}

func TestTeam_ActiveMembers(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)

	team.AddMember(TeamMember{Name: "dev-1"})
	team.AddMember(TeamMember{Name: "dev-2"})

	// Deactivate one member
	m := team.GetMember("dev-2")
	m.Active = false

	active := team.ActiveMembers()
	if len(active) != 2 {
		t.Errorf("ActiveMembers = %d, want 2", len(active))
	}
}

func TestTeam_Shutdown(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)

	select {
	case <-team.Done():
		t.Fatal("Done() should not be closed yet")
	default:
	}

	team.Shutdown()

	select {
	case <-team.Done():
		// expected
	default:
		t.Fatal("Done() should be closed after Shutdown")
	}
}

func TestTeam_GetMemberNotFound(t *testing.T) {
	lead := TeamMember{Name: "lead-1"}
	team := NewTeam("test-team", lead)

	if team.GetMember("nonexistent") != nil {
		t.Error("GetMember should return nil for unknown name")
	}
}
