package heart

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Store persists the event stream.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Persist stores an event, assigning an ID if absent. It returns inserted=false
// when a source-supplied dedup key collides with an existing event, so callers
// can skip re-handling. Persisting happens before routing, so the stream is the
// durable record of what was observed.
func (s *Store) Persist(ctx context.Context, ev *Event) (bool, error) {
	if err := s.ensure(); err != nil {
		return false, err
	}
	if ev.ID == "" {
		ev.ID = "evt_" + uuid.NewString()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO events (id, source, kind, payload, occurred_at, dedup_key)
		VALUES (?, ?, ?, ?, ?, ?)`,
		ev.ID, ev.Source, ev.Kind, ev.Payload, ev.OccurredAt, ev.DedupKey)
	if err != nil {
		return false, fmt.Errorf("persist event: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("persist event rows: %w", err)
	}
	return rows > 0, nil
}

// PersistRouted stores an event already marked routed, in a single statement, so
// there is no window in which it appears unrouted (and would be replayed by crash
// recovery). It is the storage path for events whose handling is owned by the
// caller, not the heart dispatcher (chat ingress). Returns inserted=false on a
// dedup-key collision, like Persist.
func (s *Store) PersistRouted(ctx context.Context, ev *Event, verdict string) (bool, error) {
	if err := s.ensure(); err != nil {
		return false, err
	}
	if ev.ID == "" {
		ev.ID = "evt_" + uuid.NewString()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO events (id, source, kind, payload, occurred_at, dedup_key, routed_at, verdict)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), ?)`,
		ev.ID, ev.Source, ev.Kind, ev.Payload, ev.OccurredAt, ev.DedupKey, verdict)
	if err != nil {
		return false, fmt.Errorf("persist routed event: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("persist routed event rows: %w", err)
	}
	return rows > 0, nil
}

// MarkRouted records that an event was delivered to the handler.
func (s *Store) MarkRouted(ctx context.Context, id, verdict string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE events SET routed_at = datetime('now'), verdict = ? WHERE id = ?`, verdict, id)
	if err != nil {
		return fmt.Errorf("mark routed: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return fmt.Errorf("event %q not found", id)
	}
	return nil
}

// Unrouted returns persisted events that have not yet been routed, oldest first.
func (s *Store) Unrouted(ctx context.Context) ([]Event, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, kind, payload, occurred_at, dedup_key
		FROM events WHERE routed_at IS NULL
		ORDER BY occurred_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query unrouted: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.Source, &ev.Kind, &ev.Payload, &ev.OccurredAt, &ev.DedupKey); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// RoutedEvent is a summary of a delivered event, for the feedback UX (so a user
// can find an event id to correct its routing).
type RoutedEvent struct {
	ID         string
	Source     string
	Kind       string
	OccurredAt string
}

// RecentRouted returns recently delivered events, newest first.
func (s *Store) RecentRouted(ctx context.Context, limit int) ([]RoutedEvent, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, kind, occurred_at
		FROM events WHERE routed_at IS NOT NULL
		ORDER BY occurred_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query routed events: %w", err)
	}
	defer rows.Close()

	var out []RoutedEvent
	for rows.Next() {
		var ev RoutedEvent
		if err := rows.Scan(&ev.ID, &ev.Source, &ev.Kind, &ev.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan routed event: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routed events: %w", err)
	}
	return out, nil
}

// KindsByID looks up the source and kind of each given event id. Missing ids are
// simply absent from the result (not an error). The sleep phase uses this to join
// routing corrections back to the event they correct, so it can synthesize rules
// keyed by source/kind. An empty id list returns an empty map without a query.
func (s *Store) KindsByID(ctx context.Context, ids []string) (map[string]RoutedEvent, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	out := map[string]RoutedEvent{}
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT id, source, kind, occurred_at FROM events WHERE id IN (` +
		strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup events by id: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ev RoutedEvent
		if err := rows.Scan(&ev.ID, &ev.Source, &ev.Kind, &ev.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out[ev.ID] = ev
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

func (s *Store) ensure() error {
	if s == nil || s.db == nil {
		return errors.New("heart store unavailable")
	}
	return nil
}
