package tool

import (
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
)

// ToolTrustStats tracks per-tool trust metrics.
type ToolTrustStats struct {
	ApproveCount   int        `json:"approve_count"`
	RejectCount    int        `json:"reject_count"`
	ConsecutiveOK  int        `json:"consecutive_ok"`
	LastApprovedAt time.Time  `json:"last_approved_at"`
	CurrentLevel   TrustLevel `json:"current_level"`
}

// TrustTracker maintains per-tool trust statistics and computes suggested
// permission levels based on accumulated approval/rejection history.
// Trust resets on rejection — a single rejection drops the tool back to
// the most restrictive level.
type TrustTracker struct {
	mu    sync.RWMutex
	stats map[string]*ToolTrustStats
}

// NewTrustTracker creates a new TrustTracker with empty statistics.
func NewTrustTracker() *TrustTracker {
	return &TrustTracker{
		stats: make(map[string]*ToolTrustStats),
	}
}

// RecordApproval records a successful tool approval and advances trust.
func (t *TrustTracker) RecordApproval(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.getOrCreate(toolName)
	s.ApproveCount++
	s.ConsecutiveOK++
	s.LastApprovedAt = time.Now()

	// Auto-promote based on consecutive approvals
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

	s := t.getOrCreate(toolName)
	s.RejectCount++
	s.ConsecutiveOK = 0
	s.CurrentLevel = TrustApproveAll
}

// SuggestedLevel returns the recommended permission level for a tool based on its trust history.
func (t *TrustTracker) SuggestedLevel(toolName string) TrustLevel {
	t.mu.RLock()
	defer t.mu.RUnlock()

	s, ok := t.stats[toolName]
	if !ok {
		return TrustApproveAll
	}
	return s.CurrentLevel
}

// GetStats returns a copy of the trust statistics for a tool, or nil if not tracked.
func (t *TrustTracker) GetStats(toolName string) *ToolTrustStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	s, ok := t.stats[toolName]
	if !ok {
		return nil
	}
	// Return a copy
	cp := *s
	return &cp
}

// Reset clears all trust statistics (e.g., at session start).
func (t *TrustTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats = make(map[string]*ToolTrustStats)
}

// ResetTool clears trust statistics for a single tool.
func (t *TrustTracker) ResetTool(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.stats, toolName)
}

func (t *TrustTracker) getOrCreate(toolName string) *ToolTrustStats {
	s, ok := t.stats[toolName]
	if !ok {
		s = &ToolTrustStats{CurrentLevel: TrustApproveAll}
		t.stats[toolName] = s
	}
	return s
}
