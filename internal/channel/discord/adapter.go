package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/Forest-Isle/IronClaw/internal/channel"
)

// Adapter implements channel.Channel, channel.ApprovalSender,
// channel.ReflectionSender, channel.FeedbackSender, and
// channel.NotificationSender for Discord.
type Adapter struct {
	ctx            context.Context
	token          string
	allowedUserIDs map[string]bool
	session        *discordgo.Session
	handler        channel.InboundHandler
	stopCh         chan struct{}

	// Approval tracking
	pendingApprovals   sync.Map // key: string → chan bool
	pendingReflections sync.Map // key: string → chan channel.ReplanDecision
	pendingFeedbacks   sync.Map // key: string → chan float64
	approvalTimeoutSec int

	// autoApprove, when true, skips interactive approval for all subsequent requests.
	autoApprove bool
}

// Config holds the configuration for the Discord adapter.
type Config struct {
	Token          string
	AllowedUserIDs []string
}

// New creates a new Discord adapter. Unlike Telegram, we defer session creation
// to Start() because discordgo.New doesn't validate the token until Open().
func New(cfg Config) *Adapter {
	allowed := make(map[string]bool, len(cfg.AllowedUserIDs))
	for _, id := range cfg.AllowedUserIDs {
		allowed[id] = true
	}
	return &Adapter{
		token:              cfg.Token,
		allowedUserIDs:     allowed,
		stopCh:             make(chan struct{}),
		approvalTimeoutSec: 120,
	}
}

// SetApprovalTimeout overrides the default approval timeout (120s).
func (a *Adapter) SetApprovalTimeout(seconds int) {
	if seconds > 0 {
		a.approvalTimeoutSec = seconds
	}
}

func (a *Adapter) Name() string { return "discord" }

func (a *Adapter) Start(ctx context.Context, handler channel.InboundHandler) error {
	a.ctx = ctx
	a.handler = handler

	sess, err := discordgo.New("Bot " + a.token)
	if err != nil {
		return fmt.Errorf("discord session: %w", err)
	}
	a.session = sess

	sess.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	sess.AddHandler(a.onMessageCreate)
	sess.AddHandler(a.onInteractionCreate)

	if err := sess.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}

	slog.Info("discord bot connected", "user", sess.State.User.Username)

	// Close session on context cancellation
	go func() {
		select {
		case <-ctx.Done():
		case <-a.stopCh:
		}
		_ = sess.Close()
	}()

	return nil
}

func (a *Adapter) Stop(_ context.Context) error {
	select {
	case <-a.stopCh:
		// Already closed
	default:
		close(a.stopCh)
	}
	slog.Info("discord channel stopped")
	return nil
}

func (a *Adapter) Send(_ context.Context, msg channel.OutboundMessage) error {
	text := FormatForDiscord(msg.Text)

	_, err := a.session.ChannelMessageSend(msg.ChannelID, text)
	return err
}

func (a *Adapter) SendStreaming(_ context.Context, target channel.MessageTarget) (channel.StreamUpdater, error) {
	msg, err := a.session.ChannelMessageSend(target.ChannelID, "Thinking...")
	if err != nil {
		return nil, err
	}
	return &streamUpdater{
		session:   a.session,
		channelID: target.ChannelID,
		messageID: msg.ID,
	}, nil
}

// ---------- Event Handlers ----------

// onMessageCreate handles incoming Discord messages.
func (a *Adapter) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Check user authorization
	if len(a.allowedUserIDs) > 0 && !a.allowedUserIDs[m.Author.ID] {
		slog.Warn("discord: unauthorized user", "user_id", m.Author.ID, "username", m.Author.Username)
		return
	}

	if a.handler == nil {
		return
	}

	// Process asynchronously so the event loop stays unblocked.
	// This is critical: if the handler waits for tool approval, a synchronous
	// call would deadlock because the interaction callback could never be received.
	go a.handler(a.ctx, channel.InboundMessage{
		Channel:   "discord",
		ChannelID: m.ChannelID,
		UserID:    m.Author.ID,
		UserName:  m.Author.Username,
		Text:      m.Content,
	})
}

// onInteractionCreate handles button interactions (approvals, reflections, feedback).
func (a *Adapter) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	customID := i.MessageComponentData().CustomID
	parts := strings.SplitN(customID, ":", 2)
	if len(parts) != 2 {
		return
	}

	action, key := parts[0], parts[1]
	var responseText string

	switch action {
	// Feedback
	case "feedback_yes", "feedback_no":
		var score float64
		if action == "feedback_yes" {
			score = 1.0
		} else {
			score = -1.0
		}
		if v, ok := a.pendingFeedbacks.Load(key); ok {
			ch := v.(chan float64)
			select {
			case ch <- score:
			default:
			}
		}
		responseText = "Feedback recorded."

	// Reflection
	case "reflect_continue", "reflect_adjust", "reflect_abort":
		var decision channel.ReplanDecision
		switch action {
		case "reflect_continue":
			decision = channel.ReplanContinue
		case "reflect_adjust":
			decision = channel.ReplanAdjust
		case "reflect_abort":
			decision = channel.ReplanAbort
		}
		if v, ok := a.pendingReflections.Load(key); ok {
			ch := v.(chan channel.ReplanDecision)
			select {
			case ch <- decision:
			default:
			}
		}
		responseText = "Decision recorded."

	// Always approve
	case "always_approve":
		a.autoApprove = true
		if v, ok := a.pendingApprovals.Load(key); ok {
			ch := v.(chan bool)
			select {
			case ch <- true:
			default:
			}
		}
		responseText = "Auto-approve enabled."

	// Tool approval
	case "approve":
		if v, ok := a.pendingApprovals.Load(key); ok {
			ch := v.(chan bool)
			select {
			case ch <- true:
			default:
			}
		}
		responseText = "Approved."

	case "deny":
		if v, ok := a.pendingApprovals.Load(key); ok {
			ch := v.(chan bool)
			select {
			case ch <- false:
			default:
			}
		}
		responseText = "Denied."

	default:
		return
	}

	// Acknowledge the interaction with an ephemeral message
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: responseText,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// ---------- channel.ApprovalSender ----------

// SendApprovalRequest sends a Discord message with approve/deny buttons and
// blocks until the user responds or the timeout expires.
func (a *Adapter) SendApprovalRequest(ctx context.Context, target channel.MessageTarget, toolName string, input string) (bool, error) {
	if a.autoApprove {
		return true, nil
	}

	text := fmt.Sprintf("**Tool Approval Required**\n`%s`\n```\n%s\n```\nApprove execution?",
		toolName, FormatForDiscord(input))

	key := fmt.Sprintf("%s_%s_%d", target.ChannelID, toolName, time.Now().UnixNano())

	resultCh := make(chan bool, 1)
	a.pendingApprovals.Store(key, resultCh)
	defer a.pendingApprovals.Delete(key)

	_, err := a.session.ChannelMessageSendComplex(target.ChannelID, &discordgo.MessageSend{
		Content: text,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Approve",
						Style:    discordgo.SuccessButton,
						CustomID: "approve:" + key,
					},
					discordgo.Button{
						Label:    "Deny",
						Style:    discordgo.DangerButton,
						CustomID: "deny:" + key,
					},
					discordgo.Button{
						Label:    "Always Approve",
						Style:    discordgo.SecondaryButton,
						CustomID: "always_approve:" + key,
					},
				},
			},
		},
	})
	if err != nil {
		return false, err
	}

	timeout := time.Duration(a.approvalTimeoutSec) * time.Second
	select {
	case approved := <-resultCh:
		return approved, nil
	case <-time.After(timeout):
		slog.Info("discord: approval timed out, defaulting to deny", "tool", toolName)
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// ---------- channel.ReflectionSender ----------

// SendReflectionRequest sends a Discord message with continue/adjust/abort buttons
// and blocks until the user responds or the timeout expires.
func (a *Adapter) SendReflectionRequest(ctx context.Context, target channel.MessageTarget, reason string, confidence float64) (channel.ReplanDecision, error) {
	text := fmt.Sprintf(
		"**Low confidence plan** (%.0f%%)\nReason: %s\n\nHow should I proceed?",
		confidence*100, reason,
	)

	key := fmt.Sprintf("reflect_%s_%d", target.ChannelID, time.Now().UnixNano())

	resultCh := make(chan channel.ReplanDecision, 1)
	a.pendingReflections.Store(key, resultCh)
	defer a.pendingReflections.Delete(key)

	_, err := a.session.ChannelMessageSendComplex(target.ChannelID, &discordgo.MessageSend{
		Content: text,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Continue",
						Style:    discordgo.PrimaryButton,
						CustomID: "reflect_continue:" + key,
					},
					discordgo.Button{
						Label:    "Adjust",
						Style:    discordgo.SecondaryButton,
						CustomID: "reflect_adjust:" + key,
					},
					discordgo.Button{
						Label:    "Abort",
						Style:    discordgo.DangerButton,
						CustomID: "reflect_abort:" + key,
					},
				},
			},
		},
	})
	if err != nil {
		slog.Warn("discord: failed to send reflection request", "err", err)
		return channel.ReplanContinue, nil
	}

	timeout := time.Duration(a.approvalTimeoutSec) * time.Second
	select {
	case decision := <-resultCh:
		slog.Info("discord: replan decision received", "decision", decision)
		return decision, nil
	case <-time.After(timeout):
		slog.Info("discord: reflection timed out, defaulting to continue")
		return channel.ReplanContinue, nil
	case <-ctx.Done():
		return channel.ReplanContinue, ctx.Err()
	}
}

// ---------- channel.FeedbackSender ----------

// SendFeedbackRequest sends a Discord message with thumbs up/down buttons
// and blocks until the user responds or the timeout expires.
func (a *Adapter) SendFeedbackRequest(ctx context.Context, target channel.MessageTarget) (float64, error) {
	key := fmt.Sprintf("feedback_%s_%d", target.ChannelID, time.Now().UnixNano())

	resultCh := make(chan float64, 1)
	a.pendingFeedbacks.Store(key, resultCh)
	defer a.pendingFeedbacks.Delete(key)

	_, err := a.session.ChannelMessageSendComplex(target.ChannelID, &discordgo.MessageSend{
		Content: "Was this helpful?",
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Emoji: &discordgo.ComponentEmoji{Name: "\U0001f44d"},
						Style: discordgo.SecondaryButton,
						CustomID: "feedback_yes:" + key,
					},
					discordgo.Button{
						Emoji: &discordgo.ComponentEmoji{Name: "\U0001f44e"},
						Style: discordgo.SecondaryButton,
						CustomID: "feedback_no:" + key,
					},
				},
			},
		},
	})
	if err != nil {
		slog.Warn("discord: failed to send feedback request", "err", err)
		return 0, nil
	}

	timeout := time.Duration(a.approvalTimeoutSec) * time.Second
	select {
	case score := <-resultCh:
		slog.Info("discord: feedback received", "score", score)
		return score, nil
	case <-time.After(timeout):
		slog.Info("discord: feedback timed out, defaulting to neutral")
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// ---------- channel.NotificationSender ----------

// SendNotification sends a plain text notification to the Discord channel.
func (a *Adapter) SendNotification(_ context.Context, target channel.MessageTarget, text string) error {
	_, err := a.session.ChannelMessageSend(target.ChannelID, FormatForDiscord(text))
	return err
}

// ---------- streamUpdater ----------

type streamUpdater struct {
	session   *discordgo.Session
	channelID string
	messageID string
	mu        sync.Mutex
	last      string
	lastAt    time.Time
}

const minUpdateInterval = 1 * time.Second

func (u *streamUpdater) Update(text string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Rate-limit edits to avoid Discord API throttling
	if time.Since(u.lastAt) < minUpdateInterval && u.last != "" {
		return nil
	}

	formatted := FormatForDiscord(text)
	if formatted == u.last {
		return nil
	}

	_, err := u.session.ChannelMessageEdit(u.channelID, u.messageID, formatted)
	if err != nil {
		// Discord returns error if message content hasn't changed
		return nil
	}

	u.last = formatted
	u.lastAt = time.Now()
	return nil
}

func (u *streamUpdater) Finish(text string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	formatted := FormatForDiscord(text)
	_, err := u.session.ChannelMessageEdit(u.channelID, u.messageID, formatted)
	return err
}
