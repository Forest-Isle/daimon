package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTeamManager(t *testing.T) {
	tm := NewTeamManager(nil)
	require.NotNil(t, tm)
	assert.Empty(t, tm.AllTeams())
}

func TestTeamManager_GetTeam_NotFound(t *testing.T) {
	tm := NewTeamManager(nil)
	assert.Nil(t, tm.GetTeam("nonexistent"))
}

func TestTeamManager_ShutdownTeam_NotFound(t *testing.T) {
	// Should not panic
	tm := NewTeamManager(nil)
	tm.ShutdownTeam("nonexistent")
}

func TestManagedTeam_Structure(t *testing.T) {
	team := NewTeam("test", TeamMember{Name: "lead", AgentID: "a1"})
	router := NewMessageRouter(team)
	tasks := NewTeamTaskList()

	managed := &ManagedTeam{
		Team:   team,
		Router: router,
		Tasks:  tasks,
	}

	assert.Equal(t, "test", managed.Team.Name)
	assert.NotNil(t, managed.Router)
	assert.NotNil(t, managed.Tasks)
}

func TestTeamManager_AllTeams(t *testing.T) {
	tm := NewTeamManager(nil)

	// Manually insert a team to test AllTeams
	team := NewTeam("alpha", TeamMember{Name: "lead"})
	tm.mu.Lock()
	tm.teams["alpha"] = &ManagedTeam{Team: team}
	tm.mu.Unlock()

	names := tm.AllTeams()
	assert.Equal(t, []string{"alpha"}, names)
}

func TestTeamManager_ShutdownTeam(t *testing.T) {
	tm := NewTeamManager(nil)

	team := NewTeam("beta", TeamMember{Name: "lead"})
	router := NewMessageRouter(team)
	router.Register("lead")
	team.AddMember(TeamMember{Name: "worker1"})
	router.Register("worker1")

	tm.mu.Lock()
	tm.teams["beta"] = &ManagedTeam{Team: team, Router: router}
	tm.mu.Unlock()

	tm.ShutdownTeam("beta")

	assert.Empty(t, tm.AllTeams())
	// Team done channel should be closed
	select {
	case <-team.Done():
		// ok
	default:
		t.Fatal("team done channel should be closed after shutdown")
	}
}
