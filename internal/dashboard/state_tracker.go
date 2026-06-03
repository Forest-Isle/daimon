package dashboard

import (
	"sync"
	"time"
)

type SessionState struct {
	SessionID         string    `json:"session_id"`
	Channel           string    `json:"channel,omitempty"`
	CurrentPhase      string    `json:"current_phase"`
	CurrentTool       string    `json:"current_tool,omitempty"`
	PhaseStart        time.Time `json:"phase_started_at,omitempty"`
	ToolsExecuted     int       `json:"tools_executed"`
	ReplanCount       int       `json:"replan_count"`
	Iteration         int       `json:"iteration,omitempty"`
	MaxIter           int       `json:"max_iterations,omitempty"`
	Utilization       float64   `json:"utilization,omitempty"`
	InputTokens       int64     `json:"input_tokens,omitempty"`
	OutputTokens      int64     `json:"output_tokens,omitempty"`
	CacheCreate       int64     `json:"cache_create,omitempty"`
	CacheRead         int64     `json:"cache_read,omitempty"`
	Model             string    `json:"model,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	PlanTaskCount     int       `json:"plan_task_count,omitempty"`
	PlanComplexity    string    `json:"plan_complexity,omitempty"`
	ObservationPassed int       `json:"observation_passed,omitempty"`
	ObservationFailed int       `json:"observation_failed,omitempty"`
	OverallProgress   float64   `json:"overall_progress,omitempty"`
	LastEventAt       time.Time `json:"-"`
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
	LastEventAt     time.Time `json:"-"`
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

const (
	sessionStaleTimeout   = 30 * time.Minute
	subagentStaleTimeout  = 60 * time.Minute
	gcInterval            = 5 * time.Minute
)

func (t *AgentStateTracker) Run() {
	gcTicker := time.NewTicker(gcInterval)
	defer gcTicker.Stop()

	for {
		select {
		case ev := <-t.eventCh:
			t.handleEvent(ev)
		case <-gcTicker.C:
			t.collectGarbage()
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
	case EventSessionStart:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
		if ch, ok := ev.Data["channel"].(string); ok {
			ss.Channel = ch
		}

	case EventPhaseStart:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
		if phase, ok := ev.Data["phase"].(string); ok {
			ss.CurrentPhase = phase
		}
		ss.PhaseStart = ev.Timestamp

	case EventPhaseEnd:
		if ev.Data["source"] != "evolution" {
			ss := t.getOrCreate(sid)
			ss.LastEventAt = ev.Timestamp
			ss.CurrentPhase = ""
		}

	case EventToolStart:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
		if name, ok := ev.Data["tool_name"].(string); ok {
			ss.CurrentTool = name
		}

	case EventToolEnd:
		if ev.Data["source"] != "evolution" {
			ss := t.getOrCreate(sid)
			ss.LastEventAt = ev.Timestamp
			ss.CurrentTool = ""
			ss.ToolsExecuted++
		}

	case EventPlanGenerated:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
		if tc, ok := ev.Data["task_count"].(int); ok {
			ss.PlanTaskCount = tc
		}
		if c, ok := ev.Data["complexity"].(string); ok {
			ss.PlanComplexity = c
		}

	case EventReplanStart:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
		ss.ReplanCount++

	case EventMetricsUpdate:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
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

	case EventObservationResult:
		ss := t.getOrCreate(sid)
		ss.LastEventAt = ev.Timestamp
		if p, ok := ev.Data["passed"].(int); ok {
			ss.ObservationPassed = p
		}
		if f, ok := ev.Data["failed"].(int); ok {
			ss.ObservationFailed = f
		}
		if prog, ok := ev.Data["overall_progress"].(float64); ok {
			ss.OverallProgress = prog
		}

	case EventSessionEnd:
		if ev.Data["source"] != "evolution" {
			delete(t.activeSessions, sid)
			t.totalToday++
		}

	case EventSubAgentSpawn:
		sa := &SubAgentState{
			SessionID:   sid,
			StartedAt:   ev.Timestamp,
			LastEventAt: ev.Timestamp,
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

// collectGarbage removes session and sub-agent state that has been
// inactive beyond the configured staleness thresholds. Prevents unbounded
// memory growth when EventSessionEnd is never received (crashes, network
// issues, session manager bugs).
func (t *AgentStateTracker) collectGarbage() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	for sid, ss := range t.activeSessions {
		if ss.LastEventAt.IsZero() {
			// Freshly created but never received an event — keep
			continue
		}
		if now.Sub(ss.LastEventAt) > sessionStaleTimeout {
			delete(t.activeSessions, sid)
			t.totalToday++
		}
	}

	for sid, sa := range t.activeSubAgents {
		if now.Sub(sa.LastEventAt) > subagentStaleTimeout {
			delete(t.activeSubAgents, sid)
		}
	}
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
