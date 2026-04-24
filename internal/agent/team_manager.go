package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// TeamManager coordinates multi-agent teams using the Team, MessageRouter,
// and TeamTaskList primitives. It builds on SubAgentManager for the actual
// agent spawning and adds team-level lifecycle management.
type TeamManager struct {
	subMgr *SubAgentManager
	mu     sync.RWMutex
	teams  map[string]*ManagedTeam
}

// ManagedTeam bundles a Team with its supporting infrastructure.
type ManagedTeam struct {
	Team     *Team
	Router   *MessageRouter
	Tasks    *TeamTaskList
	Members  []*SubAgentResult // results from spawned agents
}

// NewTeamManager creates a TeamManager backed by the given SubAgentManager.
func NewTeamManager(subMgr *SubAgentManager) *TeamManager {
	return &TeamManager{
		subMgr: subMgr,
		teams:  make(map[string]*ManagedTeam),
	}
}

// SpawnTeamRequest defines a team to create and populate.
type SpawnTeamRequest struct {
	Name    string         // team name
	LeadID  string         // agent ID of the lead (caller)
	Members []TeamMemberSpec
	Tasks   []TeamTaskSpec
}

// TeamMemberSpec describes a team member to spawn.
type TeamMemberSpec struct {
	Name  string   // member name
	Task  string   // initial task description
	Tools []string // allowed tool names (empty = all)
	Model string   // optional model override
}

// TeamTaskSpec describes a task to add to the shared task list.
type TeamTaskSpec struct {
	Subject     string
	Description string
	Owner       string // pre-assign to a member name
}

// SpawnTeam creates a team, spawns all members as parallel sub-agents,
// and returns the managed team with results.
func (tm *TeamManager) SpawnTeam(ctx context.Context, req SpawnTeamRequest) (*ManagedTeam, error) {
	teamName := req.Name
	if teamName == "" {
		teamName = "team-" + uuid.New().String()[:8]
	}

	// Create team with lead
	lead := TeamMember{
		Name:    "lead",
		AgentID: req.LeadID,
		Active:  true,
	}
	team := NewTeam(teamName, lead)
	router := NewMessageRouter(team)
	tasks := NewTeamTaskList()

	// Register lead inbox
	router.Register("lead")

	// Add tasks to shared list
	for _, ts := range req.Tasks {
		t := tasks.Create(ts.Subject, ts.Description)
		if ts.Owner != "" {
			tasks.Assign(t.ID, ts.Owner)
			tasks.UpdateStatus(t.ID, TaskInProgress)
		}
	}

	// Build spawn requests for all members
	spawnReqs := make([]SpawnRequest, len(req.Members))
	for i, m := range req.Members {
		team.AddMember(TeamMember{
			Name:    m.Name,
			AgentID: fmt.Sprintf("team_%s_%s", teamName, m.Name),
			Tools:   m.Tools,
			Model:   m.Model,
			Active:  true,
		})
		router.Register(m.Name)

		spec := &AgentSpec{
			Name:        m.Name,
			Tools:       m.Tools,
			Model:       m.Model,
			MaxIterations: 20,
		}
		spawnReqs[i] = SpawnRequest{
			Spec:     spec,
			Task:     m.Task,
			ParentID: req.LeadID,
			ChainID:  teamName,
		}
	}

	slog.Info("team: spawning members",
		"team", teamName,
		"members", len(req.Members),
		"tasks", len(req.Tasks),
	)

	// Spawn all members in parallel
	results, err := tm.subMgr.SpawnParallel(ctx, spawnReqs, StrategyBestEffort)
	if err != nil {
		return nil, fmt.Errorf("spawn team members: %w", err)
	}

	managed := &ManagedTeam{
		Team:    team,
		Router:  router,
		Tasks:   tasks,
		Members: results,
	}

	tm.mu.Lock()
	tm.teams[teamName] = managed
	tm.mu.Unlock()

	slog.Info("team: all members completed",
		"team", teamName,
		"results", len(results),
	)

	return managed, nil
}

// GetTeam returns a managed team by name, or nil.
func (tm *TeamManager) GetTeam(name string) *ManagedTeam {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.teams[name]
}

// ShutdownTeam shuts down a team and cleans up resources.
func (tm *TeamManager) ShutdownTeam(name string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if mt, ok := tm.teams[name]; ok {
		mt.Team.Shutdown()
		// Unregister all member inboxes
		for _, m := range mt.Team.ActiveMembers() {
			mt.Router.Unregister(m.Name)
		}
		delete(tm.teams, name)
		slog.Info("team: shutdown complete", "team", name)
	}
}

// AllTeams returns names of all active teams.
func (tm *TeamManager) AllTeams() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	names := make([]string, 0, len(tm.teams))
	for name := range tm.teams {
		names = append(names, name)
	}
	return names
}
