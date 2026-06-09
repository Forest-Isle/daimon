package tui

import "github.com/charmbracelet/lipgloss"

var (
	// ─── Palette ──────────────────────────────────────────────
	colorPurple   = lipgloss.Color("#8B6CE0")
	colorGreen    = lipgloss.Color("#2EC49C")
	colorGold     = lipgloss.Color("#F0B828")
	colorRed      = lipgloss.Color("#E84D5B")
	colorGray     = lipgloss.Color("#787882")
	colorDimGray  = lipgloss.Color("#4A4A52")
	colorWhite    = lipgloss.Color("#E8E8EC")
	colorBgPurple = lipgloss.Color("#1E1B2E")
	colorBgGreen  = lipgloss.Color("#1A2A24")

	// Header
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(colorPurple).
			Padding(0, 1)

	headerLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C4B5F5"))

	// Chat message labels
	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorGreen)

	agentLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPurple)

	systemStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(colorDimGray)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDimGray).
			Width(5)

	// Message bar accents
	userBarStyle = lipgloss.NewStyle().
			Foreground(colorGreen)

	agentBarStyle = lipgloss.NewStyle().
			Foreground(colorPurple)

	systemBarStyle = lipgloss.NewStyle().
			Foreground(colorDimGray)

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
			BorderForeground(colorPurple).
			Padding(0, 1)

	// Streaming indicator
	streamingStyle = lipgloss.NewStyle().
			Foreground(colorPurple).
			Italic(true)

	// Typing indicator
	typingDotActiveStyle = lipgloss.NewStyle().
				Foreground(colorPurple).
				Bold(true)

	typingDotInactiveStyle = lipgloss.NewStyle().
				Foreground(colorDimGray)

	// Suggestion box
	suggestionBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPurple).
				Padding(0, 1).
				MarginBottom(1)

	suggestionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPurple)

	suggestionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	selectedSuggestionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FAFAFA")).
				Background(colorPurple)

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
			Foreground(colorPurple)

	// Stats detail panel
	statsPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444444")).
			Padding(0, 1)

	statsHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPurple)

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

	// Welcome screen
	welcomeBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPurple).
			Padding(1, 2).
			Align(lipgloss.Center)

	welcomeTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPurple).
			MarginBottom(1)

	welcomeSubtitleStyle = lipgloss.NewStyle().
			Foreground(colorDimGray).
			Italic(true).
			MarginBottom(1)

	welcomeHintStyle = lipgloss.NewStyle().
			Foreground(colorGray)

	welcomeKeyStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)
)
