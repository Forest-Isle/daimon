package attention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Feedback is a user correction of a routing decision: what the router did
// versus what it should have done. The sleep phase later mines these to
// synthesize new rules.
type Feedback struct {
	EventID        string
	ExpectedAction string
	GivenAction    string
	Note           string
}

// FeedbackStore persists routing corrections.
type FeedbackStore struct {
	db *sql.DB
}

func NewFeedbackStore(db *sql.DB) *FeedbackStore {
	return &FeedbackStore{db: db}
}

// Record stores one correction.
func (s *FeedbackStore) Record(ctx context.Context, fb Feedback) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO attention_feedback (event_id, expected_action, given_action, note) VALUES (?, ?, ?, ?)`,
		fb.EventID, fb.ExpectedAction, fb.GivenAction, fb.Note); err != nil {
		return fmt.Errorf("record feedback: %w", err)
	}
	return nil
}

// Recent returns the most recent corrections, newest first.
func (s *FeedbackStore) Recent(ctx context.Context, limit int) ([]Feedback, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT event_id, expected_action, given_action, note FROM attention_feedback ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list feedback: %w", err)
	}
	defer rows.Close()

	var out []Feedback
	for rows.Next() {
		var fb Feedback
		if err := rows.Scan(&fb.EventID, &fb.ExpectedAction, &fb.GivenAction, &fb.Note); err != nil {
			return nil, fmt.Errorf("scan feedback: %w", err)
		}
		out = append(out, fb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feedback: %w", err)
	}
	return out, nil
}

func (s *FeedbackStore) ensure() error {
	if s == nil || s.db == nil {
		return errors.New("feedback store unavailable")
	}
	return nil
}
