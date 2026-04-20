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
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	// Streaming indicator
	streamingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			Italic(true)

	// Suggestion box
	suggestionBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#7D56F4")).
				Padding(0, 1).
				MarginBottom(1)

	suggestionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#7D56F4"))

	suggestionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	selectedSuggestionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FAFAFA")).
				Background(lipgloss.Color("#7D56F4"))

	suggestionHintStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262")).
				Italic(true)

	// Status bar (below input)
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#929292")).
			Padding(0, 1)

	statusToolRunningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFD700"))

	statusToolOKStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#04B575"))

	statusToolFailStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF5555"))

	statusDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	statusPhaseStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7D56F4"))

	// Stats detail panel
	statsPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444444")).
			Padding(0, 1)

	statsHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#7D56F4"))

	statsLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#929292"))

	statsValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	statsBarFilledStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#04B575"))

	statsBarEmptyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#333333"))

	statsBarWarnStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFD700"))

	statsBarCritStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF5555"))
)
