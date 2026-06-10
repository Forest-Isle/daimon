package tui

import (
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	tea "github.com/charmbracelet/bubbletea"
)

// Custom tea.Msg types for communication between the adapter goroutines
// and the Bubble Tea main loop.

// agentResponseMsg is sent when the agent produces a complete (non-streaming) response.
type agentResponseMsg struct {
	text string
}

// streamUpdateMsg is sent periodically by the streamUpdater to refresh the view.
type streamUpdateMsg struct {
	id   string
	text string
}

// streamFinishMsg signals that streaming for a given message is complete.
type streamFinishMsg struct {
	id   string
	text string
}

// approvalRequestMsg asks the user to approve or deny a tool execution.
type approvalRequestMsg struct {
	toolName string
	input    string
	resultCh chan bool
}

// reflectionRequestMsg asks the user to decide on a replan action.
type reflectionRequestMsg struct {
	reason     string
	confidence float64
	resultCh   chan channel.ReplanDecision
}

// errorMsg reports an error to the UI.
type errorMsg struct {
	err error
}

// sessionResetMsg signals that the session was reset.
type sessionResetMsg struct{}

// notificationMsg displays a lightweight status notification in the output area.
type notificationMsg struct {
	text string
}

// feedbackRequestMsg asks the user to rate the last response (1.0 = good, -1.0 = bad).
type feedbackRequestMsg struct {
	resultCh chan float64
}

// setAutoApproveMsg signals that the user wants to enable auto-approve mode.
type setAutoApproveMsg struct{}

// setAgentModeMsg updates the TUI model's displayed agent mode.
type setAgentModeMsg struct{ mode string }

// cancelRequestMsg signals that the user wants to cancel the in-flight request.
type cancelRequestMsg struct{}

// tickMsg fires periodically to drive typing indicator animation.
type tickMsg struct{}

func typingTick() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}
