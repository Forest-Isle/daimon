package tui

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	tea "github.com/charmbracelet/bubbletea"
)

// Adapter implements channel.Channel, channel.ApprovalSender,
// channel.ReflectionSender, channel.NotificationSender, and
// channel.FeedbackSender for an interactive terminal UI.
type Adapter struct {
	program     *tea.Program
	handler     channel.InboundHandler
	model       *Model
	stopCh      chan struct{}
	agentMode   string
	version     string
	autoApprove bool

	// approvalTimeout is the max time to wait for the user to respond
	// to an approval or reflection request.
	approvalTimeout time.Duration

	// userInputCh receives text messages typed by the user.
	// The Model writes here on Enter; the adapter's goroutine
	// converts them to InboundMessages for the gateway.
	userInputCh chan string
}

// New creates a new TUI adapter.
func New(agentMode, version string) *Adapter {
	return &Adapter{
		agentMode:       agentMode,
		version:         version,
		stopCh:          make(chan struct{}),
		userInputCh:     make(chan string, 16),
		approvalTimeout: 120 * time.Second,
	}
}

// SetApprovalTimeout overrides the default approval timeout.
func (a *Adapter) SetApprovalTimeout(d time.Duration) {
	if d > 0 {
		a.approvalTimeout = d
	}
}

// SetAutoApprove disables interactive approval prompts.
func (a *Adapter) SetAutoApprove(v bool) {
	a.autoApprove = v
}

func (a *Adapter) Name() string { return "tui" }

func (a *Adapter) Start(ctx context.Context, handler channel.InboundHandler) error {
	a.handler = handler

	m := NewModel(a.agentMode, a.version)
	a.model = &m

	// Create a custom model wrapper that captures user input
	wrapper := &modelWrapper{
		Model:       &m,
		userInputCh: a.userInputCh,
	}

	a.program = tea.NewProgram(
		wrapper,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Route user input to the gateway in a background goroutine
	go a.routeInput(ctx)

	return nil
}

// Run starts the Bubble Tea event loop. This blocks until the user quits.
// Call after Start().
func (a *Adapter) Run() error {
	if a.program == nil {
		return fmt.Errorf("tui: Start() must be called before Run()")
	}
	_, err := a.program.Run()
	return err
}

// routeInput reads user text from userInputCh and dispatches to the gateway handler.
func (a *Adapter) routeInput(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case text := <-a.userInputCh:
			if a.handler != nil {
				go a.handler(ctx, channel.InboundMessage{
					Channel:   "tui",
					ChannelID: "tui_local",
					UserID:    "local",
					UserName:  "local",
					Text:      text,
				})
			}
		}
	}
}

func (a *Adapter) Send(_ context.Context, msg channel.OutboundMessage) error {
	if a.program == nil {
		return nil
	}
	a.program.Send(agentResponseMsg{text: msg.Text})
	return nil
}

func (a *Adapter) SendStreaming(_ context.Context, target channel.MessageTarget) (channel.StreamUpdater, error) {
	if a.program == nil {
		return nil, fmt.Errorf("tui: program not started")
	}

	id := fmt.Sprintf("stream_%d", time.Now().UnixNano())

	su := &tuiStreamUpdater{
		program: a.program,
		id:      id,
		done:    make(chan struct{}),
	}

	// Background goroutine pushes throttled updates to the Bubble Tea loop
	go su.pump()

	return su, nil
}

func (a *Adapter) Stop(_ context.Context) error {
	close(a.stopCh)
	if a.program != nil {
		a.program.Quit()
	}
	slog.Info("tui channel stopped")
	return nil
}

// ---------- channel.ApprovalSender ----------

func (a *Adapter) SendApprovalRequest(ctx context.Context, target channel.MessageTarget, toolName string, input string) (bool, error) {
	if a.autoApprove {
		return true, nil
	}
	if a.program == nil {
		return true, nil
	}

	resultCh := make(chan bool, 1)
	a.program.Send(approvalRequestMsg{
		toolName: toolName,
		input:    input,
		resultCh: resultCh,
	})

	select {
	case approved := <-resultCh:
		return approved, nil
	case <-time.After(a.approvalTimeout):
		slog.Info("tui: approval timed out, defaulting to deny", "tool", toolName)
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// ---------- channel.ReflectionSender ----------

func (a *Adapter) SendReflectionRequest(ctx context.Context, target channel.MessageTarget, reason string, confidence float64) (channel.ReplanDecision, error) {
	if a.program == nil {
		return channel.ReplanContinue, nil
	}

	resultCh := make(chan channel.ReplanDecision, 1)
	a.program.Send(reflectionRequestMsg{
		reason:     reason,
		confidence: confidence,
		resultCh:   resultCh,
	})

	select {
	case decision := <-resultCh:
		return decision, nil
	case <-time.After(a.approvalTimeout):
		slog.Info("tui: reflection timed out, defaulting to continue")
		return channel.ReplanContinue, nil
	case <-ctx.Done():
		return channel.ReplanContinue, ctx.Err()
	}
}

// ---------- channel.FeedbackSender ----------

// SendFeedbackRequest displays a "Was this helpful? (y/n)" dialog in the TUI
// and blocks until the user responds, the approvalTimeout expires, or ctx is
// cancelled.  Returns 1.0 (helpful), -1.0 (not helpful), or 0.0 on timeout.
func (a *Adapter) SendFeedbackRequest(ctx context.Context, target channel.MessageTarget) (float64, error) {
	if a.program == nil {
		return 0, nil
	}

	resultCh := make(chan float64, 1)
	a.program.Send(feedbackRequestMsg{
		resultCh: resultCh,
	})

	select {
	case feedback := <-resultCh:
		return feedback, nil
	case <-time.After(a.approvalTimeout):
		slog.Info("tui: feedback timed out, defaulting to neutral")
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// ---------- channel.NotificationSender ----------

// SendNotification displays a dim status notification in the TUI output area.
func (a *Adapter) SendNotification(_ context.Context, target channel.MessageTarget, text string) error {
	if a.program == nil {
		return nil
	}
	a.program.Send(notificationMsg{text: text})
	return nil
}

// ---------- modelWrapper ----------

// modelWrapper wraps Model and captures user input on Enter.
type modelWrapper struct {
	*Model
	userInputCh chan string
}

func (w *modelWrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Intercept Enter in chat mode to capture user input text
	if keyMsg, ok := msg.(tea.KeyMsg); ok && w.mode == modeChat && keyMsg.Type == tea.KeyEnter {
		text := w.textarea.Value()
		if text != "" {
			w.userInputCh <- text
		}
	}

	m, cmd := w.Model.Update(msg)
	if model, ok := m.(*Model); ok {
		w.Model = model
	} else if model, ok := m.(Model); ok {
		w.Model = &model
	}
	return w, cmd
}

func (w *modelWrapper) View() string {
	return w.Model.View()
}

func (w *modelWrapper) Init() tea.Cmd {
	return w.Model.Init()
}

// ---------- tuiStreamUpdater ----------

type tuiStreamUpdater struct {
	program *tea.Program
	id      string
	latest  atomic.Value // string
	done    chan struct{}
}

const streamThrottle = 50 * time.Millisecond

func (s *tuiStreamUpdater) Update(text string) error {
	s.latest.Store(text)
	return nil
}

func (s *tuiStreamUpdater) Finish(text string) error {
	close(s.done)
	s.program.Send(streamFinishMsg{id: s.id, text: text})
	return nil
}

// pump periodically sends the latest text to the Bubble Tea loop.
func (s *tuiStreamUpdater) pump() {
	ticker := time.NewTicker(streamThrottle)
	defer ticker.Stop()

	var lastSent string
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			v := s.latest.Load()
			if v == nil {
				continue
			}
			text := v.(string)
			if text != lastSent {
				s.program.Send(streamUpdateMsg{id: s.id, text: text})
				lastSent = text
			}
		}
	}
}
