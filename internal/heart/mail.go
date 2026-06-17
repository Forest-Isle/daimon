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
	UID       uint32
	MessageID string
	From      string
	Subject   string
	Date      string
}

type MailStateStore interface {
	GetMailHighWater(ctx context.Context, mailbox string) (uidValidity uint32, lastUID uint32, found bool, err error)
	SetMailHighWater(ctx context.Context, mailbox string, uidValidity uint32, lastUID uint32) error
}

type MailFetcher interface {
	// Status reports the mailbox snapshot captured at SELECT time.
	Status() (uidValidity uint32, uidNext uint32)
	// FetchSince returns messages whose UID >= sinceUID, with UID populated.
	FetchSince(ctx context.Context, sinceUID uint32) ([]MailMessage, error)
	Close() error
}

type MailSource struct {
	Dial         func(ctx context.Context) (MailFetcher, error)
	PollInterval time.Duration
	State        MailStateStore
	Mailbox      string
}

func (m *MailSource) Name() string {
	return "mail"
}

func (m *MailSource) Run(ctx context.Context, emit func(Event) error) error {
	poll := m.PollInterval
	if poll <= 0 {
		poll = 60 * time.Second
	}
	if m.Dial == nil || m.State == nil || m.Mailbox == "" {
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

	curValidity, uidNext := f.Status()
	storedValidity, lastUID, found, err := m.State.GetMailHighWater(ctx, m.Mailbox)
	if err != nil {
		return err
	}
	if !found || storedValidity != curValidity {
		baseline := uint32(0)
		if uidNext > 0 {
			baseline = uidNext - 1
		}
		return m.State.SetMailHighWater(ctx, m.Mailbox, curValidity, baseline)
	}

	// No new mail since the high-water: UIDNEXT has not advanced past it. Skip
	// the search — an IMAP "n:*" range re-matches the highest existing message
	// even when nothing is newer, which would re-emit it (harmlessly deduped)
	// every poll. Servers that omit UIDNEXT (uidNext==0) fall through to search.
	if uidNext != 0 && uidNext <= lastUID+1 {
		return nil
	}

	msgs, err := f.FetchSince(ctx, lastUID+1)
	if err != nil {
		return err
	}
	maxUID := lastUID
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
		if msg.UID > maxUID {
			maxUID = msg.UID
		}
	}
	// Dedup on Message-ID remains crash-safe defense-in-depth: if a crash
	// happens between emit and SetMailHighWater, the next poll re-fetches from
	// lastUID+1 and the store's UNIQUE(source,dedup_key) collapses the re-emit.
	if maxUID > lastUID {
		return m.State.SetMailHighWater(ctx, m.Mailbox, curValidity, maxUID)
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
		data, err := c.Select(mailbox, nil).Wait()
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("mail: select mailbox: %w", err)
		}
		return &imapFetcher{
			client:      c,
			mailbox:     mailbox,
			uidValidity: data.UIDValidity,
			uidNext:     uint32(data.UIDNext),
		}, nil
	}
}

type imapFetcher struct {
	client      *imapclient.Client
	mailbox     string
	uidValidity uint32
	uidNext     uint32
}

func (f *imapFetcher) Status() (uint32, uint32) {
	return f.uidValidity, f.uidNext
}

func (f *imapFetcher) FetchSince(ctx context.Context, sinceUID uint32) ([]MailMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sinceUID == 0 {
		sinceUID = 1
	}

	uidSet := imap.UIDSet{{Start: imap.UID(sinceUID), Stop: 0}}
	data, err := f.client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{uidSet}}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("mail: uid search: %w", err)
	}
	uids := data.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	buffers, err := f.client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("mail: fetch envelopes: %w", err)
	}

	msgs := make([]MailMessage, 0, len(buffers))
	for _, buf := range buffers {
		if buf.Envelope == nil {
			continue
		}
		msg := mailMessageFromEnvelope(buf.Envelope)
		msg.UID = uint32(buf.UID)
		msgs = append(msgs, msg)
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
