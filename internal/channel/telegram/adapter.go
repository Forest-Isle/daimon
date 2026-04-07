package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Adapter implements channel.Channel, channel.ApprovalSender, and
// channel.ReflectionSender for Telegram.
type Adapter struct {
	bot            *tgbotapi.BotAPI
	allowedUserIDs map[int64]bool
	handler        channel.InboundHandler
	stopCh         chan struct{}

	// Approval tracking — moved from Gateway so the adapter fully owns the flow.
	pendingApprovals    sync.Map // key: toolName → chan bool
	pendingReflections  sync.Map // key: string → chan channel.ReplanDecision
	approvalTimeoutSecs int
}

func New(token string, allowedUserIDs []int64) (*Adapter, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}

	allowed := make(map[int64]bool, len(allowedUserIDs))
	for _, id := range allowedUserIDs {
		allowed[id] = true
	}

	slog.Info("telegram bot authorized", "username", bot.Self.UserName)
	return &Adapter{
		bot:                 bot,
		allowedUserIDs:      allowed,
		stopCh:              make(chan struct{}),
		approvalTimeoutSecs: 120,
	}, nil
}

// SetApprovalTimeout overrides the default approval timeout (120s).
func (a *Adapter) SetApprovalTimeout(seconds int) {
	if seconds > 0 {
		a.approvalTimeoutSecs = seconds
	}
}

func (a *Adapter) Name() string { return "telegram" }

func (a *Adapter) Start(ctx context.Context, handler channel.InboundHandler) error {
	a.handler = handler

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := a.bot.GetUpdatesChan(u)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.stopCh:
				return
			case update := <-updates:
				a.handleUpdate(ctx, update)
			}
		}
	}()

	slog.Info("telegram channel started")
	return nil
}

func (a *Adapter) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	// Handle callback queries (inline keyboard) — must be synchronous
	// so the approval/reflection channel receives the result before the update loop moves on.
	if update.CallbackQuery != nil {
		userID := update.CallbackQuery.From.ID
		if !a.allowedUserIDs[userID] {
			return
		}

		data := update.CallbackQuery.Data
		a.handleCallback(data)

		// Acknowledge callback
		callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
		_, _ = a.bot.Request(callback)
		return
	}

	if update.Message == nil {
		return
	}

	userID := update.Message.From.ID
	if !a.allowedUserIDs[userID] {
		slog.Warn("unauthorized user", "user_id", userID, "username", update.Message.From.UserName)
		return
	}

	chatID := update.Message.Chat.ID
	// Process message asynchronously so the update loop stays unblocked.
	// This is critical: if HandleMessage waits for tool approval, a synchronous
	// call would deadlock because the callback query could never be received.
	go a.handler(ctx, channel.InboundMessage{
		Channel:   "telegram",
		ChannelID: strconv.FormatInt(chatID, 10),
		UserID:    strconv.FormatInt(userID, 10),
		UserName:  update.Message.From.UserName,
		Text:      update.Message.Text,
	})
}

// handleCallback routes inline keyboard callback data to the appropriate
// pending approval or reflection channel.
func (a *Adapter) handleCallback(data string) {
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return
	}

	action, key := parts[0], parts[1]

	// Handle reflection replan decisions
	switch action {
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
		return
	}

	// Handle tool approval
	approved := action == "approve"
	if v, ok := a.pendingApprovals.Load(key); ok {
		ch := v.(chan bool)
		select {
		case ch <- approved:
		default:
		}
	}
}

func (a *Adapter) Send(ctx context.Context, msg channel.OutboundMessage) error {
	chatID, err := strconv.ParseInt(msg.ChannelID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat id: %w", err)
	}

	tgMsg := tgbotapi.NewMessage(chatID, msg.Text)
	if msg.ParseMode != "" {
		tgMsg.ParseMode = msg.ParseMode
	}
	if msg.ReplyMarkup != nil {
		tgMsg.ReplyMarkup = msg.ReplyMarkup
	}

	_, err = a.bot.Send(tgMsg)
	return err
}

func (a *Adapter) SendStreaming(ctx context.Context, target channel.MessageTarget) (channel.StreamUpdater, error) {
	chatID, err := strconv.ParseInt(target.ChannelID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chat id: %w", err)
	}

	// Send initial placeholder message
	msg := tgbotapi.NewMessage(chatID, "⏳ Thinking...")
	sent, err := a.bot.Send(msg)
	if err != nil {
		return nil, err
	}

	return &streamUpdater{
		bot:    a.bot,
		chatID: chatID,
		msgID:  sent.MessageID,
	}, nil
}

func (a *Adapter) Stop(_ context.Context) error {
	close(a.stopCh)
	a.bot.StopReceivingUpdates()
	slog.Info("telegram channel stopped")
	return nil
}

// ---------- channel.ApprovalSender ----------

// SendApprovalRequest sends a Telegram inline keyboard for tool approval and
// blocks until the user responds or the timeout expires.
func (a *Adapter) SendApprovalRequest(ctx context.Context, target channel.MessageTarget, toolName string, input string) (bool, error) {
	chatID, err := strconv.ParseInt(target.ChannelID, 10, 64)
	if err != nil || chatID == 0 {
		return true, nil // fallback: auto-approve
	}

	text := fmt.Sprintf("🔧 Tool: *%s*\n```\n%s\n```\nApprove execution?", toolName, FormatForTelegram(input))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Approve", "approve:"+toolName),
			tgbotapi.NewInlineKeyboardButtonData("❌ Deny", "deny:"+toolName),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	if _, err := a.bot.Send(msg); err != nil {
		return false, err
	}

	// Wait for callback
	resultCh := make(chan bool, 1)
	a.pendingApprovals.Store(toolName, resultCh)
	defer a.pendingApprovals.Delete(toolName)

	timeout := time.Duration(a.approvalTimeoutSecs) * time.Second
	select {
	case approved := <-resultCh:
		return approved, nil
	case <-time.After(timeout):
		slog.Info("telegram: approval timed out, defaulting to deny", "tool", toolName)
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// ---------- channel.ReflectionSender ----------

// SendReflectionRequest sends a Telegram inline keyboard for replan approval
// and blocks until the user responds or the timeout expires.
func (a *Adapter) SendReflectionRequest(ctx context.Context, target channel.MessageTarget, reason string, confidence float64) (channel.ReplanDecision, error) {
	chatID, err := strconv.ParseInt(target.ChannelID, 10, 64)
	if err != nil || chatID == 0 {
		return channel.ReplanContinue, nil
	}

	text := fmt.Sprintf(
		"🤔 *Low confidence plan* (%.0f%%)\nReason: %s\n\nHow should I proceed?",
		confidence*100, reason,
	)

	key := fmt.Sprintf("reflect_%s_%d", target.ChannelID, time.Now().UnixNano())

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ Continue", "reflect_continue:"+key),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Adjust", "reflect_adjust:"+key),
			tgbotapi.NewInlineKeyboardButtonData("🛑 Abort", "reflect_abort:"+key),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	if _, err := a.bot.Send(msg); err != nil {
		slog.Warn("telegram: failed to send reflection request", "err", err)
		return channel.ReplanContinue, nil
	}

	// Wait for callback
	resultCh := make(chan channel.ReplanDecision, 1)
	a.pendingReflections.Store(key, resultCh)
	defer a.pendingReflections.Delete(key)

	timeout := time.Duration(a.approvalTimeoutSecs) * time.Second
	select {
	case decision := <-resultCh:
		slog.Info("telegram: replan decision received", "decision", decision)
		return decision, nil
	case <-time.After(timeout):
		slog.Info("telegram: reflection timed out, defaulting to continue")
		return channel.ReplanContinue, nil
	case <-ctx.Done():
		return channel.ReplanContinue, ctx.Err()
	}
}

// ---------- channel.NotificationSender ----------

// SendNotification sends a silent notification message to the Telegram chat.
func (a *Adapter) SendNotification(_ context.Context, target channel.MessageTarget, text string) error {
	chatID, err := strconv.ParseInt(target.ChannelID, 10, 64)
	if err != nil || chatID == 0 {
		return nil
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableNotification = true
	_, err = a.bot.Send(msg)
	return err
}

// ---------- streamUpdater ----------

type streamUpdater struct {
	bot    *tgbotapi.BotAPI
	chatID int64
	msgID  int
	mu     sync.Mutex
	last   string
	lastAt time.Time
}

const minUpdateInterval = 1 * time.Second

func (s *streamUpdater) Update(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Rate-limit edits to avoid Telegram API throttling
	if time.Since(s.lastAt) < minUpdateInterval && s.last != "" {
		return nil
	}

	formatted := FormatForTelegram(text)
	if formatted == s.last {
		return nil
	}

	edit := tgbotapi.NewEditMessageText(s.chatID, s.msgID, formatted)
	_, err := s.bot.Send(edit)
	if err != nil {
		// Telegram returns error if message content hasn't changed
		return nil
	}

	s.last = formatted
	s.lastAt = time.Now()
	return nil
}

func (s *streamUpdater) Finish(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	formatted := FormatForTelegram(text)
	edit := tgbotapi.NewEditMessageText(s.chatID, s.msgID, formatted)
	_, err := s.bot.Send(edit)
	return err
}
