# Agent Team Coordination — Peer-to-Peer Multi-Agent Collaboration

## Overview

Adds a peer-to-peer Agent Team model where a Team Lead coordinates multiple Teammates, each with independent context windows. Teams communicate through direct messages and broadcasts, and share a thread-safe task list for work coordination.

## Problem

The existing `SubAgentManager` only supports parent-child delegation — a single agent spawns isolated child agents for subtasks. This lacks:

- **Peer communication** — child agents cannot talk to each other
- **Shared work tracking** — no common task list across agents
- **Team lifecycle** — no concept of a team with members joining/leaving

## Architecture

### Team Structure

```
                    ┌───────────────────┐
                    │    Team Lead      │
                    │  (coordinates)    │
                    └───────┬───────────┘
                            │  MessageRouter
            ┌───────────────┼───────────────┐
            │               │               │
     ┌──────▼──────┐ ┌─────▼──────┐ ┌─────▼──────┐
     │  Researcher  │ │   Coder    │ │  Reviewer   │
     │ (own context)│ │(own context)│ │(own context) │
     └─────────────┘ └────────────┘ └─────────────┘
                            │
                    ┌───────▼───────┐
                    │ TeamTaskList  │
                    │  (shared)     │
                    └───────────────┘
```

### Components

#### Team (`internal/agent/team.go`)

Core team lifecycle management:

```go
type Team struct {
    Name     string
    Lead     TeamMember
    Members  []TeamMember
    Messages chan TeamMessage
}

type TeamMember struct {
    Name     string      // human-readable identifier
    Role     TeamRole    // "lead" or "teammate"
    AgentID  string      // unique agent identifier
    Tools    []string    // allowed tool names (empty = all)
    Model    string      // optional model override
    MaxTurns int         // max agentic turns before stop
    Active   bool        // currently running
}
```

- `NewTeam(name, lead)` — Creates team with lead member
- `AddMember(m)` / `RemoveMember(name)` — Manage membership
- `GetMember(name)` — Lookup by name
- `ActiveMembers()` — List currently active members
- `Shutdown()` / `Done()` — Graceful team lifecycle

#### Message Router (`internal/agent/team_message.go`)

Buffered, per-member message delivery:

```go
type TeamMessage struct {
    Type      MessageType  // message, broadcast, shutdown_request/response, plan_approval
    From      string
    To        string       // empty for broadcast
    Content   string
    Summary   string
    RequestID string       // for request/response pairing
    Approve   *bool        // for approval responses
    Timestamp time.Time    // auto-filled if zero
}
```

5 message types:

| Type | Delivery | Use Case |
|------|----------|----------|
| `MsgDirect` | Single recipient | Normal communication |
| `MsgBroadcast` | All except sender | Critical announcements |
| `MsgShutdownRequest` | Single recipient | Graceful termination |
| `MsgShutdownResponse` | Single recipient | Acknowledge shutdown |
| `MsgPlanApproval` | Single recipient | Lead approves teammate plan |

Router features:
- `Register(name)` returns a `<-chan TeamMessage` inbox (buffered, 50 deep)
- `Send(msg)` delivers to recipient or broadcasts
- `Unregister(name)` closes and removes inbox
- Full messages dropped silently (non-blocking send)

#### Shared Task List (`internal/agent/team_task.go`)

Thread-safe task coordination:

```go
type TeamTask struct {
    ID          string
    Subject     string
    Description string
    Owner       string      // assigned member name
    Status      TaskStatus  // pending, in_progress, completed, blocked
    BlockedBy   []string    // task IDs that must complete first
}
```

- `Create(subject, description)` — Auto-incrementing IDs
- `Get(id)` — Returns copy (prevents races)
- `UpdateStatus(id, status)` / `Assign(id, owner)` — State management
- `Available()` — Returns unassigned, unblocked, pending tasks
- `All()` — Returns copies of all tasks

### Thread Safety

All three components use `sync.RWMutex`:
- `Team.mu` — Protects member list
- `MessageRouter.mu` — Protects inbox map
- `TeamTaskList.mu` — Protects task slice

`Get()` and `All()` methods return **copies** to prevent callers from mutating shared state.

## Files

| File | Lines | Description |
|---|---|---|
| `internal/agent/team.go` | 101 | Team lifecycle + member management |
| `internal/agent/team_message.go` | 98 | Message router with 5 message types |
| `internal/agent/team_task.go` | 118 | Shared thread-safe task list |
| `internal/agent/team_test.go` | 111 | 6 tests: creation, membership, shutdown |
| `internal/agent/team_message_test.go` | 157 | 5 tests: direct, broadcast, unregister |
| `internal/agent/team_task_test.go` | 155 | 8 tests: CRUD, assignment, blocking |

## Testing

```bash
go test -run "TestTeam" ./internal/agent/...
# 19 tests pass
```

## Integration Guide

To create a team in the cognitive loop:

```go
// Create team
team := agent.NewTeam("my-project", agent.TeamMember{
    Name: "lead", AgentID: "agent-001",
})

// Add teammates
team.AddMember(agent.TeamMember{
    Name: "researcher", AgentID: "agent-002",
    Tools: []string{"file_read", "browser_search", "browser_extract"},
})

// Set up message routing
router := agent.NewMessageRouter(team)
leadInbox := router.Register("lead")
researcherInbox := router.Register("researcher")

// Set up shared tasks
tasks := agent.NewTeamTaskList()
tasks.Create("Research auth patterns", "Find authentication implementations...")

// Send messages
router.Send(agent.TeamMessage{
    Type: agent.MsgDirect, From: "lead", To: "researcher",
    Content: "Start researching auth patterns",
})

// Teammate receives
msg := <-researcherInbox
```
