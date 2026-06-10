package tui

import (
	"os"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mode controls how key events are routed.
type mode int

const (
	modeChat       mode = iota // normal: keys go to textarea
	modeApproval               // y/n/a intercepted for tool approval
	modeFeedback               // y/n intercepted for feedback rating
	modeReflection             // 1/2/3 intercepted for replan decision
)

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role      string // "user", "agent", "system"
	content   string
	timestamp time.Time
}

// Model is the Bubble Tea model for the TUI channel.
type Model struct {
	// UI components
	viewport viewport.Model
	textarea textarea.Model

	// State
	mode          mode
	messages      []chatMessage
	streamingID   string // non-empty while streaming
	streamingText string
	agentMode     string // "simple" or "cognitive"
	version       string

	// Approval state
	approvalTool  string
	approvalInput string
	approvalCh    chan bool

	// Reflection state
	reflectReason     string
	reflectConfidence float64
	reflectCh         chan channel.ReplanDecision

	// Feedback state
	feedbackCh chan float64

	// Suggestion state
	suggestions        []SuggestionItem
	selectedSuggestion int // -1 means no selection
	showingSuggestions bool
	argCompleter       ArgCompleter // optional; injected by the adapter

	// Scroll state
	autoScroll bool // true = follow new content; false = user is reading history

	// Input history (↑/↓ navigation)
	inputHistory []string
	historyIdx   int    // current position; len(inputHistory) = "new input"
	historySaved string // stash current input when entering history

	// Panel toggles
	showHelpPanel  bool // toggle for help/commands panel
	showModelPanel bool // toggle for model info/selection panel

	// Model selection state
	modelItems       []ModelRoleEntry // populated via SetModelRoles
	modelSelectionIdx int        // -1 = no selection, initializes to 0

	// Typing indicator
	waitingForResponse bool
	typingTick         int // animation frame 0-2

	// Environment
	username string
	cwd      string

	// Layout
	width  int
	height int
	ready  bool
}

// NewModel creates a new TUI model.
func NewModel(agentMode, version, username, cwd string) Model {
	ta := textarea.New()
	ta.Placeholder = fmt.Sprintf("Message IronClaw… (/help for commands)")
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(1) // single-line chat input
	ta.ShowLineNumbers = false
	ta.Prompt = userBarStyle.Render("❯")
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	return Model{
		textarea:           ta,
		messages:           make([]chatMessage, 0),
		agentMode:          agentMode,
		version:            version,
		username:           username,
		cwd:                cwd,
		selectedSuggestion: -1,
		autoScroll:         true,
	}
}

func (m Model) Init() tea.Cmd {
	return textarea.Blink
}

// ModelRoleEntry is a single model entry for the /model selection panel.
type ModelRoleEntry struct {
	Role string
	Name string
}

// SetModelRoles populates the model selection list from config values.
// opus/sonnet/haiku come from llm.models in config; empty = use defaults.
// Priority: config > ANTHROPIC_DEFAULT_* env > official default.
func (m *Model) SetModelRoles(provider, opusModel, sonnetModel, haikuModel string) {
	m.modelItems = nil

	use := func(role, envKey, officialDefault string) string {
		if v := os.Getenv(envKey); v != "" {
			return v
		}
		return officialDefault
	}

	if provider == "claude" || provider == "" {
		opus := opusModel
		if opus == "" { opus = use("Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL", "claude-opus-4-8") }
		sonnet := sonnetModel
		if sonnet == "" { sonnet = use("Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL", "claude-sonnet-4-6") }
		haiku := haikuModel
		if haiku == "" { haiku = use("Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "claude-haiku-4-5") }

		m.modelItems = append(m.modelItems,
			ModelRoleEntry{Role: "Opus", Name: opus},
			ModelRoleEntry{Role: "Sonnet", Name: sonnet},
			ModelRoleEntry{Role: "Haiku", Name: haiku},
		)
	}
	if provider == "openai" || provider == "openai-compatible" || provider == "" {
		m.modelItems = append(m.modelItems,
			ModelRoleEntry{Role: "GPT-5", Name: use("GPT-5", "OPENAI_DEFAULT_GPT5_MODEL", "gpt-5.4")},
			ModelRoleEntry{Role: "GPT-5 Mini", Name: use("GPT-5 Mini", "OPENAI_DEFAULT_GPT5_MINI_MODEL", "gpt-5.4-mini")},
			ModelRoleEntry{Role: "GPT-4.1", Name: use("GPT-4.1", "OPENAI_DEFAULT_GPT4_MODEL", "gpt-4.1")},
			ModelRoleEntry{Role: "Reasoning", Name: use("Reasoning", "OPENAI_DEFAULT_REASONING_MODEL", "o4-mini")},
		)
	}
}
