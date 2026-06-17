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
	uidValidity uint32
	uidNext     uint32
	msgs        []MailMessage
	fetchErr    error
	fetchSince  uint32
	fetched     bool
}

func (s *stubMailFetcher) Status() (uint32, uint32) {
	return s.uidValidity, s.uidNext
}

func (s *stubMailFetcher) FetchSince(ctx context.Context, sinceUID uint32) ([]MailMessage, error) {
	s.fetched = true
	s.fetchSince = sinceUID
	return s.msgs, s.fetchErr
}

func (s *stubMailFetcher) Close() error {
	return nil
}

type stubMailStateStore struct {
	mu    sync.Mutex
	state map[string]stubMailState
}

type stubMailState struct {
	uidValidity uint32
	lastUID     uint32
}

func newStubMailStateStore() *stubMailStateStore {
	return &stubMailStateStore{state: map[string]stubMailState{}}
}

func (s *stubMailStateStore) GetMailHighWater(ctx context.Context, mailbox string) (uint32, uint32, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[mailbox]
	return st.uidValidity, st.lastUID, ok, nil
}

func (s *stubMailStateStore) SetMailHighWater(ctx context.Context, mailbox string, uidValidity uint32, lastUID uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[mailbox] = stubMailState{uidValidity: uidValidity, lastUID: lastUID}
	return nil
}

func (s *stubMailStateStore) get(mailbox string) (stubMailState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.state[mailbox]
	return st, ok
}

func TestMailSourcePollFirstRunBaselinesWithoutEmitting(t *testing.T) {
	state := newStubMailStateStore()
	fetcher := &stubMailFetcher{uidValidity: 7, uidNext: 100}
	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			return fetcher, nil
		},
		State:   state,
		Mailbox: "INBOX",
	}

	if err := src.pollOnce(context.Background(), func(ev Event) error {
		t.Fatalf("emit called on first baseline: %+v", ev)
		return nil
	}); err != nil {
		t.Fatalf("pollOnce() error = %v", err)
	}
	if fetcher.fetched {
		t.Fatal("FetchSince called while establishing baseline")
	}
	got, ok := state.get("INBOX")
	if !ok {
		t.Fatal("mail high-water mark was not persisted")
	}
	if got.uidValidity != 7 || got.lastUID != 99 {
		t.Fatalf("state = %+v, want uidValidity=7 lastUID=99", got)
	}
}

func TestMailSourcePollFetchesAfterHighWaterAndPersistsMaxUID(t *testing.T) {
	state := newStubMailStateStore()
	if err := state.SetMailHighWater(context.Background(), "INBOX", 7, 100); err != nil {
		t.Fatal(err)
	}
	fetcher := &stubMailFetcher{
		uidValidity: 7,
		uidNext:     103,
		msgs: []MailMessage{
			{UID: 101, MessageID: "msg-1", From: "one@example.com", Subject: "One", Date: "2026-06-18T10:00:00Z"},
			{UID: 102, MessageID: "msg-2", From: "two@example.com", Subject: "Two", Date: "2026-06-18T11:00:00Z"},
		},
	}
	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			return fetcher, nil
		},
		State:   state,
		Mailbox: "INBOX",
	}

	var events []Event
	if err := src.pollOnce(context.Background(), func(ev Event) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("pollOnce() error = %v", err)
	}
	if fetcher.fetchSince != 101 {
		t.Fatalf("FetchSince called with %d, want 101", fetcher.fetchSince)
	}
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
	got, ok := state.get("INBOX")
	if !ok {
		t.Fatal("mail high-water mark was not persisted")
	}
	if got.uidValidity != 7 || got.lastUID != 102 {
		t.Fatalf("state = %+v, want uidValidity=7 lastUID=102", got)
	}
}

func TestMailSourcePollUIDValidityChangeRebaselinesWithoutEmitting(t *testing.T) {
	state := newStubMailStateStore()
	if err := state.SetMailHighWater(context.Background(), "INBOX", 5, 500); err != nil {
		t.Fatal(err)
	}
	fetcher := &stubMailFetcher{uidValidity: 6, uidNext: 40}
	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			return fetcher, nil
		},
		State:   state,
		Mailbox: "INBOX",
	}

	if err := src.pollOnce(context.Background(), func(ev Event) error {
		t.Fatalf("emit called after UIDVALIDITY change: %+v", ev)
		return nil
	}); err != nil {
		t.Fatalf("pollOnce() error = %v", err)
	}
	if fetcher.fetched {
		t.Fatal("FetchSince called while re-baselining")
	}
	got, ok := state.get("INBOX")
	if !ok {
		t.Fatal("mail high-water mark was not persisted")
	}
	if got.uidValidity != 6 || got.lastUID != 39 {
		t.Fatalf("state = %+v, want uidValidity=6 lastUID=39", got)
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
		State:        newStubMailStateStore(),
		Mailbox:      "INBOX",
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
	state := newStubMailStateStore()
	if err := state.SetMailHighWater(ctx, "INBOX", 7, 100); err != nil {
		t.Fatal(err)
	}
	src := &MailSource{
		Dial: func(ctx context.Context) (MailFetcher, error) {
			cancel()
			return &stubMailFetcher{uidValidity: 7, uidNext: 101, fetchErr: errors.New("fetch failed")}, nil
		},
		PollInterval: time.Hour,
		State:        state,
		Mailbox:      "INBOX",
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

func TestMailSourceRunNilDialOrStateBlocksUntilCancel(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  *MailSource
	}{
		{name: "nil dial", src: &MailSource{PollInterval: time.Hour, State: newStubMailStateStore(), Mailbox: "INBOX"}},
		{name: "nil state", src: &MailSource{PollInterval: time.Hour, Dial: func(ctx context.Context) (MailFetcher, error) {
			return &stubMailFetcher{}, nil
		}, Mailbox: "INBOX"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- tc.src.Run(ctx, func(ev Event) error {
					t.Errorf("emit called with invalid mail source config")
					return nil
				})
			}()
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() error = %v, want context.Canceled", err)
			}
		})
	}
}
