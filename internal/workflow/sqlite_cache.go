package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type SQLiteCache struct {
	db *sql.DB
}

func NewSQLiteCache(db *sql.DB) *SQLiteCache {
	return &SQLiteCache{db: db}
}

func (c *SQLiteCache) Get(ctx context.Context, key string) (*CacheRecord, bool, error) {
	if c == nil || c.db == nil {
		return nil, false, nil
	}
	row := c.db.QueryRowContext(ctx, `
		SELECT workflow_name, workflow_hash, stage_id, step_id, cache_key, result_json, created_at
		FROM workflow_step_cache
		WHERE cache_key = ?`, key)
	var record CacheRecord
	var resultJSON string
	if err := row.Scan(&record.WorkflowName, &record.WorkflowHash, &record.StageID, &record.StepID, &record.CacheKey, &resultJSON, &record.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := json.Unmarshal([]byte(resultJSON), &record.Result); err != nil {
		return nil, false, fmt.Errorf("unmarshal workflow cache result: %w", err)
	}
	return &record, true, nil
}

func (c *SQLiteCache) Put(ctx context.Context, record CacheRecord) error {
	if c == nil || c.db == nil {
		return nil
	}
	if record.CacheKey == "" {
		return fmt.Errorf("cache record requires cache key")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	resultJSON, err := json.Marshal(record.Result)
	if err != nil {
		return fmt.Errorf("marshal workflow cache result: %w", err)
	}
	_, err = c.db.ExecContext(ctx, `
		INSERT INTO workflow_step_cache
			(cache_key, workflow_name, workflow_hash, stage_id, step_id, result_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			workflow_name = excluded.workflow_name,
			workflow_hash = excluded.workflow_hash,
			stage_id = excluded.stage_id,
			step_id = excluded.step_id,
			result_json = excluded.result_json,
			created_at = excluded.created_at`,
		record.CacheKey, record.WorkflowName, record.WorkflowHash, record.StageID, record.StepID, string(resultJSON), record.CreatedAt)
	return err
}
