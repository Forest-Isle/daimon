package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Header / status bar
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	// Chat messages
	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	agentLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4"))

	systemStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("#626262"))

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// Approval dialog
	approvalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#FF9900")).
				Padding(0, 1).
				MarginTop(1)

	approvalToolStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FF9900"))

	approvalHintStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262")).
				Italic(true)

	// Feedback dialog
	feedbackBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#04B575")).
				Padding(0, 1).
				MarginTop(1)

	// Reflection dialog
	reflectionBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#FFD700")).
				Padding(0, 1).
				MarginTop(1)

	// Input area
	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), true, false, false, false).
				BorderForeground(lipgloss.Color("#626262"))

	// Streaming indicator
	streamingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			Italic(true)
)
