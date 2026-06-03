package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// TrustLevel represents the permission level assigned based on accumulated trust.
type TrustLevel int

const (
	TrustApproveAll  TrustLevel = iota // Every call requires explicit approval
	TrustNotify                        // User is notified but not asked for approval
	TrustAutoApprove                   // Tool runs without any prompt
)

// String returns a human-readable name for the trust level.
func (tl TrustLevel) String() string {
	switch tl {
	case TrustApproveAll:
		return "approve_all"
	case TrustNotify:
		return "notify"
	case TrustAutoApprove:
		return "auto_approve"
	default:
		return "unknown"
	}
}

// TrustLevelToPermission converts a TrustLevel to the corresponding PermissionAction.
func TrustLevelToPermission(level TrustLevel) PermissionAction {
	switch level {
	case TrustAutoApprove:
		return PermissionNone
	case TrustNotify:
		return PermissionNotify
	default:
		return PermissionApprove
	}
}

const (
	// promoteToNotifyThreshold is the consecutive approval count needed to move
	// from TrustApproveAll to TrustNotify.
	promoteToNotifyThreshold = 15

	// promoteToAutoThreshold is the consecutive approval count needed to move
	// from TrustNotify to TrustAutoApprove.
	promoteToAutoThreshold = 30

	// inputFingerprintLen is the number of hex chars from the SHA-256 hash used
	// to group similar inputs. 8 chars = ~4 billion buckets, sufficient to
	// differentiate "ls -la" from "rm -rf /" while still allowing identical safe
	// commands to accumulate trust.
	inputFingerprintLen = 8
)

// inputFingerprint returns a short hash prefix of the tool input, used to
// prevent tool-name-only trust accumulation. Two calls with the same
// (toolName, fingerprint) accumulate trust together; different inputs
// start from zero.
func inputFingerprint(input string) string {
	if len(input) == 0 {
		return "_empty_"
	}
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])[:inputFingerprintLen]
}

// ToolTrustStats tracks per-tool per-input-fingerprint trust metrics.
type ToolTrustStats struct {
	ApproveCount   int        `json:"approve_count"`
	RejectCount    int        `json:"reject_count"`
	ConsecutiveOK  int        `json:"consecutive_ok"`
	LastApprovedAt time.Time  `json:"last_approved_at"`
	CurrentLevel   TrustLevel `json:"current_level"`
}

// TrustTracker maintains per-tool trust statistics and computes suggested
// permission levels based on accumulated approval/rejection history.
// Trust is tracked per (toolName, inputFingerprint) so that executing safe
// commands (e.g., "ls -la") does not build trust for dangerous ones
// (e.g., "rm -rf /") even though they share the same tool name.
// Trust resets on rejection — a single rejection drops the fingerprint
// back to the most restrictive level.
type TrustTracker struct {
	mu    sync.RWMutex
	stats map[string]*ToolTrustStats // key: toolName + ":" + fingerprint
}

// NewTrustTracker creates a new TrustTracker with empty statistics.
func NewTrustTracker() *TrustTracker {
	return &TrustTracker{
		stats: make(map[string]*ToolTrustStats),
	}
}

// statKey builds the compound key for (toolName, input).
func statKey(toolName, input string) string {
	return toolName + ":" + inputFingerprint(input)
}

// RecordApproval records a successful tool approval and advances trust.
func (t *TrustTracker) RecordApproval(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordApprovalLocked(toolName, "")
}

// RecordApprovalWithInput records a successful tool approval with input
// fingerprinting. Use this when the tool input is available — it provides
// finer-grained trust tracking than RecordApproval.
func (t *TrustTracker) RecordApprovalWithInput(toolName, input string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordApprovalLocked(toolName, input)
}

func (t *TrustTracker) recordApprovalLocked(toolName, input string) {
	s := t.getOrCreateLocked(statKey(toolName, input))
	s.ApproveCount++
	s.ConsecutiveOK++
	s.LastApprovedAt = time.Now()

	if s.CurrentLevel == TrustApproveAll && s.ConsecutiveOK >= promoteToNotifyThreshold && s.RejectCount == 0 {
		s.CurrentLevel = TrustNotify
	}
	if s.CurrentLevel == TrustNotify && s.ConsecutiveOK >= promoteToAutoThreshold && s.RejectCount == 0 {
		s.CurrentLevel = TrustAutoApprove
	}
}

// RecordRejection records a tool rejection and resets trust to the most restrictive level.
func (t *TrustTracker) RecordRejection(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordRejectionLocked(toolName, "")
}

// RecordRejectionWithInput records a tool rejection with input fingerprinting.
func (t *TrustTracker) RecordRejectionWithInput(toolName, input string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordRejectionLocked(toolName, input)
}

func (t *TrustTracker) recordRejectionLocked(toolName, input string) {
	s := t.getOrCreateLocked(statKey(toolName, input))
	s.RejectCount++
	s.ConsecutiveOK = 0
	s.CurrentLevel = TrustApproveAll
}

// SuggestedLevel returns the recommended permission level for a tool based on
// its trust history. Uses an empty input fingerprint — callers that have the
// actual input should use SuggestedLevelForInput for finer-grained results.
func (t *TrustTracker) SuggestedLevel(toolName string) TrustLevel {
	return t.SuggestedLevelForInput(toolName, "")
}

// SuggestedLevelForInput returns the recommended permission level based on
// the tool's trust history for the given input fingerprint. Trust accumulated
// for one input (e.g., "ls -la") does not affect another (e.g., "rm -rf /").
func (t *TrustTracker) SuggestedLevelForInput(toolName, input string) TrustLevel {
	t.mu.RLock()
	defer t.mu.RUnlock()

	s, ok := t.stats[statKey(toolName, input)]
	if !ok {
		return TrustApproveAll
	}
	return s.CurrentLevel
}

// GetStats returns aggregated trust statistics for a tool across all input
// fingerprints, or nil if the tool is not tracked.
func (t *TrustTracker) GetStats(toolName string) *ToolTrustStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	prefix := toolName + ":"
	var agg ToolTrustStats
	agg.CurrentLevel = TrustAutoApprove // will be lowered by worst fingerprint
	found := false
	for k, s := range t.stats {
		if k == toolName || (len(k) > len(prefix) && k[:len(prefix)] == prefix) {
			found = true
			agg.ApproveCount += s.ApproveCount
			agg.RejectCount += s.RejectCount
			agg.ConsecutiveOK += s.ConsecutiveOK
			if s.LastApprovedAt.After(agg.LastApprovedAt) {
				agg.LastApprovedAt = s.LastApprovedAt
			}
			// Worst fingerprint determines aggregate level
			if s.CurrentLevel < agg.CurrentLevel {
				agg.CurrentLevel = s.CurrentLevel
			}
		}
	}
	if !found {
		return nil
	}
	return &agg
}

// Reset clears all trust statistics (e.g., at session start).
func (t *TrustTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats = make(map[string]*ToolTrustStats)
}

// ResetTool clears trust statistics for a single tool (all fingerprints).
func (t *TrustTracker) ResetTool(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	prefix := toolName + ":"
	for k := range t.stats {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(t.stats, k)
		}
	}
}

func (t *TrustTracker) getOrCreateLocked(key string) *ToolTrustStats {
	s, ok := t.stats[key]
	if !ok {
		s = &ToolTrustStats{CurrentLevel: TrustApproveAll}
		t.stats[key] = s
	}
	return s
}
