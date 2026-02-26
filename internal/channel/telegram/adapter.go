package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/channel"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Adapter implements channel.Channel for Telegram.
type Adapter struct {
	bot            *tgbotapi.BotAPI
	allowedUserIDs map[int64]bool
	handler        channel.InboundHandler
	stopCh         chan struct{}
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
	return &Adapter{bot: bot, allowedUserIDs: allowed, stopCh: make(chan struct{})}, nil
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
	// so the approval channel receives the result before the update loop moves on.
	if update.CallbackQuery != nil {
		userID := update.CallbackQuery.From.ID
		if !a.allowedUserIDs[userID] {
			return
		}
		chatID := update.CallbackQuery.Message.Chat.ID
		a.handler(ctx, channel.InboundMessage{
			Channel:      "telegram",
			ChannelID:    strconv.FormatInt(chatID, 10),
			UserID:       strconv.FormatInt(userID, 10),
			UserName:     update.CallbackQuery.From.UserName,
			CallbackData: update.CallbackQuery.Data,
		})

		// Acknowledge callback
		callback := tgbotapi.NewCallback(update.CallbackQuery.ID, "")
		a.bot.Request(callback)
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

// SendApprovalRequest sends an inline keyboard for tool approval.
func (a *Adapter) SendApprovalRequest(chatID int64, toolName, input string) (int, error) {
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

	sent, err := a.bot.Send(msg)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

// streamUpdater implements channel.StreamUpdater by editing a Telegram message.
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
