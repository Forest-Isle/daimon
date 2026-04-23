package agent

import (
	"sync"
)

// TeamRole defines a member's role in the team.
type TeamRole string

const (
	RoleLead     TeamRole = "lead"
	RoleTeammate TeamRole = "teammate"
)

// TeamMember represents a single agent in a team.
type TeamMember struct {
	Name     string
	Role     TeamRole
	AgentID  string
	Tools    []string // allowed tool names (empty = all)
	Model    string   // optional model override
	MaxTurns int      // max agentic turns
	Active   bool
}

// Team coordinates multiple agents working on shared tasks.
type Team struct {
	mu       sync.RWMutex
	Name     string
	Lead     TeamMember
	Members  []TeamMember
	Messages chan TeamMessage
	done     chan struct{}
}

// NewTeam creates a team with the given lead.
func NewTeam(name string, lead TeamMember) *Team {
	lead.Role = RoleLead
	lead.Active = true
	return &Team{
		Name:     name,
		Lead:     lead,
		Members:  []TeamMember{lead},
		Messages: make(chan TeamMessage, 100),
		done:     make(chan struct{}),
	}
}

// AddMember adds a teammate to the team.
func (t *Team) AddMember(m TeamMember) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m.Role = RoleTeammate
	m.Active = true
	t.Members = append(t.Members, m)
}

// RemoveMember removes a teammate by name.
func (t *Team) RemoveMember(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, m := range t.Members {
		if m.Name == name {
			t.Members = append(t.Members[:i], t.Members[i+1:]...)
			return
		}
	}
}

// GetMember returns a member by name, or nil if not found.
func (t *Team) GetMember(name string) *TeamMember {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for i := range t.Members {
		if t.Members[i].Name == name {
			return &t.Members[i]
		}
	}
	return nil
}

// ActiveMembers returns all currently active members.
func (t *Team) ActiveMembers() []TeamMember {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var active []TeamMember
	for _, m := range t.Members {
		if m.Active {
			active = append(active, m)
		}
	}
	return active
}

// Shutdown signals all members to stop.
func (t *Team) Shutdown() {
	close(t.done)
}

// Done returns a channel that's closed when the team shuts down.
func (t *Team) Done() <-chan struct{} { return t.done }
