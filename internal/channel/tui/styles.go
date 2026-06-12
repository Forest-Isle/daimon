package tui

import "github.com/charmbracelet/lipgloss"

var (
	// ─── Palette ──────────────────────────────────────────────
	colorPrimary     = lipgloss.Color("#8B7CF6")
	colorPrimarySoft = lipgloss.Color("#B8B2FF")
	colorCyan        = lipgloss.Color("#4CC9F0")
	colorGreen       = lipgloss.Color("#35D39D")
	colorGold        = lipgloss.Color("#F2C14E")
	colorRed         = lipgloss.Color("#FF5C6C")
	colorText        = lipgloss.Color("#ECECF1")
	colorMuted       = lipgloss.Color("#9A9AA6")
	colorDim         = lipgloss.Color("#5B6070")
	colorPanelBorder = lipgloss.Color("#343A46")
	colorSurface     = lipgloss.Color("#151923")
	colorStatusBg    = lipgloss.Color("#10131A")

	// Header
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText).
			Background(colorSurface).
			Padding(0, 1)

	headerLabelStyle = lipgloss.NewStyle().
				Foreground(colorCyan)

	// Chat message labels
	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorGreen)

	agentLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimarySoft)

	systemStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(colorDim)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Width(5)

	// Message bar accents
	userBarStyle = lipgloss.NewStyle().
			Foreground(colorGreen)

	agentBarStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)

	systemBarStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Approval dialog
	approvalBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorGold).
				Padding(0, 1).
				MarginTop(1)

	approvalToolStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorGold)

	approvalHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	// Feedback dialog
	feedbackBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorGreen).
				Padding(0, 1).
				MarginTop(1)

	// Input area
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(0, 1)

	// Streaming indicator
	streamingStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Italic(true)

	// Typing indicator
	typingDotActiveStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	typingDotInactiveStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	// Suggestion box
	suggestionBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorCyan).
				Padding(0, 1).
				MarginBottom(1)

	suggestionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorCyan)

	suggestionMetaStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	suggestionStyle = lipgloss.NewStyle().
			Foreground(colorText)

	selectedSuggestionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorText).
				Background(lipgloss.Color("#2B3452"))

	suggestionHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	statusDimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	statusPhaseStyle = lipgloss.NewStyle().
				Foreground(colorPrimary)

	// Stats detail panel
	statsPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPanelBorder).
			Padding(0, 1)

	statsHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimarySoft)

	statsLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statsValueStyle = lipgloss.NewStyle().
			Foreground(colorText)

	// Welcome screen
	welcomeBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPanelBorder).
			Padding(1, 2).
			Align(lipgloss.Center)

	welcomeTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimarySoft).
				MarginBottom(1)

	welcomeSubtitleStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true).
				MarginBottom(1)

	welcomeHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	welcomeKeyStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	// ─── Status bar ───────────────────────────────────────────
	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(colorStatusBg)

	statusReadyStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Background(colorStatusBg).
				Bold(true)

	statusBusyStyle = lipgloss.NewStyle().
			Foreground(colorGold).
			Background(colorStatusBg).
			Bold(true)

	statusModelStyle = lipgloss.NewStyle().
				Foreground(colorPrimarySoft).
				Background(colorStatusBg)

	statusHintStyle = lipgloss.NewStyle().
			Foreground(colorGold).
			Background(colorStatusBg)
)
