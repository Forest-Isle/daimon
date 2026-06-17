package heart

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubMailFetcher struct {
	msgs     []MailMessage
	fetchErr error
}

func (s *stubMailFetcher) FetchUnseen(ctx context.Context) ([]MailMessage, error) {
	return s.msgs, s.fetchErr
}

func (s *stubMailFetcher) Close() error {
	return nil
}

func TestMailSourceRunEmitsFetchedMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			return &stubMailFetcher{msgs: []MailMessage{
				{MessageID: "msg-1", From: "one@example.com", Subject: "One", Date: "2026-06-18T10:00:00Z"},
				{MessageID: "msg-2", From: "two@example.com", Subject: "Two", Date: "2026-06-18T11:00:00Z"},
			}}, nil
		},
		PollInterval: time.Hour,
	}

	var (
		mu     sync.Mutex
		events []Event
	)
	done := make(chan error, 1)
	go func() {
		done <- src.Run(ctx, func(ev Event) error {
			mu.Lock()
			events = append(events, ev)
			if len(events) == 2 {
				cancel()
			}
			mu.Unlock()
			return nil
		})
	}()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for i, ev := range events {
		if ev.Kind != "mail.received" {
			t.Fatalf("events[%d].Kind = %q, want mail.received", i, ev.Kind)
		}
		wantID := []string{"msg-1", "msg-2"}[i]
		if ev.DedupKey != wantID {
			t.Fatalf("events[%d].DedupKey = %q, want %q", i, ev.DedupKey, wantID)
		}
		for _, want := range []string{
			`"from":"` + []string{"one@example.com", "two@example.com"}[i] + `"`,
			`"subject":"` + []string{"One", "Two"}[i] + `"`,
			`"message_id":"` + wantID + `"`,
		} {
			if !strings.Contains(ev.Payload, want) {
				t.Fatalf("events[%d].Payload = %q, want substring %q", i, ev.Payload, want)
			}
		}
	}
}

func TestMailSourceRunDialErrorContinuesUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			cancel()
			return nil, errors.New("dial failed")
		},
		PollInterval: time.Hour,
	}

	emitted := false
	err := src.Run(ctx, func(ev Event) error {
		emitted = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if emitted {
		t.Fatal("emit called on dial error")
	}
}

func TestMailSourceRunFetchErrorContinuesUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			cancel()
			return &stubMailFetcher{fetchErr: errors.New("fetch failed")}, nil
		},
		PollInterval: time.Hour,
	}

	emitted := false
	err := src.Run(ctx, func(ev Event) error {
		emitted = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if emitted {
		t.Fatal("emit called on fetch error")
	}
}

func TestMailSourceRunNilDialBlocksUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := &MailSource{PollInterval: time.Hour}
	err := src.Run(ctx, func(ev Event) error {
		t.Fatal("emit called with nil dial")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}
