package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// MCTSNode is a node in the Monte Carlo search tree.
// Each node represents a partial or complete plan at a given depth.
type MCTSNode struct {
	Plan        *TaskPlan
	Strategy    string
	Depth       int
	Parent      *MCTSNode
	Children    []*MCTSNode
	Visits      int
	TotalReward float64
	MeanReward  float64
	IsTerminal  bool
	Failed      bool

	mu sync.Mutex
}

// MCTSConfig tunes the Monte Carlo Tree Search behavior.
type MCTSConfig struct {
	MaxIterations    int           // max MCTS iterations per search (default: 100)
	MaxDepth         int           // max tree depth (default: 5)
	ExplorationC     float64       // UCB1 exploration constant (default: 1.414)
	TimeBudget       time.Duration // time-budgeted search (0 = unlimited)
	RolloutDepth     int           // max steps in a rollout simulation (default: 3)
	ProgressiveWidth int           // max children per node before progressive widening (default: 5)
	MinVisits        int           // minimum visits before a node can be selected as best (default: 10)
	Temperature      float64       // softmax temperature for final policy (default: 0.5)
}

// DefaultMCTSConfig returns sensible defaults.
func DefaultMCTSConfig() MCTSConfig {
	return MCTSConfig{
		MaxIterations:    100,
		MaxDepth:         5,
		ExplorationC:     1.414,
		TimeBudget:       30 * time.Second,
		RolloutDepth:     3,
		ProgressiveWidth: 5,
		MinVisits:        10,
		Temperature:      0.5,
	}
}

// MCTSPlanner wraps a Planner with full Monte Carlo Tree Search.
// It explores the plan space using selection → expansion → simulation → backpropagation.
type MCTSPlanner struct {
	planner  *Planner
	provider Provider
	llmModel string
	cfg      MCTSConfig

	root      *MCTSNode
	nodesByID map[string]*MCTSNode // plan hash → node
	mu        sync.RWMutex
}

// NewMCTSPlanner creates a full MCTS planner.
func NewMCTSPlanner(planner *Planner, provider Provider, llmModel string, cfg MCTSConfig) *MCTSPlanner {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 100
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 5
	}
	if cfg.ExplorationC <= 0 {
		cfg.ExplorationC = 1.414
	}
	if cfg.RolloutDepth <= 0 {
		cfg.RolloutDepth = 3
	}
	return &MCTSPlanner{
		planner:   planner,
		provider:  provider,
		llmModel:  llmModel,
		cfg:       cfg,
		nodesByID: make(map[string]*MCTSNode),
	}
}

// planHash creates a stable key from a plan for deduplication.
func planHash(plan *TaskPlan) string {
	if plan == nil {
		return "nil"
	}
	data, _ := json.Marshal(plan.SubTasks)
	return string(data)
}

// Search performs full MCTS from the given state, returning the best plan found.
func (p *MCTSPlanner) Search(ctx context.Context, state *CognitiveState) (*TaskPlan, []PlanCandidate, error) {
	if p == nil || p.planner == nil {
		return nil, nil, fmt.Errorf("mcts planner unavailable")
	}
	if state == nil {
		return nil, nil, fmt.Errorf("nil cognitive state")
	}

	p.mu.Lock()
	p.nodesByID = make(map[string]*MCTSNode)
	p.mu.Unlock()

	// Phase 1: Generate initial candidates for the root
	rootCandidates, err := p.generateRootCandidates(ctx, state)
	if err != nil {
		return nil, nil, fmt.Errorf("mcts: root candidate generation: %w", err)
	}
	if len(rootCandidates) == 0 {
		return nil, nil, fmt.Errorf("mcts: no viable root candidates generated")
	}

	p.root = &MCTSNode{
		Depth:    0,
		Children: make([]*MCTSNode, 0, len(rootCandidates)),
	}
	for _, c := range rootCandidates {
		child := &MCTSNode{
			Plan:        c.Plan,
			Strategy:    c.Strategy,
			Depth:       1,
			Parent:      p.root,
			TotalReward: c.LLMScore,
			Visits:      1,
			MeanReward:  c.LLMScore,
		}
		p.root.Children = append(p.root.Children, child)
		p.nodesByID[planHash(c.Plan)] = child
	}

	// Phase 2: MCTS iterations
	deadline := time.Now().Add(p.cfg.TimeBudget)
	for iter := 0; iter < p.cfg.MaxIterations; iter++ {
		select {
		case <-ctx.Done():
			break
		default:
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			slog.Debug("mcts: time budget exhausted", "iterations", iter)
			break
		}

		// SELECTION: traverse tree using UCB1 to find a leaf
		leaf := p.selectLeaf(p.root)

		// EXPANSION: if leaf is not terminal and not fully explored, expand
		if !leaf.IsTerminal && leaf.Depth < p.cfg.MaxDepth && len(leaf.Children) < p.cfg.ProgressiveWidth {
			expanded, err := p.expandNode(ctx, leaf, state)
			if err != nil {
				slog.Debug("mcts: expansion failed", "depth", leaf.Depth, "error", err)
				// Mark as failed but continue search
				leaf.Failed = true
				leaf.IsTerminal = true
				p.backpropagate(leaf, 0) // backprop failure
				continue
			}
			if len(expanded) > 0 {
				leaf = expanded[0] // use first expanded child for rollout
			}
		}

		// SIMULATION: rollout from leaf to estimate value
		reward := p.rollout(ctx, leaf, state)

		// BACKPROPAGATION: update all ancestors
		p.backpropagate(leaf, reward)
	}

	// Phase 3: Select best plan from the tree
	bestCandidate := p.selectBestPlan()
	if bestCandidate == nil {
		return nil, nil, fmt.Errorf("mcts: no viable plan found after %d iterations", p.cfg.MaxIterations)
	}

	// Build candidate list for the cognitive agent to use
	candidates := p.collectCandidates()

	slog.Info("mcts: search complete",
		"iterations", p.cfg.MaxIterations,
		"best_strategy", bestCandidate.Strategy,
		"best_score", bestCandidate.LLMScore,
		"best_visits", bestCandidate.Visits,
		"total_candidates", len(candidates),
	)

	return bestCandidate.Plan, candidates, nil
}

// selectLeaf traverses the tree from root using UCB1, returning the leaf node
// with the highest UCB score at each level. Stops at terminal nodes or leaves.
func (p *MCTSPlanner) selectLeaf(node *MCTSNode) *MCTSNode {
	current := node
	for {
		if current.IsTerminal || len(current.Children) == 0 {
			return current
		}

		// If any child is unvisited, select it immediately
		for _, child := range current.Children {
			child.mu.Lock()
			visits := child.Visits
			child.mu.Unlock()
			if visits == 0 {
				return child
			}
		}

		// UCB1 selection among children
		bestChild := p.ucbSelect(current)
		if bestChild == nil {
			return current
		}
		current = bestChild
	}
}

// ucbSelect picks the child with the highest UCB1 score.
func (p *MCTSPlanner) ucbSelect(node *MCTSNode) *MCTSNode {
	if len(node.Children) == 0 {
		return nil
	}

	totalVisits := 0
	for _, c := range node.Children {
		c.mu.Lock()
		totalVisits += c.Visits
		c.mu.Unlock()
	}

	var best *MCTSNode
	bestScore := math.Inf(-1)

	for _, child := range node.Children {
		child.mu.Lock()
		visits := child.Visits
		reward := child.MeanReward
		child.mu.Unlock()

		if visits == 0 {
			return child // unvisited = infinite UCB
		}

		exploitation := reward
		exploration := p.cfg.ExplorationC * math.Sqrt(math.Log(float64(totalVisits))/float64(visits))
		score := exploitation + exploration

		if score > bestScore {
			bestScore = score
			best = child
		}
	}
	return best
}

// expandNode generates new candidate plans branching from the given node.
// Uses the planner with modified hints to explore alternative strategies.
func (p *MCTSPlanner) expandNode(ctx context.Context, node *MCTSNode, state *CognitiveState) ([]*MCTSNode, error) {
	if node == nil || node.Plan == nil {
		return nil, fmt.Errorf("cannot expand nil node")
	}

	// Generate alternative strategies based on current node's plan
	alternatives := p.generateAlternatives(ctx, node, state)

	newChildren := make([]*MCTSNode, 0, len(alternatives))
	for _, alt := range alternatives {
		hash := planHash(alt.Plan)
		p.mu.RLock()
		existing, exists := p.nodesByID[hash]
		p.mu.RUnlock()
		if exists {
			// This plan already exists in the tree — link it
			newChildren = append(newChildren, existing)
			continue
		}

		child := &MCTSNode{
			Plan:        alt.Plan,
			Strategy:    alt.Strategy,
			Depth:       node.Depth + 1,
			Parent:      node,
			TotalReward: alt.LLMScore,
			Visits:      1,
			MeanReward:  alt.LLMScore,
		}
		node.Children = append(node.Children, child)

		p.mu.Lock()
		p.nodesByID[hash] = child
		p.mu.Unlock()

		newChildren = append(newChildren, child)
	}

	return newChildren, nil
}

// rollout simulates executing the plan from the given node and returns an estimated reward.
// Uses a fast heuristic evaluation rather than full LLM calls for efficiency.
func (p *MCTSPlanner) rollout(ctx context.Context, node *MCTSNode, state *CognitiveState) float64 {
	if node == nil || node.Plan == nil {
		return 0
	}

	reward := 0.0

	// Heuristic 1: Plan structural quality
	reward += p.evaluatePlanStructure(node.Plan)

	// Heuristic 2: Tool availability (all referenced tools exist and are available)
	reward += p.evaluateToolAvailability(node.Plan)

	// Heuristic 3: Plan coherence (steps have logical flow)
	reward += p.evaluatePlanCoherence(node.Plan)

	// Heuristic 4: Strategy diversity bonus (reward plans different from parent)
	if node.Parent != nil && len(node.Parent.Children) > 1 {
		reward += 0.1 // diversity bonus
	}

	// Heuristic 5: Complexity penalty (prefer simpler plans)
	stepCount := len(node.Plan.SubTasks)
	if stepCount > 10 {
		reward -= float64(stepCount-10) * 0.02
	}

	// Heuristic 6: Depth discount (shallower plans preferred unless deeper is better)
	reward -= float64(node.Depth) * 0.05

	// Clamp to [0, 1]
	if reward < 0 {
		reward = 0
	}
	if reward > 1 {
		reward = 1
	}

	return reward
}

// evaluatePlanStructure scores the structural quality of a plan.
func (p *MCTSPlanner) evaluatePlanStructure(plan *TaskPlan) float64 {
	if plan == nil {
		return 0
	}
	score := 0.3 // base score

	// Has explicit steps
	if len(plan.SubTasks) > 0 {
		score += 0.15
	}

	// Steps have descriptions
	hasDescs := true
	for _, s := range plan.SubTasks {
		if s.Description == "" {
			hasDescs = false
			break
		}
	}
	if hasDescs {
		score += 0.1
	}

	// Has explicit tool references
	hasTools := false
	for _, s := range plan.SubTasks {
		if s.ToolName != "" {
			hasTools = true
			break
		}
	}
	if hasTools {
		score += 0.1
	}

	// Has a clear goal statement
	if plan.Summary != "" {
		score += 0.05
	}

	return math.Min(score, 0.7)
}

// evaluateToolAvailability checks that all referenced tools exist.
func (p *MCTSPlanner) evaluateToolAvailability(plan *TaskPlan) float64 {
	if plan == nil || p.planner == nil || p.planner.tools == nil {
		return 0
	}

	score := 0.0
	toolCount := 0
	for _, step := range plan.SubTasks {
		if step.ToolName == "" {
			continue
		}
		toolCount++
		if _, err := p.planner.tools.Get(step.ToolName); err == nil {
			score += 1.0
		}
	}

	if toolCount == 0 {
		return 0.1 // no tools referenced, neutral
	}
	return (score / float64(toolCount)) * 0.15
}

// evaluatePlanCoherence checks if plan steps form a logical sequence.
func (p *MCTSPlanner) evaluatePlanCoherence(plan *TaskPlan) float64 {
	if plan == nil || len(plan.SubTasks) <= 1 {
		return 0.1
	}

	score := 0.1 // base coherence
	// Check for dependency chains (outputs of previous steps used as inputs)
	for i := 1; i < len(plan.SubTasks); i++ {
		prev := plan.SubTasks[i-1]
		curr := plan.SubTasks[i]
		// Simple heuristic: if adjacent steps share logical progression
		if prev.ToolName != "" && curr.ToolName != "" {
			score += 0.05
		}
	}

	return math.Min(score, 0.15)
}

// backpropagate updates visit counts and rewards up the tree from leaf to root.
func (p *MCTSPlanner) backpropagate(node *MCTSNode, reward float64) {
	current := node
	for current != nil {
		current.mu.Lock()
		current.Visits++
		current.TotalReward += reward
		current.MeanReward = current.TotalReward / float64(current.Visits)
		current.mu.Unlock()
		current = current.Parent
	}
}

// selectBestPlan returns the best plan from the tree using visit-weighted scoring.
func (p *MCTSPlanner) selectBestPlan() *PlanCandidate {
	if p.root == nil || len(p.root.Children) == 0 {
		return nil
	}

	var best *PlanCandidate
	bestScore := math.Inf(-1)

	for _, child := range p.root.Children {
		child.mu.Lock()
		visits := child.Visits
		reward := child.MeanReward
		strategy := child.Strategy
		plan := child.Plan
		child.mu.Unlock()

		if visits < p.cfg.MinVisits {
			continue
		}

		// Score combines mean reward with a visit bonus (robustness)
		visitBonus := math.Log(float64(visits)) / math.Log(float64(p.cfg.MaxIterations)) * 0.1
		score := reward + visitBonus

		if score > bestScore {
			bestScore = score
			best = &PlanCandidate{
				Plan:        plan,
				Strategy:    strategy,
				Visits:      visits,
				TotalReward: reward * float64(visits),
				LLMScore:    reward,
			}
		}
	}

	return best
}

// collectCandidates builds the candidate list for downstream consumers.
func (p *MCTSPlanner) collectCandidates() []PlanCandidate {
	var candidates []PlanCandidate
	visited := make(map[string]bool)

	var collect func(node *MCTSNode, depth int)
	collect = func(node *MCTSNode, depth int) {
		if node == nil || node.Plan == nil {
			return
		}
		hash := planHash(node.Plan)
		if visited[hash] {
			return
		}
		visited[hash] = true

		node.mu.Lock()
		c := PlanCandidate{
			Plan:        node.Plan,
			Strategy:    node.Strategy,
			Visits:      node.Visits,
			TotalReward: node.TotalReward,
			LLMScore:    node.MeanReward,
			Failed:      node.Failed,
		}
		node.mu.Unlock()
		candidates = append(candidates, c)

		for _, child := range node.Children {
			collect(child, depth+1)
		}
	}

	// Collect from root's children (depth 1+)
	for _, child := range p.root.Children {
		collect(child, 1)
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].LLMScore > candidates[j].LLMScore
	})

	return candidates
}

// generateRootCandidates creates the initial plan alternatives for MCTS.
func (p *MCTSPlanner) generateRootCandidates(ctx context.Context, state *CognitiveState) ([]PlanCandidate, error) {
	// Generate multiple strategies at root level
	hints := []strategyHint{
		{
			Name: "direct",
			Hint: "Prefer the simplest, most direct approach. Minimize tool calls. Use the most obvious solution.",
		},
		{
			Name: "thorough",
			Hint: "Verify each step before proceeding. Include diagnostic and validation checks. Prefer reliability over speed.",
		},
		{
			Name: "incremental",
			Hint: "Break the task into the smallest possible sub-steps. Solve one small piece at a time.",
		},
		{
			Name: "parallel",
			Hint: "Identify steps that can run concurrently. Plan for maximum parallelism. Use independent tool calls simultaneously.",
		},
		{
			Name: "defensive",
			Hint: "Anticipate failure modes at each step. Include rollback or cleanup actions. Plan for error recovery.",
		},
	}

	return p.generateCandidatesForHints(ctx, state, hints)
}

// generateAlternatives creates branching alternatives from an existing node.
func (p *MCTSPlanner) generateAlternatives(ctx context.Context, node *MCTSNode, state *CognitiveState) []PlanCandidate {
	// Generate alternative strategies that differ from the current node's approach
	var hints []strategyHint

	switch node.Strategy {
	case "direct":
		hints = []strategyHint{
			{
				Name: "direct-thorough",
				Hint: "Similar to the direct approach, but add a verification step after each critical action.",
			},
			{
				Name: "direct-parallel",
				Hint: "Take the direct approach but parallelize independent operations.",
			},
		}
	case "thorough":
		hints = []strategyHint{
			{
				Name: "thorough-optimized",
				Hint: "Maintain thoroughness but reduce unnecessary validation steps. Skip checks that won't change the outcome.",
			},
		}
	case "incremental":
		hints = []strategyHint{
			{
				Name: "incremental-batched",
				Hint: "Group related micro-steps into slightly larger batches to reduce overhead while keeping control.",
			},
		}
	case "parallel":
		hints = []strategyHint{
			{
				Name: "parallel-ordered",
				Hint: "Introduce ordering constraints between parallel groups to ensure correctness.",
			},
		}
	case "defensive":
		hints = []strategyHint{
			{
				Name: "defensive-streamlined",
				Hint: "Reduce rollback complexity by using simpler recovery mechanisms. Focus defenses on the most likely failures.",
			},
		}
	default:
		hints = []strategyHint{
			{
				Name: "general-refinement",
				Hint: "Refine the current plan by improving step ordering and reducing unnecessary work.",
			},
		}
	}

	candidates, err := p.generateCandidatesForHints(ctx, state, hints)
	if err != nil {
		slog.Debug("mcts: alternative generation failed", "strategy", node.Strategy, "error", err)
		return nil
	}
	return candidates
}

// generateCandidatesForHints calls the underlying planner for each strategy hint.
func (p *MCTSPlanner) generateCandidatesForHints(ctx context.Context, state *CognitiveState, hints []strategyHint) ([]PlanCandidate, error) {
	var candidates []PlanCandidate

	for _, hint := range hints {
		// Modify the state's prompt to include the strategy hint
		plan, err := p.planner.generateWithHint(ctx, state, hint)
		if err != nil {
			slog.Debug("mcts: candidate generation failed for hint",
				"hint", hint.Name, "error", err)
			continue
		}
		if plan == nil || len(plan.SubTasks) == 0 {
			continue
		}

		candidates = append(candidates, PlanCandidate{
			Plan:     plan,
			Strategy: hint.Name,
			LLMScore: p.scorePlan(plan),
			Visits:   1,
		})
	}

	return candidates, nil
}

// scorePlan assigns an initial LLM-based quality score to a plan.
func (p *MCTSPlanner) scorePlan(plan *TaskPlan) float64 {
	if plan == nil {
		return 0
	}
	// Start with structural heuristics
	score := 0.5

	// Higher score for plans with clear, well-described steps
	if len(plan.SubTasks) > 0 && len(plan.SubTasks) <= 7 {
		score += 0.15 // sweet spot: not too few, not too many
	}

	// Tool specificity bonus
	toolCount := 0
	for _, s := range plan.SubTasks {
		if s.ToolName != "" {
			toolCount++
		}
	}
	if toolCount > 0 {
		score += float64(toolCount) / float64(len(plan.SubTasks)) * 0.2
	}

	// Clear goal bonus
	if plan.Summary != "" {
		score += 0.1
	}

	return math.Min(score, 1.0)
}

// BestPlan returns the current best plan without running a new search.
// Used for quick lookups after a search has been performed.
func (p *MCTSPlanner) BestPlan() *PlanCandidate {
	return p.selectBestPlan()
}

// TreeStats returns statistics about the current search tree.
func (p *MCTSPlanner) TreeStats() map[string]interface{} {
	p.mu.RLock()
	nodeCount := len(p.nodesByID)
	p.mu.RUnlock()

	if p.root == nil {
		return map[string]interface{}{"nodes": 0}
	}

	p.root.mu.Lock()
	rootVisits := p.root.Visits
	rootChildren := len(p.root.Children)
	p.root.mu.Unlock()

	totalVisits := 0
	maxDepth := 0
	for _, child := range p.root.Children {
		child.mu.Lock()
		totalVisits += child.Visits
		if child.Depth > maxDepth {
			maxDepth = child.Depth
		}
		child.mu.Unlock()
	}

	return map[string]interface{}{
		"nodes":         nodeCount,
		"root_children": rootChildren,
		"root_visits":   rootVisits,
		"total_visits":  totalVisits,
		"max_depth":     maxDepth,
	}
}
