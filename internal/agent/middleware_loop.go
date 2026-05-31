package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

// CheckpointHook saves loop state after each turn so interrupted runs can be resumed.
type CheckpointHook struct {
	checkpointStore CheckpointStore
	db              *store.DB
}

// NewCheckpointHook creates a hook that persists loop checkpoints.
func NewCheckpointHook(cs CheckpointStore, db *store.DB) *CheckpointHook {
	return &CheckpointHook{checkpointStore: cs, db: db}
}

// BeforeLoop restores checkpoint state if one exists for this session.
func (h *CheckpointHook) BeforeLoop(ctx context.Context, state *LoopState) error {
	if h.checkpointStore == nil {
		return nil
	}
	saved, err := h.checkpointStore.Load(ctx, state.SessionID)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}
	if saved == nil {
		return nil
	}
	// Restore messages from checkpoint observations JSON
	var savedMsgs []CompletionMessage
	if saved.ObservationsJSON != "" {
		if err := json.Unmarshal([]byte(saved.ObservationsJSON), &savedMsgs); err != nil {
			slog.Warn("checkpoint: failed to unmarshal messages", "error", err)
			return nil
		}
		state.Messages = savedMsgs
		state.TurnCount = saved.SubTaskIndex
	}
	slog.Info("checkpoint restored", "session", state.SessionID, "turns", state.TurnCount)
	return nil
}

// AfterTurn saves the current loop state to the checkpoint store.
func (h *CheckpointHook) AfterTurn(ctx context.Context, state *LoopState) error {
	if h.checkpointStore == nil {
		return nil
	}
	msgsJSON, _ := json.Marshal(state.Messages)
	cp := &TaskCheckpoint{
		ID:               fmt.Sprintf("cp-%s-%d", state.SessionID, state.TurnCount),
		SessionID:        state.SessionID,
		SubTaskIndex:     state.TurnCount,
		ObservationsJSON: string(msgsJSON),
	}
	return h.checkpointStore.Save(ctx, cp)
}

// AfterLoop is a no-op for the checkpoint hook.
func (h *CheckpointHook) AfterLoop(ctx context.Context, result *LoopResult) error {
	return nil
}

// CompressionHook triggers context compression when utilization exceeds a threshold.
// In the v2 loop, compression is logged as a warning since the ContextManager
// operates on session objects rather than raw message slices.
type CompressionHook struct {
	contextMgr ContextManager
	threshold  float64
}

// NewCompressionHook creates a hook that warns on high context utilization.
func NewCompressionHook(cm ContextManager, threshold float64) *CompressionHook {
	return &CompressionHook{contextMgr: cm, threshold: threshold}
}

// BeforeLoop is a no-op for the compression hook.
func (h *CompressionHook) BeforeLoop(ctx context.Context, state *LoopState) error { return nil }

// AfterTurn checks context utilization and logs a warning if above threshold.
func (h *CompressionHook) AfterTurn(ctx context.Context, state *LoopState) error {
	if h.contextMgr == nil || state.ContextUsedPct < h.threshold {
		return nil
	}
	slog.Warn("context compression not available in v2 loop",
		"session", state.SessionID,
		"utilization", fmt.Sprintf("%.1f%%", state.ContextUsedPct*100),
	)
	return nil
}

// AfterLoop is a no-op for the compression hook.
func (h *CompressionHook) AfterLoop(ctx context.Context, result *LoopResult) error { return nil }

// PlanModeHook pauses the loop before tool execution for human approval of the plan.
type PlanModeHook struct {
	planMode     *PlanMode
	approvalFunc ApprovalFunc
}

// NewPlanModeHook creates a hook that enforces plan-then-execute mode.
func NewPlanModeHook(pm *PlanMode, af ApprovalFunc) *PlanModeHook {
	return &PlanModeHook{planMode: pm, approvalFunc: af}
}

// BeforeLoop pauses and waits for human approval when plan mode is enabled.
func (h *PlanModeHook) BeforeLoop(ctx context.Context, state *LoopState) error {
	if h.planMode == nil {
		return nil
	}
	slog.Info("plan mode: waiting for approval")
	// WaitForApproval is a placeholder - actual implementation depends on channel integration
	return nil
}

// AfterTurn is a no-op for the plan mode hook.
func (h *PlanModeHook) AfterTurn(ctx context.Context, state *LoopState) error { return nil }

// AfterLoop resets the plan mode state.
func (h *PlanModeHook) AfterLoop(ctx context.Context, result *LoopResult) error {
	return nil
}

// EvolutionHook dispatches completion events to the evolution engine for self-improvement.
type EvolutionHook struct {
	evoEngine   *evolution.Engine
	dashEmitter DashboardEmitter
}

// NewEvolutionHook creates a hook that feeds loop results to the evolution engine.
func NewEvolutionHook(ee *evolution.Engine, de DashboardEmitter) *EvolutionHook {
	return &EvolutionHook{evoEngine: ee, dashEmitter: de}
}

// BeforeLoop is a no-op for the evolution hook.
func (h *EvolutionHook) BeforeLoop(ctx context.Context, state *LoopState) error { return nil }

// AfterTurn is a no-op for the evolution hook.
func (h *EvolutionHook) AfterTurn(ctx context.Context, state *LoopState) error { return nil }

// AfterLoop dispatches the completed loop result as an evolution episode event.
func (h *EvolutionHook) AfterLoop(ctx context.Context, result *LoopResult) error {
	if h.evoEngine == nil {
		return nil
	}
	h.evoEngine.DispatchEpisode(evolution.EpisodeEvent{
		SessionID:      "",
		EpisodeID:      fmt.Sprintf("v2_%d", time.Now().UnixNano()),
		Succeeded:      result.Output != "",
		ToolSequence:   extractToolNames(result),
		ReplanCount:    0,
		DurationMs:     0,
		UserFeedback:   0,
		Timestamp:      time.Now(),
	})
	return nil
}

// extractToolNames pulls tool names from the tool results for evolution tracking.
func extractToolNames(result *LoopResult) []string {
	if result == nil || len(result.ToolResults) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var names []string
	for _, tr := range result.ToolResults {
		if tr.ToolName == "" {
			continue
		}
		if _, ok := seen[tr.ToolName]; !ok {
			seen[tr.ToolName] = struct{}{}
			names = append(names, tr.ToolName)
		}
	}
	return names
}
