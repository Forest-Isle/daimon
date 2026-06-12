package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mode controls how key events are routed.
type mode int

// maxInputLines caps how tall the input box grows as wrapped text accumulates.
const maxInputLines = 6

const (
	modeChat     mode = iota // normal: keys go to textarea
	modeApproval             // y/n/a intercepted for tool approval
	modeFeedback             // y/n intercepted for feedback rating
)

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role      string // "user", "agent", "system"
	content   string
	timestamp time.Time

	// Render cache: the rendered body (glamour for agent, wrapped for
	// user/system) and the terminal width it was built at. renderedWidth==0
	// means not yet cached. Reused across frames so glamour runs once per
	// message instead of on every streaming tick.
	rendered      string
	renderedWidth int
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
	version       string

	// Approval state
	approvalTool  string
	approvalInput string
	approvalCh    chan bool

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
	modelItems        []ModelRoleEntry // populated via SetModelRoles
	modelSelectionIdx int              // -1 = no selection, initializes to 0

	// Typing indicator
	waitingForResponse bool
	typingTick         int // animation frame 0-2

	// Active tool activity (shown in status line while a tool runs).
	activeTool        string
	activeToolSummary string

	// Environment
	username string
	cwd      string

	// currentModel is the active LLM model name shown in the status bar.
	// Empty until set via SetCurrentModel.
	currentModel string

	// Mouse support — toggled at runtime so user can select/copy text
	mouseEnabled bool

	// Layout
	width  int
	height int
	ready  bool
}

// NewModel creates a new TUI model.
func NewModel(version, username, cwd string) Model {
	ta := textarea.New()
	ta.Placeholder = fmt.Sprintf("Message Daimon… (/help for commands)")
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(1) // grows up to maxInputLines as the user types
	ta.MaxHeight = maxInputLines
	ta.ShowLineNumbers = false
	ta.Prompt = userBarStyle.Render("❯")
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	return Model{
		textarea:           ta,
		messages:           make([]chatMessage, 0),
		version:            version,
		username:           username,
		cwd:                cwd,
		selectedSuggestion: -1,
		autoScroll:         true,
		mouseEnabled:       true,
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

// SetCurrentModel sets the active model name shown in the status bar.
func (m *Model) SetCurrentModel(name string) {
	m.currentModel = name
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
		if opus == "" {
			opus = use("Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL", "claude-opus-4-8")
		}
		sonnet := sonnetModel
		if sonnet == "" {
			sonnet = use("Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL", "claude-sonnet-4-6")
		}
		haiku := haikuModel
		if haiku == "" {
			haiku = use("Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "claude-haiku-4-5")
		}

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
