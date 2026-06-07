package agent

import "testing"

// TestAppendStopNotice verifies that responses cut off by the max-token limit
// or stopped abnormally (e.g. content filtered) are flagged to the user, rather
// than a partial/empty answer being silently presented as complete (the prior
// behavior, where stopReason was discarded).
func TestAppendStopNotice(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		stopReason StopReason
		wantSuffix string // "" means text must be unchanged
	}{
		{
			name:       "truncated response gets a notice",
			text:       "here is a partial ans",
			stopReason: StopMaxToken,
			wantSuffix: noticeMaxTokens,
		},
		{
			name:       "abnormal stop gets a notice",
			text:       "partial",
			stopReason: StopAbnormal,
			wantSuffix: noticeAbnormal,
		},
		{
			name:       "normal completion is unchanged",
			text:       "here is the full answer",
			stopReason: StopEndTurn,
			wantSuffix: "",
		},
		{
			name:       "tool-use stop is unchanged",
			text:       "calling tools",
			stopReason: StopToolUse,
			wantSuffix: "",
		},
		{
			name:       "empty truncated text still surfaces the notice",
			text:       "",
			stopReason: StopMaxToken,
			wantSuffix: noticeMaxTokens,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendStopNotice(tt.text, tt.stopReason)
			if tt.wantSuffix == "" {
				if got != tt.text {
					t.Errorf("expected unchanged %q, got %q", tt.text, got)
				}
				return
			}
			if len(got) < len(tt.wantSuffix) || got[len(got)-len(tt.wantSuffix):] != tt.wantSuffix {
				t.Errorf("expected suffix %q, got %q", tt.wantSuffix, got)
			}
		})
	}
}
