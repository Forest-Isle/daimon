package memory

import (
	"context"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

type AccessLog struct {
	db *store.DB
}

func NewAccessLog(db *store.DB) *AccessLog {
	return &AccessLog{db: db}
}

func (al *AccessLog) RecordAccess(ctx context.Context, factID, sessionID string) error {
	_, err := al.db.ExecContext(ctx, `
		INSERT INTO memory_access_log (fact_id, session_id)
		VALUES (?, ?)
	`, factID, sessionID)

	if err != nil {
		return err
	}

	// Update stats synchronously for tests
	al.updateStats(factID)
	return nil
}

func (al *AccessLog) GetStats(ctx context.Context, factID string) (count int, lastAccess time.Time, err error) {
	err = al.db.QueryRowContext(ctx, `
		SELECT access_count, last_access
		FROM memory_access_stats
		WHERE fact_id = ?
	`, factID).Scan(&count, &lastAccess)
	return
}

func (al *AccessLog) updateStats(factID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = al.db.ExecContext(ctx, `
		INSERT INTO memory_access_stats (fact_id, access_count, last_access, first_access)
		SELECT ?, COUNT(*), MAX(accessed_at), MIN(accessed_at)
		FROM memory_access_log
		WHERE fact_id = ?
		ON CONFLICT(fact_id) DO UPDATE SET
			access_count = excluded.access_count,
			last_access = excluded.last_access
	`, factID, factID)
}
