package eval

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/memory"
)

// indexRebuilder is satisfied by memory stores that support explicit index
// rebuilds (e.g. FileMemoryStore). Stores that don't implement it (mocks,
// in-memory stores) simply skip the rebuild step.
type indexRebuilder interface {
	RebuildIndex(ctx context.Context) error
}

// CognitiveAgentRunner implements AgentRunner by driving a real Agent.
// Each task gets a fresh session and an EvalChannel that auto-approves tools.
type CognitiveAgentRunner struct {
	agent        *agent.Agent
	hook         *EvalHook
	channel      *EvalChannel
	cogCollector *cogmetrics.Collector
	memStore     memory.Store // the agent's active store, wired by gateway.NewEvalRunner
	evoEngine    *evolution.Engine
}

// NewCognitiveAgentRunner creates a runner that drives the given Agent.
// The evolution engine provides hooks for capturing reflection/episode/metrics data.
func NewCognitiveAgentRunner(a *agent.Agent, evo *evolution.Engine) *CognitiveAgentRunner {
	r := &CognitiveAgentRunner{
		agent:     a,
		channel:   &EvalChannel{},
		evoEngine: evo,
	}

	if evo != nil {
		r.hook = NewEvalHook()
		evo.RegisterHook(r.hook)
	}

	return r
}

// MemoryAwareRunner is implemented by runners that can inject and clean up
// memory fixtures directly into the agent's active memory store.
type MemoryAwareRunner interface {
	// InjectMemory writes test entries into the agent's live memory store so
	// the PERCEIVE phase can retrieve them during task execution.
	InjectMemory(ctx context.Context, entries ...memory.Entry) error
	// CleanupMemory removes previously injected entries by ID.
	CleanupMemory(ctx context.Context, ids ...string) error
}

// SetMemoryStore attaches the gateway's memory store so that InjectMemory and
// CleanupMemory can write test fixtures directly into the store the agent reads
// from. Called by gateway.NewEvalRunner.
func (r *CognitiveAgentRunner) SetMemoryStore(s memory.Store) { r.memStore = s }

// InjectMemory writes entries into the agent's active memory store.
// Returns an error when no store is wired (memory disabled in eval gateway).
// After saving all entries it rebuilds the FTS5 index (when the store supports
// it) so that searches can find the injected entries immediately.
func (r *CognitiveAgentRunner) InjectMemory(ctx context.Context, entries ...memory.Entry) error {
	if r.memStore == nil {
		return fmt.Errorf("eval runner: memory store not available (memory disabled?)")
	}
	for _, e := range entries {
		if err := r.memStore.Save(ctx, e); err != nil {
			return fmt.Errorf("eval runner: inject memory entry %q: %w", e.ID, err)
		}
	}
	// Force index rebuild so FTS5 search can find injected entries immediately.
	if rebuilder, ok := r.memStore.(indexRebuilder); ok {
		if err := rebuilder.RebuildIndex(ctx); err != nil {
			slog.Warn("eval: rebuild memory index after inject", "err", err)
		}
	}
	return nil
}

// CleanupMemory removes previously injected entries by ID.
func (r *CognitiveAgentRunner) CleanupMemory(ctx context.Context, ids ...string) error {
	if r.memStore == nil {
		return nil
	}
	var firstErr error
	for _, id := range ids {
		if err := r.memStore.Delete(ctx, id); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("eval runner: cleanup memory entry %q: %w", id, err)
		}
	}
	return firstErr
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

func (a *compressionAdapter) EmitPhaseStart(_ string, _ string)                        {}
func (a *compressionAdapter) EmitPhaseEnd(_ string, _ string, _ int64)                 {}
func (a *compressionAdapter) EmitToolStart(_ string, _ string, _ string)               {}
func (a *compressionAdapter) EmitToolEnd(_ string, _ string, _ bool, _ int64)          {}
func (a *compressionAdapter) EmitSessionStart(_ string, _ string)                      {}
func (a *compressionAdapter) EmitSessionEnd(_ string, _ bool, _ int64)                 {}
func (a *compressionAdapter) EmitMetricsUpdate(_ string, _, _ int, _ float64, _, _, _, _ int64, _, _ string) {
}
func (a *compressionAdapter) EmitPlanGenerated(_ string, _ int, _ string, _ bool)      {}
func (a *compressionAdapter) EmitReplanStart(_ string, _ int, _ string)                {}
func (a *compressionAdapter) EmitObservationResult(_ string, _, _, _ int, _ float64)   {}
func (a *compressionAdapter) EmitSubAgentSpawn(_ string, _ string, _ string, _ string) {}
func (a *compressionAdapter) EmitSubAgentComplete(_ string, _ string, _ bool, _ int64) {}

// RunTask executes a single evaluation task against the agent.
func (r *CognitiveAgentRunner) RunTask(ctx context.Context, task TaskCase) (*EvalResult, error) {
	if task.ID == TaskIDSkillEvolutionDraftQuality {
		return RunSkillEvolutionDimensionCheck(ctx, task)
	}

	sessions := r.agent.Sessions()
	if sessions == nil {
		return nil, fmt.Errorf("agent has no session manager")
	}

	r.channel.Reset()

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
	if r.evoEngine != nil {
		r.evoEngine.WaitPending()
	}

	result := &EvalResult{
		TaskID:     task.ID,
		Goal:       task.Goal,
		Complexity: task.Complexity,
		Duration:   duration,
		Timestamp:  time.Now(),
	}

	// Capture routing decision for this task's complexity level.
	if r.evoEngine != nil {
		if rr := r.evoEngine.Router().SelectModel(task.Complexity); rr.Routed {
			result.RoutedModel = rr.Model
		}
	}

	if handleErr != nil {
		result.Error = handleErr.Error()
		return result, nil
	}

	result.AgentOutput = r.channel.LastMessage()

	r.populateFromEvolution(result, sess.ID)

	// Override episode reward to include simulated user feedback when set.
	if task.UserFeedback != 0 {
		result.UserFeedback = task.UserFeedback
		result.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
			Succeeded:    result.Success,
			Progress:     result.AssertionPassRate,
			DurationMs:   result.Duration.Milliseconds(),
			ReplanCount:  result.ReplanCount,
			UserFeedback: task.UserFeedback,
		})
	}

	// Feed assertion pass rate into the cogmetrics collector so HealthReport
	// reflects eval-run assertion quality in addition to live agent stats.
	if r.cogCollector != nil && result.AssertionTotal > 0 {
		r.cogCollector.RecordAssertionRate(result.AssertionPassRate)
	}

	if len(result.ToolsUsed) > 0 {
		result.AgentOutput += "\n\n[Tool Execution Summary: " + strings.Join(result.ToolsUsed, ", ") + "]"
	}

	r.populateSuccessFallback(result)

	return result, nil
}

// RunInsightsCycle implements InsightsTrigger. It triggers the evolution
// engine's insights feedback loop immediately, bypassing the 6-hour timer.
// Returns whether the cycle ran and a human-readable reason.
func (r *CognitiveAgentRunner) RunInsightsCycle() (ran bool, reason string) {
	if r.evoEngine == nil {
		return false, "evolution engine not configured"
	}
	if !r.evoEngine.IsEnabled() {
		return false, "evolution engine disabled"
	}
	r.evoEngine.RunInsightsCycle()
	return true, "insights cycle completed"
}

// TrajectoryCount implements InsightsTrigger. Returns the number of trajectory
// records written in the last 7 days by the evolution engine.
func (r *CognitiveAgentRunner) TrajectoryCount() int {
	if r.evoEngine == nil {
		return 0
	}
	dir := r.evoEngine.TrajectoryDir()
	if dir == "" {
		return 0
	}
	since := time.Now().Add(-7 * 24 * time.Hour)
	records, err := evolution.ReadTrajectories(dir, since, time.Now())
	if err != nil {
		return 0
	}
	return len(records)
}

// CaptureSnapshot returns a point-in-time snapshot of the evolution subsystem.
// Returns a zero-valued snapshot when no evolution engine is configured.
func (r *CognitiveAgentRunner) CaptureSnapshot() *EvolutionSnapshot {
	snap := &EvolutionSnapshot{}
	if r.evoEngine == nil {
		return snap
	}
	if pl := r.evoEngine.PreferenceLearnerHook(); pl != nil {
		snap.PreferenceCount = pl.EntryCount()
		populatePreferenceQuality(snap, pl)
	}
	if so := r.evoEngine.StrategyOptimizerHook(); so != nil {
		strategy := so.GetStrategy()
		snap.StrategyVersion = strategy.Version
		snap.ReplanThreshold = strategy.ReplanThreshold.Value
		snap.ReplanThresholdPrev = strategy.ReplanThreshold.Previous
		snap.ReplanThresholdReason = strategy.ReplanThreshold.Reason
		if len(strategy.ToolPriorities) > 0 {
			tp := make(map[string]float64, len(strategy.ToolPriorities))
			for tool, param := range strategy.ToolPriorities {
				tp[tool] = param.Value
			}
			snap.ToolPriorities = tp
		}
	}
	if ss := r.evoEngine.SkillSynthesizerHook(); ss != nil {
		snap.SkillDraftCount = ss.DraftCount()
	}
	if tr := r.evoEngine.TrajectoryRecorderHook(); tr != nil {
		dir := tr.Dir()
		if dir != "" {
			since := time.Now().Add(-7 * 24 * time.Hour)
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

	if result.Error != "" {
		return // hard error -- leave Success=false
	}

	// No evolution data and no error -- treat as success with moderate confidence.
	result.Success = true
	result.Confidence = 0.5
}

func (r *CognitiveAgentRunner) populateFromEvolution(result *EvalResult, sessionID string) {
	// Always compute episode reward -- it only depends on result fields.
	defer func() {
		result.EpisodeReward = evolution.ComputeReward(evolution.RewardInput{
			Succeeded:   result.Success,
			Progress:    result.AssertionPassRate,
			DurationMs:  result.Duration.Milliseconds(),
			ReplanCount: result.ReplanCount,
		})
	}()

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

	if compressions := r.hook.GetCompressions(sessionID); len(compressions) > 0 {
		result.CompressionCount = len(compressions)
		result.CompressionEvents = compressions
	}
}

// populatePreferenceQuality fills preference quality distribution metrics in
// the snapshot using all entries (not filtered by MinConfidence) so that the
// full distribution including low-confidence, recently observed entries is
// captured.
func populatePreferenceQuality(snap *EvolutionSnapshot, pl *evolution.PreferenceLearner) {
	toolEntries := pl.ListByCategory("tool_preference")
	complexityEntries := pl.ListByCategory("complexity_handling")

	snap.PreferenceToolCount = len(toolEntries)
	snap.PreferenceComplexityCount = len(complexityEntries)

	all := make([]evolution.PreferenceEntry, 0, len(toolEntries)+len(complexityEntries))
	all = append(all, toolEntries...)
	all = append(all, complexityEntries...)

	// Include replan_tendency entries in distribution too.
	all = append(all, pl.ListByCategory("replan_tendency")...)

	if len(all) == 0 {
		return
	}

	var sumConf float64
	for _, e := range all {
		sumConf += e.Confidence
		switch {
		case e.Confidence >= 0.8:
			snap.PreferenceHighConfCount++
		case e.Confidence >= 0.4:
			snap.PreferenceMedConfCount++
		default:
			snap.PreferenceLowConfCount++
		}
	}
	snap.PreferenceAvgConfidence = sumConf / float64(len(all))
}
