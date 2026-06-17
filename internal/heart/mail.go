package heart

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type MailMessage struct {
	MessageID string
	From      string
	Subject   string
	Date      string
}

type MailFetcher interface {
	FetchUnseen(ctx context.Context) ([]MailMessage, error)
	Close() error
}

type MailSource struct {
	Dial         func(ctx context.Context) (MailFetcher, error)
	PollInterval time.Duration
}

func (m *MailSource) Name() string {
	return "mail"
}

func (m *MailSource) Run(ctx context.Context, emit func(Event) error) error {
	poll := m.PollInterval
	if poll <= 0 {
		poll = 60 * time.Second
	}
	if m.Dial == nil {
		<-ctx.Done()
		return ctx.Err()
	}

	if err := m.pollOnce(ctx, emit); err != nil {
		slog.Warn("mail: poll failed", "err", err)
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := m.pollOnce(ctx, emit); err != nil {
				slog.Warn("mail: poll failed", "err", err)
			}
		}
	}
}

func (m *MailSource) pollOnce(ctx context.Context, emit func(Event) error) error {
	f, err := m.Dial(ctx)
	if err != nil {
		return err
	}
	defer f.Close()

	msgs, err := f.FetchUnseen(ctx)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		payload, err := json.Marshal(struct {
			From      string `json:"from"`
			Subject   string `json:"subject"`
			MessageID string `json:"message_id"`
			Date      string `json:"date"`
		}{
			From:      msg.From,
			Subject:   msg.Subject,
			MessageID: msg.MessageID,
			Date:      msg.Date,
		})
		if err != nil {
			slog.Debug("mail: marshal event failed", "message_id", msg.MessageID, "err", err)
			continue
		}
		dedupKey := msg.MessageID
		if dedupKey == "" {
			dedupKey = fmt.Sprintf("%s|%s|%s", msg.From, msg.Subject, msg.Date)
		}
		if err := emit(Event{Kind: "mail.received", Payload: string(payload), DedupKey: dedupKey}); err != nil {
			slog.Warn("mail: emit event failed", "message_id", msg.MessageID, "err", err)
		}
	}
	return nil
}

func IMAPDialer(host string, port int, username, password, mailbox string) func(ctx context.Context) (MailFetcher, error) {
	return func(ctx context.Context) (MailFetcher, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		c, err := imapclient.DialTLS(net.JoinHostPort(host, strconv.Itoa(port)), nil)
		if err != nil {
			return nil, fmt.Errorf("mail: dial imap: %w", err)
		}
		if err := c.Login(username, password).Wait(); err != nil {
			c.Close()
			return nil, fmt.Errorf("mail: login imap: %w", err)
		}
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			c.Close()
			return nil, fmt.Errorf("mail: select mailbox: %w", err)
		}
		return &imapFetcher{client: c, mailbox: mailbox}, nil
	}
}

type imapFetcher struct {
	client  *imapclient.Client
	mailbox string
}

func (f *imapFetcher) FetchUnseen(ctx context.Context) ([]MailMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	data, err := f.client.Search(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("mail: search unseen: %w", err)
	}
	seqNums := data.AllSeqNums()
	if len(seqNums) == 0 {
		return nil, nil
	}

	buffers, err := f.client.Fetch(imap.SeqSetNum(seqNums...), &imap.FetchOptions{Envelope: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("mail: fetch envelopes: %w", err)
	}

	msgs := make([]MailMessage, 0, len(buffers))
	for _, buf := range buffers {
		if buf.Envelope == nil {
			continue
		}
		msgs = append(msgs, mailMessageFromEnvelope(buf.Envelope))
	}
	return msgs, nil
}

func (f *imapFetcher) Close() error {
	_ = f.client.Logout().Wait()
	return f.client.Close()
}

func mailMessageFromEnvelope(env *imap.Envelope) MailMessage {
	from := ""
	if len(env.From) > 0 {
		from = env.From[0].Addr()
	}
	date := ""
	if !env.Date.IsZero() {
		date = env.Date.Format(time.RFC3339)
	}
	// Do not mark mail \Seen: heart store dedup on MessageID gives crash-safe at-least-once delivery.
	return MailMessage{
		MessageID: env.MessageID,
		From:      from,
		Subject:   env.Subject,
		Date:      date,
	}
}
