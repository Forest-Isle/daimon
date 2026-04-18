package eval

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// CognitiveAgentRunner implements AgentRunner by driving a real CognitiveAgent.
// Each task gets a fresh session and an EvalChannel that auto-approves tools.
type CognitiveAgentRunner struct {
	agent   *agent.CognitiveAgent
	hook    *EvalHook
	channel *EvalChannel

	mu              sync.Mutex
	lastObservation *agent.ObservationResult
}

// NewCognitiveAgentRunner creates a runner that drives the given CognitiveAgent.
// If the agent has an evolution engine configured, an EvalHook is registered to
// capture reflection/episode metrics. An observation callback is set on the
// agent to capture assertion statistics.
func NewCognitiveAgentRunner(ca *agent.CognitiveAgent) *CognitiveAgentRunner {
	r := &CognitiveAgentRunner{
		agent:   ca,
		channel: &EvalChannel{},
	}

	// Register observation callback to capture assertion stats.
	ca.SetObservationCallback(func(result *agent.ObservationResult) {
		r.mu.Lock()
		r.lastObservation = result
		r.mu.Unlock()
	})

	// Register eval hook on evolution engine if available.
	if evo := ca.EvolutionEngine(); evo != nil {
		r.hook = NewEvalHook()
		evo.RegisterHook(r.hook)
	}

	return r
}

// RunTask executes a single evaluation task against the cognitive agent.
func (r *CognitiveAgentRunner) RunTask(ctx context.Context, task TaskCase) (*EvalResult, error) {
	sessions := r.agent.Sessions()
	if sessions == nil {
		return nil, fmt.Errorf("cognitive agent has no session manager")
	}

	r.channel.Reset()
	r.mu.Lock()
	r.lastObservation = nil
	r.mu.Unlock()

	// Each eval task gets a unique channel ID to isolate sessions.
	evalChannelID := fmt.Sprintf("eval_%s_%d", task.ID, time.Now().UnixNano())

	sess, err := sessions.Get(ctx, "eval", evalChannelID)
	if err != nil {
		return nil, fmt.Errorf("create eval session: %w", err)
	}

	if r.hook != nil {
		r.hook.ClearSession(sess.ID)
	}

	msg := channel.InboundMessage{
		Channel:   "eval",
		ChannelID: evalChannelID,
		UserID:    "eval_user",
		UserName:  "eval",
		Text:      task.Goal,
	}

	start := time.Now()
	handleErr := r.agent.HandleMessage(ctx, r.channel, msg)
	duration := time.Since(start)

	// Wait for all async evolution hooks to finish before reading their state.
	if evo := r.agent.EvolutionEngine(); evo != nil {
		evo.WaitPending()
	}

	result := &EvalResult{
		TaskID:     task.ID,
		Goal:       task.Goal,
		Complexity: task.Complexity,
		Duration:   duration,
		Timestamp:  time.Now(),
	}

	if handleErr != nil {
		result.Error = handleErr.Error()
		return result, nil
	}

	r.populateFromObservation(result)
	r.populateFromEvolution(result, sess.ID)

	return result, nil
}

func (r *CognitiveAgentRunner) populateFromObservation(result *EvalResult) {
	r.mu.Lock()
	obs := r.lastObservation
	r.mu.Unlock()

	if obs == nil {
		return
	}

	result.AssertionTotal = len(obs.Assertions)
	passed := 0
	for _, a := range obs.Assertions {
		if a.Passed {
			passed++
		}
	}
	result.AssertionPassed = passed
	if result.AssertionTotal > 0 {
		result.AssertionPassRate = float64(passed) / float64(result.AssertionTotal)
	}

	var tools []string
	seen := make(map[string]bool)
	for _, o := range obs.Observations {
		if !seen[o.ToolName] {
			tools = append(tools, o.ToolName)
			seen[o.ToolName] = true
		}
	}
	result.ToolsUsed = tools
}

// CaptureSnapshot returns a point-in-time snapshot of the evolution subsystem.
// Returns a zero-valued snapshot when no evolution engine is configured.
func (r *CognitiveAgentRunner) CaptureSnapshot() *EvolutionSnapshot {
	snap := &EvolutionSnapshot{}
	evo := r.agent.EvolutionEngine()
	if evo == nil {
		return snap
	}
	if pl := evo.PreferenceLearnerHook(); pl != nil {
		snap.PreferenceCount = pl.EntryCount()
	}
	if so := evo.StrategyOptimizerHook(); so != nil {
		snap.StrategyVersion = so.GetStrategy().Version
	}
	return snap
}

func (r *CognitiveAgentRunner) populateFromEvolution(result *EvalResult, sessionID string) {
	if r.hook == nil {
		return
	}

	if ref := r.hook.GetReflection(sessionID); ref != nil {
		result.Success = ref.Succeeded
		result.Confidence = ref.Confidence
		result.ReplanCount = ref.ReplanCount
		if len(result.ToolsUsed) == 0 {
			result.ToolsUsed = ref.ToolsUsed
		}
	}

	if ep := r.hook.GetEpisode(sessionID); ep != nil {
		if result.ReplanCount == 0 {
			result.ReplanCount = ep.ReplanCount
		}
		if !result.Success {
			result.Success = ep.Succeeded
		}
	}
}
