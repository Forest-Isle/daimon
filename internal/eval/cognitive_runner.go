package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// CognitiveAgentRunner implements AgentRunner by driving a real CognitiveAgent.
// Each task gets a fresh session and an EvalChannel that auto-approves tools.
type CognitiveAgentRunner struct {
	agent        *agent.CognitiveAgent
	hook         *EvalHook
	channel      *EvalChannel
	cogCollector *cogmetrics.Collector

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

// SetCogCollector attaches the gateway's cogmetrics.Collector so that
// CaptureCogHealth() can return a populated HealthReport after the suite.
func (r *CognitiveAgentRunner) SetCogCollector(c *cogmetrics.Collector) {
	r.cogCollector = c
}

// CaptureCogHealth implements CogHealthCaptor. Returns nil when no collector
// is wired (e.g. evolution is disabled).
func (r *CognitiveAgentRunner) CaptureCogHealth() *cogmetrics.HealthReport {
	if r.cogCollector == nil {
		return nil
	}
	h := r.cogCollector.Snapshot()
	return &h
}

// CompressionEmitter returns a DashboardEmitter that routes context compression
// events into the eval hook. The gateway wires this into the context manager so
// that compression events are tracked even when the dashboard is disabled.
func (r *CognitiveAgentRunner) CompressionEmitter() agent.DashboardEmitter {
	if r.hook == nil {
		return nil
	}
	return &compressionAdapter{hook: r.hook}
}

// compressionAdapter is a thin agent.DashboardEmitter whose only live method is
// EmitContextCompress; all others are no-ops. This keeps EvalHook focused on
// evolution.Hook responsibility while still satisfying the full interface.
type compressionAdapter struct {
	hook *EvalHook
}

func (a *compressionAdapter) EmitContextCompress(sessionID, reason string, layersRun int, beforePct, afterPct float64) {
	a.hook.RecordCompression(sessionID, reason, layersRun, beforePct, afterPct)
}

func (a *compressionAdapter) EmitPhaseStart(_ string, _ string)                            {}
func (a *compressionAdapter) EmitPhaseEnd(_ string, _ string, _ int64)                     {}
func (a *compressionAdapter) EmitToolStart(_ string, _ string, _ string)                   {}
func (a *compressionAdapter) EmitToolEnd(_ string, _ string, _ bool, _ int64)              {}
func (a *compressionAdapter) EmitSessionStart(_ string, _ string)                          {}
func (a *compressionAdapter) EmitSessionEnd(_ string, _ bool, _ int64)                     {}
func (a *compressionAdapter) EmitMetricsUpdate(_ string, _, _ int, _ float64, _, _, _, _ int64, _, _ string) {
}
func (a *compressionAdapter) EmitPlanGenerated(_ string, _ int, _ string, _ bool)           {}
func (a *compressionAdapter) EmitReplanStart(_ string, _ int, _ string)                     {}
func (a *compressionAdapter) EmitObservationResult(_ string, _, _, _ int, _ float64)        {}
func (a *compressionAdapter) EmitSubAgentSpawn(_ string, _ string, _ string, _ string)      {}
func (a *compressionAdapter) EmitSubAgentComplete(_ string, _ string, _ bool, _ int64)      {}

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

	result.AgentOutput = r.channel.LastMessage()

	r.populateFromObservation(result)
	r.populateFromEvolution(result, sess.ID)

	if len(result.ToolsUsed) > 0 {
		result.AgentOutput += "\n\n[Tool Execution Summary: " + strings.Join(result.ToolsUsed, ", ") + "]"
	}

	r.populateSuccessFallback(result)

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
	if ss := evo.SkillSynthesizerHook(); ss != nil {
		snap.SkillDraftCount = ss.DraftCount()
	}
	if tr := evo.TrajectoryRecorderHook(); tr != nil {
		dir := tr.Dir()
		if dir != "" {
			since := time.Now().Add(-24 * time.Hour)
			if records, err := evolution.ReadTrajectories(dir, since, time.Now()); err == nil {
				snap.TrajectoryCount = len(records)
			}
		}
	}
	return snap
}

// populateSuccessFallback sets Success and Confidence when the evolution
// hook did not provide explicit success signals. This covers runs where
// evolution is disabled or did not emit events for the session.
func (r *CognitiveAgentRunner) populateSuccessFallback(result *EvalResult) {
	if result.Confidence > 0 {
		return // evolution hook already populated success data
	}

	r.mu.Lock()
	obs := r.lastObservation
	r.mu.Unlock()

	if result.Error != "" {
		return // hard error — leave Success=false
	}

	if obs != nil && result.AssertionTotal > 0 {
		result.Success = result.AssertionPassRate >= 0.8
		result.Confidence = result.AssertionPassRate
		return
	}

	// No assertions and no error — treat as success with moderate confidence.
	result.Success = true
	result.Confidence = 0.5
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

	if execs := r.hook.GetToolExecs(sessionID); len(execs) > 0 {
		statsMap := make(map[string]*ToolExecStat)
		for _, e := range execs {
			st, ok := statsMap[e.ToolName]
			if !ok {
				st = &ToolExecStat{ToolName: e.ToolName}
				statsMap[e.ToolName] = st
			}
			st.CallCount++
			if e.Succeeded {
				st.SuccessCount++
			} else {
				st.FailCount++
			}
			st.TotalDurationMs += e.DurationMs
		}

		stats := make([]ToolExecStat, 0, len(statsMap))
		for _, st := range statsMap {
			if st.CallCount > 0 {
				st.SuccessRate = float64(st.SuccessCount) / float64(st.CallCount)
				st.AvgDurationMs = float64(st.TotalDurationMs) / float64(st.CallCount)
			}
			stats = append(stats, *st)
		}
		sort.Slice(stats, func(i, j int) bool {
			return stats[i].ToolName < stats[j].ToolName
		})
		result.ToolExecStats = stats

		if len(result.ToolsUsed) == 0 {
			seen := make(map[string]bool)
			for _, e := range execs {
				if !seen[e.ToolName] {
					result.ToolsUsed = append(result.ToolsUsed, e.ToolName)
					seen[e.ToolName] = true
				}
			}
		}
	}

	result.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
		Succeeded:   result.Success,
		Progress:    result.AssertionPassRate,
		DurationMs:  result.Duration.Milliseconds(),
		ReplanCount: result.ReplanCount,
	})

	if compressions := r.hook.GetCompressions(sessionID); len(compressions) > 0 {
		result.CompressionCount = len(compressions)
		result.CompressionEvents = compressions
	}
}
