package collective

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// MarketTask is a task posted to the agent market for bidding.
type MarketTask struct {
	ID             string     `json:"id"`
	Description    string     `json:"description"`
	RequiredSkills []string   `json:"required_skills"`
	Complexity     string     `json:"complexity"` // simple, moderate, complex
	Budget         float64    `json:"budget"`
	Deadline       time.Time  `json:"deadline"`
	Bids           []*Bid     `json:"bids"`
	AwardedTo      string     `json:"awarded_to"`
	Status         TaskStatus `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
}

// TaskStatus tracks the lifecycle of a market task.
type TaskStatus string

const (
	TaskOpen     TaskStatus = "open"
	TaskAwarded  TaskStatus = "awarded"
	TaskRunning  TaskStatus = "running"
	TaskComplete TaskStatus = "complete"
	TaskFailed   TaskStatus = "failed"
	TaskExpired  TaskStatus = "expired"
)

// Bid is an agent's offer to execute a task.
type Bid struct {
	AgentID           string        `json:"agent_id"`
	AgentName         string        `json:"agent_name"`
	Confidence        float64       `json:"confidence"`
	EstimatedDuration time.Duration `json:"estimated_duration"`
	Price             float64       `json:"price"`
	Rationale         string        `json:"rationale"`
	Reputation        float64       `json:"reputation"`
	SubmittedAt       time.Time     `json:"submitted_at"`
}

// AgentMarket manages task bidding and award among agents.
type AgentMarket struct {
	board          *TaskBoard
	registry       *AgentRegistry
	reputation     *ReputationSystem
	settlement     *SettlementEngine
	auctionTimeout time.Duration
	mu             sync.RWMutex
}

// NewAgentMarket creates a new agent market.
func NewAgentMarket(rep *ReputationSystem) *AgentMarket {
	return &AgentMarket{
		board:          NewTaskBoard(),
		registry:       NewAgentRegistry(),
		reputation:     rep,
		settlement:     NewSettlementEngine(),
		auctionTimeout: 30 * time.Second,
	}
}

// RegisterAgent adds an agent to the market.
func (m *AgentMarket) RegisterAgent(id, name string, skills []string) {
	m.registry.Register(id, name, skills)
}

// PostTask publishes a task to the market board for bidding.
func (m *AgentMarket) PostTask(description string, requiredSkills []string, complexity string) *MarketTask {
	task := &MarketTask{
		ID:             fmt.Sprintf("task_%d", time.Now().UnixNano()),
		Description:    description,
		RequiredSkills: requiredSkills,
		Complexity:     complexity,
		Status:         TaskOpen,
		CreatedAt:      time.Now(),
	}

	m.mu.Lock()
	m.board.Add(task)
	m.mu.Unlock()

	slog.Info("collective: task posted to market",
		"task_id", task.ID,
		"complexity", complexity,
		"skills", requiredSkills,
	)

	return task
}

// SubmitBid allows an agent to bid on a task.
func (m *AgentMarket) SubmitBid(taskID, agentID, agentName string, confidence float64, estDuration time.Duration, rationale string) (*Bid, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task := m.board.Get(taskID)
	if task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	if task.Status != TaskOpen {
		return nil, fmt.Errorf("task %s is not open for bidding", taskID)
	}

	rep := m.reputation.GetReputation(agentID)

	bid := &Bid{
		AgentID:           agentID,
		AgentName:         agentName,
		Confidence:        confidence,
		EstimatedDuration: estDuration,
		Reputation:        rep,
		Rationale:         rationale,
		SubmittedAt:       time.Now(),
	}

	task.Bids = append(task.Bids, bid)
	return bid, nil
}

// RunAuction closes bidding and selects the winner.
func (m *AgentMarket) RunAuction(ctx context.Context, taskID string) (*Bid, error) {
	// Wait for bids
	select {
	case <-time.After(m.auctionTimeout):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	task := m.board.Get(taskID)
	if task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	if len(task.Bids) == 0 {
		task.Status = TaskExpired
		return nil, fmt.Errorf("no bids received for task %s", taskID)
	}

	winner := m.selectWinner(task.Bids)
	task.AwardedTo = winner.AgentID
	task.Status = TaskAwarded

	slog.Info("collective: auction awarded",
		"task_id", taskID,
		"winner", winner.AgentName,
		"score", fmt.Sprintf("%.3f", m.bidScore(winner)),
	)

	return winner, nil
}

func (m *AgentMarket) selectWinner(bids []*Bid) *Bid {
	var best *Bid
	var bestScore float64
	for _, b := range bids {
		score := m.bidScore(b)
		if score > bestScore {
			bestScore = score
			best = b
		}
	}
	return best
}

func (m *AgentMarket) bidScore(b *Bid) float64 {
	speedFactor := 1.0 / math.Max(b.EstimatedDuration.Seconds(), 1)
	priceFactor := 1.0 / math.Max(b.Price, 0.01)
	return b.Reputation*0.4 + b.Confidence*0.3 + speedFactor*0.2 + priceFactor*0.1
}

// CompleteTask records a task outcome and updates reputation.
func (m *AgentMarket) CompleteTask(taskID string, success bool, qualityScore float64) {
	m.mu.Lock()
	task := m.board.Get(taskID)
	if task == nil {
		m.mu.Unlock()
		return
	}
	if success {
		task.Status = TaskComplete
	} else {
		task.Status = TaskFailed
	}

	agentID := task.AwardedTo
	m.mu.Unlock()

	if agentID != "" {
		m.reputation.RecordOutcome(agentID, taskID, task.Complexity, success, qualityScore)
		m.settlement.Settle(task, success)
	}
}

// TaskBoard holds open and completed tasks.
type TaskBoard struct {
	tasks map[string]*MarketTask
	mu    sync.RWMutex
}

func NewTaskBoard() *TaskBoard {
	return &TaskBoard{tasks: make(map[string]*MarketTask)}
}

func (tb *TaskBoard) Add(task *MarketTask) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tasks[task.ID] = task
}

func (tb *TaskBoard) Get(id string) *MarketTask {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.tasks[id]
}

func (tb *TaskBoard) ListByStatus(status TaskStatus) []*MarketTask {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	var result []*MarketTask
	for _, t := range tb.tasks {
		if t.Status == status {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

// SettlementEngine tracks agent earnings and task costs.
type SettlementEngine struct {
	balances map[string]float64
	mu       sync.RWMutex
}

func NewSettlementEngine() *SettlementEngine {
	return &SettlementEngine{balances: make(map[string]float64)}
}

func (se *SettlementEngine) Settle(task *MarketTask, success bool) {
	se.mu.Lock()
	defer se.mu.Unlock()
	if success {
		se.balances[task.AwardedTo] += task.Budget
	}
}

func (se *SettlementEngine) Balance(agentID string) float64 {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.balances[agentID]
}

// AgentRegistry tracks available agents and their skills.
type AgentRegistry struct {
	agents map[string]*MarketAgent
	mu     sync.RWMutex
}

type MarketAgent struct {
	ID     string
	Name   string
	Skills []string
	Active bool
}

func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{agents: make(map[string]*MarketAgent)}
}

func (ar *AgentRegistry) Register(id, name string, skills []string) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.agents[id] = &MarketAgent{ID: id, Name: name, Skills: skills, Active: true}
}

func (ar *AgentRegistry) FilterBySkill(skill string) []*MarketAgent {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	var result []*MarketAgent
	for _, a := range ar.agents {
		for _, s := range a.Skills {
			if s == skill {
				result = append(result, a)
				break
			}
		}
	}
	return result
}

func (ar *AgentRegistry) All() []*MarketAgent {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	result := make([]*MarketAgent, 0, len(ar.agents))
	for _, a := range ar.agents {
		result = append(result, a)
	}
	return result
}
