package agent

import (
	"strings"
	"testing"
)

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

func TestFormatToolCallStatus(t *testing.T) {
	tests := []struct {
		name  string
		calls []ToolUseBlock
		want  []string
		nope  []string
	}{
		{
			name:  "empty calls",
			calls: nil,
			want:  []string{"Calling tools"},
		},
		{
			name: "single bash call",
			calls: []ToolUseBlock{{ID: "x", Name: "bash", Input: `{"command":"go test"}`}},
			want:  []string{"⚙ bash: go test"},
			nope:  []string{"Calling tools"},
		},
		{
			name: "file_read call",
			calls: []ToolUseBlock{{ID: "x", Name: "file_read", Input: `{"file_path":"/etc/hosts"}`}},
			want:  []string{"⚙ file_read: /etc/hosts"},
		},
		{
			name: "unknown input field",
			calls: []ToolUseBlock{{ID: "x", Name: "foo", Input: `{"bar":"baz"}`}},
			want:  []string{"⚙ foo"},
		},
		{
			name: "two calls separated",
			calls: []ToolUseBlock{
				{ID: "a", Name: "bash", Input: `{"command":"a"}`},
				{ID: "b", Name: "file_read", Input: `{"file_path":"/p"}`},
			},
			want: []string{"⚙ bash: a", "file_read: /p"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolCallStatus(tt.calls)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("expected %q in output, got %q", w, got)
				}
			}
			for _, n := range tt.nope {
				if strings.Contains(got, n) {
					t.Errorf("expected NOT %q in output, got %q", n, got)
				}
			}
		})
	}
}

func TestToolInputHint(t *testing.T) {
	if got := toolInputHint(`{"command":"echo hi"}`); got != "echo hi" {
		t.Errorf("expected 'echo hi', got %q", got)
	}
	if got := toolInputHint(`not json`); got != "" {
		t.Errorf("expected empty for invalid json, got %q", got)
	}
	if got := toolInputHint(``); got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}
