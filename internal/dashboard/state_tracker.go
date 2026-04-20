package dashboard

import (
	"sync"
	"time"
)

type SessionState struct {
	SessionID     string    `json:"session_id"`
	Channel       string    `json:"channel,omitempty"`
	CurrentPhase  string    `json:"current_phase"`
	CurrentTool   string    `json:"current_tool,omitempty"`
	PhaseStart    time.Time `json:"phase_started_at,omitempty"`
	ToolsExecuted int       `json:"tools_executed"`
	ReplanCount   int       `json:"replan_count"`
}

type StateSnapshot struct {
	Status            string          `json:"status"`
	ActiveSessions    []SessionState  `json:"active_sessions"`
	UptimeSeconds     int64           `json:"uptime_seconds"`
	TotalSessions     int             `json:"total_sessions"`
	ActiveSubAgents   []SubAgentState `json:"active_subagents,omitempty"`
	CompressionEvents int             `json:"compression_events,omitempty"`
}

type SubAgentState struct {
	SessionID       string    `json:"session_id"`
	ParentSessionID string    `json:"parent_session_id"`
	AgentName       string    `json:"agent_name"`
	Task            string    `json:"task,omitempty"`
	StartedAt       time.Time `json:"started_at"`
}

type AgentStateTracker struct {
	bus              *Bus
	eventCh          chan Event
	mu               sync.RWMutex
	activeSessions   map[string]*SessionState
	activeSubAgents  map[string]*SubAgentState
	compressionCount int
	totalToday       int
	startedAt        time.Time
	stopCh           chan struct{}
}

func NewAgentStateTracker(bus *Bus) *AgentStateTracker {
	return &AgentStateTracker{
		bus:             bus,
		eventCh:         bus.Subscribe(),
		activeSessions:  make(map[string]*SessionState),
		activeSubAgents: make(map[string]*SubAgentState),
		startedAt:       time.Now(),
		stopCh:          make(chan struct{}),
	}
}

func (t *AgentStateTracker) Run() {
	for {
		select {
		case ev := <-t.eventCh:
			t.handleEvent(ev)
		case <-t.stopCh:
			t.bus.Unsubscribe(t.eventCh)
			return
		}
	}
}

func (t *AgentStateTracker) Stop() {
	select {
	case <-t.stopCh:
	default:
		close(t.stopCh)
	}
}

func (t *AgentStateTracker) handleEvent(ev Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	sid := ev.SessionID
	if sid == "" {
		return
	}

	switch ev.Type {
	case EventPhaseStart:
		ss := t.getOrCreate(sid)
		if phase, ok := ev.Data["phase"].(string); ok {
			ss.CurrentPhase = phase
		}
		ss.PhaseStart = ev.Timestamp

	case EventPhaseEnd:
		ss := t.getOrCreate(sid)
		ss.CurrentPhase = ""

	case EventToolStart:
		ss := t.getOrCreate(sid)
		if name, ok := ev.Data["tool_name"].(string); ok {
			ss.CurrentTool = name
		}

	case EventToolEnd:
		ss := t.getOrCreate(sid)
		ss.CurrentTool = ""
		ss.ToolsExecuted++

	case EventReplanStart:
		ss := t.getOrCreate(sid)
		ss.ReplanCount++

	case EventSessionEnd:
		delete(t.activeSessions, sid)
		t.totalToday++

	case EventSubAgentSpawn:
		sa := &SubAgentState{
			SessionID: sid,
			StartedAt: ev.Timestamp,
		}
		if v, ok := ev.Data["parent_session_id"].(string); ok {
			sa.ParentSessionID = v
		}
		if v, ok := ev.Data["agent_name"].(string); ok {
			sa.AgentName = v
		}
		if v, ok := ev.Data["task"].(string); ok {
			sa.Task = v
		}
		t.activeSubAgents[sid] = sa

	case EventSubAgentComplete:
		delete(t.activeSubAgents, sid)

	case EventContextCompress:
		t.compressionCount++
	}
}

func (t *AgentStateTracker) getOrCreate(sessionID string) *SessionState {
	ss, ok := t.activeSessions[sessionID]
	if !ok {
		ss = &SessionState{SessionID: sessionID}
		t.activeSessions[sessionID] = ss
	}
	return ss
}

func (t *AgentStateTracker) Snapshot() StateSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	sessions := make([]SessionState, 0, len(t.activeSessions))
	for _, ss := range t.activeSessions {
		sessions = append(sessions, *ss)
	}

	subAgents := make([]SubAgentState, 0, len(t.activeSubAgents))
	for _, sa := range t.activeSubAgents {
		subAgents = append(subAgents, *sa)
	}

	status := "idle"
	if len(sessions) > 0 {
		status = "busy"
	}

	return StateSnapshot{
		Status:            status,
		ActiveSessions:    sessions,
		UptimeSeconds:     int64(time.Since(t.startedAt).Seconds()),
		TotalSessions:     t.totalToday + len(t.activeSessions),
		ActiveSubAgents:   subAgents,
		CompressionEvents: t.compressionCount,
	}
}
