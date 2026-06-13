package heart

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// FollowUp is a one-shot future re-entry trigger planted by an episode. When its
// fire_at elapses, FollowUpSource emits an internal.followup event carrying the
// stored goal, which the heart routes back into a fresh episode.
type FollowUp struct {
	ID            string
	SourceEpisode string
	Kind          string
	Goal          string
	Trigger       string
	FireAt        int64 // unix seconds
}

// FollowUpStore persists planted follow-ups.
type FollowUpStore struct {
	db *sql.DB
}

func NewFollowUpStore(db *sql.DB) *FollowUpStore {
	return &FollowUpStore{db: db}
}

// Create plants a follow-up. A blank ID is assigned; a blank Kind defaults to
// timer.
func (s *FollowUpStore) Create(ctx context.Context, fu FollowUp) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if fu.ID == "" {
		fu.ID = "fu_" + uuid.NewString()
	}
	if fu.Kind == "" {
		fu.Kind = "timer"
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO follow_ups (id, source_episode, kind, goal, trigger, fire_at, state)
		VALUES (?, ?, ?, ?, ?, ?, 'pending')`,
		fu.ID, fu.SourceEpisode, fu.Kind, fu.Goal, fu.Trigger, fu.FireAt); err != nil {
		return fmt.Errorf("create follow-up: %w", err)
	}
	return nil
}

// Due returns pending follow-ups whose fire_at has elapsed, oldest first.
func (s *FollowUpStore) Due(ctx context.Context, nowUnix int64) ([]FollowUp, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_episode, kind, goal, trigger, fire_at
		FROM follow_ups
		WHERE state = 'pending' AND fire_at <= ?
		ORDER BY fire_at ASC`, nowUnix)
	if err != nil {
		return nil, fmt.Errorf("query due follow-ups: %w", err)
	}
	defer rows.Close()

	var out []FollowUp
	for rows.Next() {
		var fu FollowUp
		if err := rows.Scan(&fu.ID, &fu.SourceEpisode, &fu.Kind, &fu.Goal, &fu.Trigger, &fu.FireAt); err != nil {
			return nil, fmt.Errorf("scan follow-up: %w", err)
		}
		out = append(out, fu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate follow-ups: %w", err)
	}
	return out, nil
}

// MarkFired transitions a follow-up to fired so it is not re-emitted.
func (s *FollowUpStore) MarkFired(ctx context.Context, id string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE follow_ups SET state = 'fired' WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("mark follow-up fired: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return fmt.Errorf("follow-up %q not found", id)
	}
	return nil
}

func (s *FollowUpStore) ensure() error {
	if s == nil || s.db == nil {
		return errors.New("follow-up store unavailable")
	}
	return nil
}

// FollowUpSource is a heart Source that polls the follow-up queue and emits an
// internal.followup event for each due entry, marking it fired. The event's
// DedupKey carries the follow-up id so a crash between emit and MarkFired cannot
// trigger a duplicate episode (the heart dedups on source + dedup_key).
type FollowUpSource struct {
	Store    *FollowUpStore
	Interval time.Duration // poll period; defaults to 30s
	Now      func() time.Time
}

func (f *FollowUpSource) Name() string { return "followup" }

func (f *FollowUpSource) Run(ctx context.Context, emit func(Event)) error {
	if f.Store == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	interval := f.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	now := f.Now
	if now == nil {
		now = time.Now
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			due, err := f.Store.Due(ctx, now().Unix())
			if err != nil {
				slog.Error("heart: load due follow-ups failed", "err", err)
				continue
			}
			for _, fu := range due {
				emit(Event{Kind: "internal.followup", Payload: fu.Goal, DedupKey: "followup:" + fu.ID})
				if err := f.Store.MarkFired(ctx, fu.ID); err != nil {
					slog.Error("heart: mark follow-up fired failed", "id", fu.ID, "err", err)
				}
			}
		}
	}
}
