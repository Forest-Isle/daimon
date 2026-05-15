package agent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
)

type SQLiteReplayStore struct {
	db *store.DB
}

func NewSQLiteReplayStore(db *store.DB) *SQLiteReplayStore {
	return &SQLiteReplayStore{db: db}
}

func (s *SQLiteReplayStore) CreateReplay(ctx context.Context, sessionID, agentMode, model string) (string, error) {
	replayID := fmt.Sprintf("rep_%d", time.Now().UnixNano())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_replays (id, session_id, agent_mode, status, model, message_count, started_at, metadata)
		 VALUES (?, ?, ?, 'recording', ?, 0, ?, '{}')`,
		replayID, sessionID, agentMode, model, time.Now(),
	)
	if err != nil {
		return "", err
	}
	return replayID, nil
}

func (s *SQLiteReplayStore) AppendEvent(ctx context.Context, replayID string, event *ReplayEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_replay_events (id, replay_id, sequence, event_type, timestamp, duration_ms, data)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.ID, replayID, event.Sequence, event.EventType, event.Timestamp, event.DurationMs, string(event.Data),
	)
	return err
}

func (s *SQLiteReplayStore) CompleteReplay(ctx context.Context, replayID, status string, messageCount int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_replays
		 SET status = ?, completed_at = ?, message_count = ?
		 WHERE id = ?`,
		status, time.Now(), messageCount, replayID,
	)
	return err
}

func (s *SQLiteReplayStore) LoadReplay(ctx context.Context, replayID string) (*Replay, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, agent_mode, status, model, message_count, started_at, completed_at, metadata
		 FROM agent_replays
		 WHERE id = ?`, replayID,
	)

	var replay Replay
	var completedAt sql.NullTime
	var metadata sql.NullString
	if err := row.Scan(
		&replay.ID,
		&replay.SessionID,
		&replay.AgentMode,
		&replay.Status,
		&replay.Model,
		&replay.MessageCount,
		&replay.StartedAt,
		&completedAt,
		&metadata,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if completedAt.Valid {
		replay.CompletedAt = &completedAt.Time
	}
	if metadata.Valid {
		replay.Metadata = []byte(metadata.String)
	}
	return &replay, nil
}

func (s *SQLiteReplayStore) LoadEvents(ctx context.Context, replayID string, offset, limit int) ([]ReplayEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, replay_id, sequence, event_type, timestamp, duration_ms, data
		 FROM agent_replay_events
		 WHERE replay_id = ?
		 ORDER BY sequence ASC
		 LIMIT ? OFFSET ?`,
		replayID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []ReplayEvent
	for rows.Next() {
		var event ReplayEvent
		var duration sql.NullInt64
		var data string
		if err := rows.Scan(
			&event.ID,
			&event.ReplayID,
			&event.Sequence,
			&event.EventType,
			&event.Timestamp,
			&duration,
			&data,
		); err != nil {
			return nil, err
		}
		if duration.Valid {
			event.DurationMs = &duration.Int64
		}
		event.Data = []byte(data)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *SQLiteReplayStore) ListReplays(ctx context.Context, sessionID string, offset, limit int) ([]Replay, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, agent_mode, status, model, message_count, started_at, completed_at, metadata
		 FROM agent_replays
		 WHERE session_id = ?
		 ORDER BY started_at DESC
		 LIMIT ? OFFSET ?`,
		sessionID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var replays []Replay
	for rows.Next() {
		var replay Replay
		var completedAt sql.NullTime
		var metadata sql.NullString
		if err := rows.Scan(
			&replay.ID,
			&replay.SessionID,
			&replay.AgentMode,
			&replay.Status,
			&replay.Model,
			&replay.MessageCount,
			&replay.StartedAt,
			&completedAt,
			&metadata,
		); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			replay.CompletedAt = &completedAt.Time
		}
		if metadata.Valid {
			replay.Metadata = []byte(metadata.String)
		}
		replays = append(replays, replay)
	}
	return replays, rows.Err()
}

func (s *SQLiteReplayStore) DeleteReplay(ctx context.Context, replayID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_replays WHERE id = ?`, replayID)
	return err
}
