package tui

import "github.com/punkopunko/ironclaw/internal/channel"

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
