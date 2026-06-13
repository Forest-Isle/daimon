package action

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/daimon/internal/tool"
)

// Interceptor records every governed (non-read-only) tool execution in the
// trust ledger and stamps the reversibility class onto the result as a receipt.
// It does not block — gating stays with the permission interceptor for now — so
// it sits inside the permission gate and only sees calls that were allowed to
// run. Its job in this phase is to build the trust track record and make
// reversibility visible; enforcement (holds, trust-gated approval) lands once
// compensable/irreversible life tools and the hold-execution loop exist.
type Interceptor struct {
	store      *Store
	classifier Classifier
	gate       ValueGate
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
	if classifier == nil {
		classifier = NewClassifier()
	}
	return &Interceptor{store: store, classifier: classifier, gate: gate}
}

func (i *Interceptor) Name() string { return "action" }

func (i *Interceptor) Intercept(ctx context.Context, call *tool.ToolCall, next tool.InterceptorFunc) (*tool.ToolResult, error) {
	class, governed := i.classifier.Classify(call)
	contextKey := i.classifier.ContextKey(call)

	// Values segment (pipeline head): a governed, non-low-risk action needs an
	// explicit permitting source to run autonomously. The gate decides; if it
	// refuses, the action is not released (returned blocked, tool not executed).
	// Reversible (low-risk) actions are exempt — they are undoable and execute
	// freely. A nil gate disables the check (observe-only default).
	valueRef := ""
	if governed && class == Reversible {
		valueRef = "reversible"
	}
	if governed && class != Reversible && i.gate != nil {
		ref, permitted := i.gate.Permit(ctx, class, contextKey)
		if !permitted {
			return valueBlockedResult(call.ToolName, class), nil
		}
		valueRef = ref
	}

	// Snapshot the target file's prior state BEFORE execution so a reversible file
	// mutation can be reversed. Best-effort: capture never blocks the tool.
	var undo UndoRecord
	captureUndo := false
	if governed && i.store != nil && class == Reversible {
		undo, captureUndo = captureFileUndo(ctx, call.ToolName, call.Input)
	}

	result, err := next(ctx, call)

	if !governed || i.store == nil {
		return result, err
	}

	succeeded := err == nil && (result == nil || result.Error == "")
	// Only reversible actions earn autonomy from a clean execution: they are
	// undoable, so a successful run is sufficient evidence. Compensable and
	// irreversible actions record the attempt but never auto-verify on mere
	// success — they stay at ask-every until an explicit objective verification
	// mechanism marks them, keeping high-stakes actions behind a human gate.
	verified := succeeded && class == Reversible
	if recErr := i.store.RecordAttempt(ctx, class, contextKey, verified); recErr != nil {
		slog.Warn("action: record trust attempt failed", "tool", call.ToolName, "err", recErr)
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
