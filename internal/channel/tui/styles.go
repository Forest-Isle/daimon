package tui

import "github.com/charmbracelet/lipgloss"

var (
	// ─── Palette ──────────────────────────────────────────────
	// Calm, low-noise dark theme: one dominant accent (soft blue) anchors
	// the agent, a single secondary (mint) marks the user, everything else
	// stays neutral so long sessions are easy on the eyes.
	colorBrand    = lipgloss.Color("#82AAFF") // agent glyph, brand, primary accent
	colorBrandDim = lipgloss.Color("#A9C2FF") // lighter brand for headers/labels
	colorUser     = lipgloss.Color("#7DCFB6") // user glyph + input prompt
	colorText     = lipgloss.Color("#C8CCD4") // primary text
	colorMuted    = lipgloss.Color("#828A99") // secondary text
	colorDim      = lipgloss.Color("#4B515E") // tertiary / dim / separators
	colorGreen    = lipgloss.Color("#9ECE6A") // ready / success
	colorGold     = lipgloss.Color("#E0AF68") // busy / approval / warn
	colorBorder   = lipgloss.Color("#2A2F3A") // panel borders
	colorSurface  = lipgloss.Color("#161A22") // header background
	colorStatusBg = lipgloss.Color("#0E1117") // status bar background
	colorSelBg    = lipgloss.Color("#243049") // selected list row background

	// Header
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText).
			Background(colorSurface).
			Padding(0, 1)

	headerLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	// Chat — role glyphs lead each turn; bodies use neutral text.
	userGlyphStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorUser)

	agentGlyphStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBrand)

	systemStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(colorDim)

	// Input prompt glyph (textarea).
	userBarStyle = lipgloss.NewStyle().
			Foreground(colorUser)

	// Streaming cursor.
	streamCursorStyle = lipgloss.NewStyle().
				Foreground(colorBrand)

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
			BorderForeground(colorBrand).
			Padding(0, 1)

	// Typing indicator
	typingDotActiveStyle = lipgloss.NewStyle().
				Foreground(colorBrand).
				Bold(true)

	typingDotInactiveStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	// Suggestion box
	suggestionBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBrand).
				Padding(0, 1).
				MarginBottom(1)

	suggestionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBrandDim)

	suggestionMetaStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	suggestionStyle = lipgloss.NewStyle().
			Foreground(colorText)

	selectedSuggestionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorText).
				Background(colorSelBg)

	suggestionHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	statusDimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Workflow steps — tool calls shown inline under a round.
	stepGuideStyle  = lipgloss.NewStyle().Foreground(colorDim)
	stepGlyphStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	stepArgStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	stepMetaStyle   = lipgloss.NewStyle().Foreground(colorDim)
	stepRunStyle    = lipgloss.NewStyle().Foreground(colorGold)
	stepOkStyle     = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	stepErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E")).Bold(true)
	stepOutputStyle = lipgloss.NewStyle().Foreground(colorDim)

	// Stats detail panel
	statsPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	statsHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBrandDim)

	statsLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statsValueStyle = lipgloss.NewStyle().
			Foreground(colorText)

	// Welcome screen
	welcomeTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBrandDim)

	welcomeSubtitleStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	welcomeHintStyle = lipgloss.NewStyle().
				Foreground(colorMuted)

	welcomeKeyStyle = lipgloss.NewStyle().
			Foreground(colorBrand).
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
				Foreground(colorBrandDim).
				Background(colorStatusBg)

	statusHintStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(colorStatusBg)
)
