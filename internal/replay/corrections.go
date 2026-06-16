package replay

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Correction records a user-marked replay session that must remain good in the
// §4.10 regression set.
type Correction struct {
	SessionID string
	Note      string
	CreatedAt int64
}

type CorrectionStore struct {
	db *sql.DB
}

func NewCorrectionStore(db *sql.DB) *CorrectionStore {
	return &CorrectionStore{db: db}
}

func (s *CorrectionStore) ensure() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("correction store unavailable")
	}
	return nil
}

// Mark records that sessionID was corrected by the user. Re-marking a session
// updates its note and timestamp, keeping one durable row per session.
func (s *CorrectionStore) Mark(ctx context.Context, sessionID, note string, nowUnix int64) error {
	if err := s.ensure(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("correction session id is required")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO regression_corrections (session_id, note, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			note = excluded.note,
			created_at = excluded.created_at`,
		sessionID, note, nowUnix); err != nil {
		return fmt.Errorf("mark correction: %w", err)
	}
	return nil
}

// List returns all corrections in the order they were recorded.
func (s *CorrectionStore) List(ctx context.Context) ([]Correction, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, note, created_at
		FROM regression_corrections
		ORDER BY created_at ASC, session_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list corrections: %w", err)
	}
	defer rows.Close()

	var out []Correction
	for rows.Next() {
		var c Correction
		if err := rows.Scan(&c.SessionID, &c.Note, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan correction: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate corrections: %w", err)
	}
	return out, nil
}

// SessionIDSet returns the corrected session id set consumed by SelectRegression.
func (s *CorrectionStore) SessionIDSet(ctx context.Context) (map[string]bool, error) {
	corrections, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(corrections))
	for _, c := range corrections {
		out[c.SessionID] = true
	}
	return out, nil
}
