package action

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Forest-Isle/daimon/internal/tool"
	"github.com/google/uuid"
)

// Interceptor records every governed (non-read-only) tool execution in the
// trust ledger and stamps the reversibility class onto the result as a receipt.
// It does not block — gating stays with the permission interceptor for now — so
// it sits inside the permission gate and only sees calls that were allowed to
// run. Its job in this phase is to build the trust track record and make
// reversibility visible; enforcement (holds, trust-gated approval) lands once
// compensable/irreversible life tools and the hold-execution loop exist.
type Interceptor struct {
	store       *Store
	classifier  Classifier
	gate        ValueGate
	notifier    TrustNotifier
	holdEnabled bool
	holdWindow  time.Duration
}

// NewInterceptor builds the action interceptor. A nil classifier uses the
// default. The value gate is nil (observe-only); use NewInterceptorWithGate to
// enable ask-once enforcement.
func NewInterceptor(store *Store, classifier Classifier) *Interceptor {
	return NewInterceptorWithGate(store, classifier, nil)
}

// NewInterceptorWithGate builds the action interceptor with a value gate at the
// head of the pipeline. A nil gate leaves the interceptor observe-only.
func NewInterceptorWithGate(store *Store, classifier Classifier, gate ValueGate) *Interceptor {
	return NewInterceptorWithGateAndHold(store, classifier, gate, false, 0)
}

// NewInterceptorWithGateAndHold builds the action interceptor with optional
// compensable-action hold queue enforcement.
func NewInterceptorWithGateAndHold(store *Store, classifier Classifier, gate ValueGate, holdEnabled bool, holdWindow time.Duration) *Interceptor {
	if classifier == nil {
		classifier = NewClassifier()
	}
	if holdWindow <= 0 {
		holdWindow = 120 * time.Second
	}
	return &Interceptor{store: store, classifier: classifier, gate: gate, holdEnabled: holdEnabled, holdWindow: holdWindow}
}

func (i *Interceptor) Name() string { return "action" }

func (i *Interceptor) SetTrustNotifier(n TrustNotifier) { i.notifier = n }

// dryRunResult is the synthetic receipt returned for a governed call under a dry
// run: no tool ran, so it carries no real output — only metadata describing the
// would-be action so the shadow's transcript records what it intended.
func dryRunResult(toolName string, class Class) *tool.ToolResult {
	return &tool.ToolResult{
		Output: "[dry-run] " + toolName + " not executed (shadow record-only)",
		Metadata: map[string]string{
			"dry_run":      "true",
			"action_class": class.String(),
		},
	}
}

func heldResult(toolName, holdID string, class Class, window time.Duration) *tool.ToolResult {
	seconds := int(window.Seconds())
	return &tool.ToolResult{
		Output: fmt.Sprintf("[held] %s queued for execution in %ds; recall with: daimon holds recall %s", toolName, seconds, holdID),
		Metadata: map[string]string{
			"action_class": class.String(),
			"held":         "true",
			"hold_id":      holdID,
		},
	}
}

func (i *Interceptor) Intercept(ctx context.Context, call *tool.ToolCall, next tool.InterceptorFunc) (*tool.ToolResult, error) {
	class, governed := i.classifier.Classify(call)

	// Shadow dry-run: governed (side-effecting) actions are short-circuited to
	// record-only — the tool does not run and no trust/undo/hold state changes,
	// so the shadow brain can reason without touching the world. Read-only calls
	// fall through and execute normally (the shadow still needs to observe). This
	// is fail-closed: only a caller that opts in via tool.WithDryRun carries the
	// flag, so production request contexts always execute for real.
	if governed && tool.IsDryRun(ctx) {
		return dryRunResult(call.ToolName, class), nil
	}

	contextKey := i.classifier.ContextKey(call)

	// Values segment (pipeline head): a governed, non-low-risk action needs an
	// explicit permitting source to run autonomously. The gate decides; if it
	// refuses, the action is not released (returned blocked, tool not executed).
	// Reversible (low-risk) actions are exempt — they are undoable and execute
	// freely. A nil gate disables the check (observe-only default).
	valueRef := ""
	if governed && class != Reversible && i.gate != nil {
		ref, permitted := i.gate.Permit(ctx, class, contextKey)
		if !permitted {
			return valueBlockedResult(call.ToolName, class), nil
		}
		valueRef = ref
	}

	if governed && class == Compensable && i.holdEnabled && !tool.IsDryRun(ctx) && i.store != nil {
		holdID := "hold_" + uuid.NewString()
		executeAt := time.Now().UTC().Add(i.holdWindow).Format("2006-01-02 15:04:05")
		if err := i.store.CreateHold(ctx, Hold{
			ID:        holdID,
			ToolName:  call.ToolName,
			Payload:   call.Input,
			ExecuteAt: executeAt,
			State:     "pending",
		}); err != nil {
			return nil, fmt.Errorf("create hold: %w", err)
		}
		// A queued compensable action is a governed, not-yet-verified action.
		// Record it (unverified) so the episode that queued it is not counted
		// distill-clean: queuing a side-effect is not the same as taking none.
		tool.ActionCollectorFromContext(ctx).Record(false)
		return heldResult(call.ToolName, holdID, class, i.holdWindow), nil
	}

	// Snapshot the target file's prior state BEFORE execution so a reversible file
	// mutation can be reversed. Best-effort: capture never blocks the tool.
	var undo UndoRecord
	captureUndo := false
	if governed && i.store != nil && class == Reversible {
		undo, captureUndo = captureFileUndo(ctx, call.ToolName, call.Input)
	}
	if captureUndo {
		undo.EpisodeID = tool.EpisodeIDFromContext(ctx)
	}

	result, err := next(ctx, call)

	if !governed {
		return result, err
	}

	succeeded := err == nil && (result == nil || result.Error == "")
	// Only reversible actions earn autonomy from a clean execution: they are
	// undoable, so a successful run is sufficient evidence. Compensable and
	// irreversible actions record the attempt but never auto-verify on mere
	// success — they stay at ask-every until an explicit objective verification
	// mechanism marks them, keeping high-stakes actions behind a human gate.
	verified := succeeded && class == Reversible

	// Report this governed action into the episode's verification collector (if the
	// caller installed one). This is about the action, not the trust ledger, so it
	// happens for EVERY governed call — independent of whether a trust store is
	// wired. Doing it before the store-nil guard prevents a governed (possibly
	// unverified) action from being silently dropped to "0 unverified" when no
	// store is configured, which would wrongly let its episode look distill-clean.
	// Read-only calls returned above, so only governed actions are counted.
	// Observational — never affects the result.
	tool.ActionCollectorFromContext(ctx).Record(verified)

	if i.store == nil {
		return result, err
	}

	change, recErr := i.store.RecordAttempt(ctx, class, contextKey, verified)
	if recErr != nil {
		slog.Warn("action: record trust attempt failed", "tool", call.ToolName, "err", recErr)
	} else if change.Promoted && i.notifier != nil {
		i.notifier.TrustPromoted(ctx, class, contextKey, change.From, change.To)
	}
	if recErr == nil && class == Reversible {
		valueRef = "trust:" + change.Level.String()
	}

	if result != nil {
		if result.Metadata == nil {
			result.Metadata = map[string]string{}
		}
		result.Metadata["action_class"] = class.String()
		// Stamp the permitting source on the receipt so every autonomous action
		// can be traced back to the value/trust decision that allowed it.
		if valueRef != "" {
			result.Metadata["value_ref"] = valueRef
		}
	}

	// A successful reversible file mutation earns an undo journal entry; its
	// receipt id is stamped onto the result so callers can reference the action.
	if captureUndo && succeeded {
		if recErr := i.store.RecordUndo(ctx, undo); recErr != nil {
			slog.Warn("action: record undo failed", "tool", call.ToolName, "err", recErr)
		} else if result != nil {
			result.Metadata["receipt_id"] = undo.ReceiptID
		}
	}
	return result, err
}
