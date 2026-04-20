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
	Iteration     int       `json:"iteration,omitempty"`
	MaxIter       int       `json:"max_iterations,omitempty"`
	Utilization   float64   `json:"utilization,omitempty"`
	InputTokens   int64     `json:"input_tokens,omitempty"`
	OutputTokens  int64     `json:"output_tokens,omitempty"`
	CacheCreate   int64     `json:"cache_create,omitempty"`
	CacheRead     int64     `json:"cache_read,omitempty"`
	Model         string    `json:"model,omitempty"`
	Provider      string    `json:"provider,omitempty"`
}

type StateSnapshot struct {
	Status         string         `json:"status"`
	ActiveSessions []SessionState `json:"active_sessions"`
	UptimeSeconds  int64          `json:"uptime_seconds"`
	TotalSessions  int            `json:"total_sessions"`
}

type AgentStateTracker struct {
	bus            *Bus
	eventCh        chan Event
	mu             sync.RWMutex
	activeSessions map[string]*SessionState
	totalToday     int
	startedAt      time.Time
	stopCh         chan struct{}
}

func NewAgentStateTracker(bus *Bus) *AgentStateTracker {
	return &AgentStateTracker{
		bus:            bus,
		eventCh:        bus.Subscribe(),
		activeSessions: make(map[string]*SessionState),
		startedAt:      time.Now(),
		stopCh:         make(chan struct{}),
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

	case EventMetricsUpdate:
		ss := t.getOrCreate(sid)
		if v, ok := ev.Data["iteration"].(int); ok {
			ss.Iteration = v
		}
		if v, ok := ev.Data["max_iterations"].(int); ok {
			ss.MaxIter = v
		}
		if v, ok := ev.Data["utilization"].(float64); ok {
			ss.Utilization = v
		}
		if v, ok := ev.Data["input_tokens"].(int64); ok {
			ss.InputTokens = v
		}
		if v, ok := ev.Data["output_tokens"].(int64); ok {
			ss.OutputTokens = v
		}
		if v, ok := ev.Data["cache_create"].(int64); ok {
			ss.CacheCreate = v
		}
		if v, ok := ev.Data["cache_read"].(int64); ok {
			ss.CacheRead = v
		}
		if v, ok := ev.Data["model"].(string); ok {
			ss.Model = v
		}
		if v, ok := ev.Data["provider"].(string); ok {
			ss.Provider = v
		}

	case EventSessionEnd:
		delete(t.activeSessions, sid)
		t.totalToday++
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

	status := "idle"
	if len(sessions) > 0 {
		status = "busy"
	}

	return StateSnapshot{
		Status:         status,
		ActiveSessions: sessions,
		UptimeSeconds:  int64(time.Since(t.startedAt).Seconds()),
		TotalSessions:  t.totalToday + len(t.activeSessions),
	}
}
