package tool

import (
	"testing"
)

func TestTrustTracker_NewTracker(t *testing.T) {
	tt := NewTrustTracker()
	if tt == nil {
		t.Fatal("NewTrustTracker returned nil")
	}
	level := tt.SuggestedLevel("unknown_tool")
	if level != TrustApproveAll {
		t.Errorf("unknown tool should have TrustApproveAll, got %v", level)
	}
}

func TestTrustTracker_RecordApproval(t *testing.T) {
	tt := NewTrustTracker()

	for i := 0; i < 10; i++ {
		tt.RecordApproval("bash")
	}

	stats := tt.GetStats("bash")
	if stats == nil {
		t.Fatal("expected stats for bash")
	}
	if stats.ApproveCount != 10 {
		t.Errorf("expected 10 approvals, got %d", stats.ApproveCount)
	}
	if stats.ConsecutiveOK != 10 {
		t.Errorf("expected 10 consecutive, got %d", stats.ConsecutiveOK)
	}
	if stats.CurrentLevel != TrustApproveAll {
		t.Errorf("expected TrustApproveAll at 10 approvals, got %v", stats.CurrentLevel)
	}
}

func TestTrustTracker_PromoteToNotify(t *testing.T) {
	tt := NewTrustTracker()

	for i := 0; i < promoteToNotifyThreshold; i++ {
		tt.RecordApproval("file_read")
	}

	level := tt.SuggestedLevel("file_read")
	if level != TrustNotify {
		t.Errorf("expected TrustNotify after %d approvals, got %v", promoteToNotifyThreshold, level)
	}
}

func TestTrustTracker_PromoteToAutoApprove(t *testing.T) {
	tt := NewTrustTracker()

	for i := 0; i < promoteToAutoThreshold; i++ {
		tt.RecordApproval("file_read")
	}

	level := tt.SuggestedLevel("file_read")
	if level != TrustAutoApprove {
		t.Errorf("expected TrustAutoApprove after %d approvals, got %v", promoteToAutoThreshold, level)
	}
}

func TestTrustTracker_RejectionResetsLevel(t *testing.T) {
	tt := NewTrustTracker()

	// Build up trust
	for i := 0; i < promoteToAutoThreshold; i++ {
		tt.RecordApproval("bash")
	}
	if tt.SuggestedLevel("bash") != TrustAutoApprove {
		t.Fatal("should be auto-approve before rejection")
	}

	// Single rejection resets everything
	tt.RecordRejection("bash")

	level := tt.SuggestedLevel("bash")
	if level != TrustApproveAll {
		t.Errorf("expected TrustApproveAll after rejection, got %v", level)
	}

	stats := tt.GetStats("bash")
	if stats.ConsecutiveOK != 0 {
		t.Errorf("expected 0 consecutive after rejection, got %d", stats.ConsecutiveOK)
	}
}

func TestTrustTracker_RejectionPreventsPromotion(t *testing.T) {
	tt := NewTrustTracker()

	// Approve many times but with one early rejection
	tt.RecordApproval("http")
	tt.RecordRejection("http")

	for i := 0; i < promoteToNotifyThreshold; i++ {
		tt.RecordApproval("http")
	}

	// RejectCount > 0, so should not promote even with enough consecutive
	level := tt.SuggestedLevel("http")
	if level != TrustApproveAll {
		t.Errorf("expected TrustApproveAll (reject count > 0), got %v", level)
	}
}

func TestTrustTracker_Reset(t *testing.T) {
	tt := NewTrustTracker()

	for i := 0; i < 20; i++ {
		tt.RecordApproval("bash")
	}

	tt.Reset()

	if stats := tt.GetStats("bash"); stats != nil {
		t.Error("expected nil stats after reset")
	}
}

func TestTrustTracker_ResetTool(t *testing.T) {
	tt := NewTrustTracker()
	tt.RecordApproval("bash")
	tt.RecordApproval("file_read")

	tt.ResetTool("bash")

	if tt.GetStats("bash") != nil {
		t.Error("expected nil stats for bash after ResetTool")
	}
	if tt.GetStats("file_read") == nil {
		t.Error("file_read stats should not be affected")
	}
}

func TestTrustTracker_GetStats_ReturnsACopy(t *testing.T) {
	tt := NewTrustTracker()
	tt.RecordApproval("bash")

	stats := tt.GetStats("bash")
	stats.ApproveCount = 999

	original := tt.GetStats("bash")
	if original.ApproveCount == 999 {
		t.Error("GetStats should return a copy, not a reference")
	}
}

func TestTrustLevel_String(t *testing.T) {
	tests := []struct {
		level TrustLevel
		want  string
	}{
		{TrustApproveAll, "approve_all"},
		{TrustNotify, "notify"},
		{TrustAutoApprove, "auto_approve"},
		{TrustLevel(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("TrustLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestTrustLevelToPermission(t *testing.T) {
	if TrustLevelToPermission(TrustAutoApprove) != PermissionNone {
		t.Error("auto-approve should map to PermissionNone")
	}
	if TrustLevelToPermission(TrustNotify) != PermissionNotify {
		t.Error("notify should map to PermissionNotify")
	}
	if TrustLevelToPermission(TrustApproveAll) != PermissionApprove {
		t.Error("approve-all should map to PermissionApprove")
	}
}
