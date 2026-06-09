package tui

import (
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

// toolHistoryEntry records a completed tool execution for the stats panel.
type toolHistoryEntry struct {
	name       string
	succeeded  bool
	durationMs int64
}

// metricsState holds the latest runtime metrics for display.
type metricsState struct {
	iteration    int
	maxIter      int
	utilization  float64
	cacheCreate  int64
	cacheRead    int64
	inputTokens  int64
	outputTokens int64
	model        string
	provider     string
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

	// Metrics & tool tracking
	activeTool  string // currently executing tool name (empty when idle)
	lastTool    string // most recent completed tool
	lastToolOK  bool
	lastToolMs  int64
	toolHistory []toolHistoryEntry
	toolCount   int // total tools executed this session
	metrics     metricsState
	showStats   bool   // toggle for detailed stats panel

	// Compression tracking for stats panel
	compressionCount   int
	lastCompressFrom   float64 // before utilization (0.0–1.0)
	lastCompressTo     float64 // after utilization (0.0–1.0)
	lastCompressReason string

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
