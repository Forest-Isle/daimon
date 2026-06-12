package taskruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

type Metadata struct {
	Goal              string   `json:"goal,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	NextAction        string   `json:"next_action,omitempty"`
	SessionID         string   `json:"session_id,omitempty"`
	SessionChannel    string   `json:"session_channel,omitempty"`
	SessionChannelID  string   `json:"session_channel_id,omitempty"`
	ScheduledTaskID   string   `json:"scheduled_task_id,omitempty"`
	BackgroundAgentID string   `json:"background_agent_id,omitempty"`
	WakeupAt          string   `json:"wakeup_at,omitempty"`
}

type Entry struct {
	ID          string
	ParentID    string
	Kind        string
	State       State
	Title       string
	Description string
	Assignee    string
	DependsOn   string
	CreatedAt   string
	UpdatedAt   string
	StartedAt   string
	CompletedAt string
	Heartbeat   string
	Result      string
	Metadata    Metadata
}

type CreateInput struct {
	ID          string
	ParentID    string
	Kind        string
	Title       string
	Description string
	Assignee    string
	DependsOn   string
	Metadata    Metadata
}

type Ledger struct {
	db *sql.DB
}

func NewLedger(db *sql.DB) *Ledger {
	return &Ledger{db: db}
}

func ScheduledLedgerID(taskID string) string {
	return "scheduled_" + taskID
}

func (l *Ledger) Create(ctx context.Context, in CreateInput) (*Entry, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("task ledger unavailable")
	}
	if in.ID == "" {
		in.ID = "task_" + uuid.NewString()
	}
	if in.Kind == "" {
		in.Kind = "user_request"
	}
	if in.Title == "" {
		in.Title = compactTitle(in.Description)
	}
	if in.Metadata.Goal == "" {
		in.Metadata.Goal = firstNonEmpty(in.Title, in.Description)
	}
	meta, err := json.Marshal(in.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal task metadata: %w", err)
	}
	_, err = l.db.ExecContext(ctx, `
		INSERT INTO task_ledger
			(id, parent_id, kind, state, title, description, assignee, depends_on, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.ParentID, in.Kind, StatePending, in.Title, in.Description, in.Assignee, in.DependsOn, string(meta))
	if err != nil {
		return nil, fmt.Errorf("create task ledger entry: %w", err)
	}
	return l.Get(ctx, in.ID)
}

func (l *Ledger) EnsureScheduledTask(ctx context.Context, scheduledTaskID, title, description, cronExpr, notifyTo, notifyID string) (*Entry, error) {
	id := ScheduledLedgerID(scheduledTaskID)
	meta := Metadata{
		Goal:             description,
		NextAction:       "wait for scheduled wakeup",
		ScheduledTaskID:  scheduledTaskID,
		SessionChannel:   notifyTo,
		SessionChannelID: notifyID,
		WakeupAt:         cronExpr,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if title == "" {
		title = compactTitle(description)
	}
	_, err = l.db.ExecContext(ctx, `
		INSERT INTO task_ledger
			(id, kind, state, title, description, assignee, metadata)
		VALUES (?, 'scheduled', 'pending', ?, ?, 'scheduler', ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			description = excluded.description,
			metadata = excluded.metadata,
			updated_at = CURRENT_TIMESTAMP`,
		id, title, description, string(metaJSON))
	if err != nil {
		return nil, fmt.Errorf("ensure scheduled task ledger: %w", err)
	}
	return l.Get(ctx, id)
}

func (l *Ledger) MarkRunning(ctx context.Context, id string, meta Metadata, evidence string) error {
	entry, err := l.Get(ctx, id)
	if err != nil {
		return err
	}
	merged := mergeMetadata(entry.Metadata, meta)
	if evidence != "" {
		merged.Evidence = append(merged.Evidence, evidence)
	}
	metaJSON, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, `
		UPDATE task_ledger
		SET state = ?, started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		    heartbeat = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP, metadata = ?
		WHERE id = ?`, StateRunning, string(metaJSON), id)
	return err
}

func (l *Ledger) Complete(ctx context.Context, id, result string, evidence ...string) error {
	return l.finish(ctx, id, StateSucceeded, result, evidence...)
}

func (l *Ledger) Fail(ctx context.Context, id, result string, evidence ...string) error {
	return l.finish(ctx, id, StateFailed, result, evidence...)
}

func (l *Ledger) Cancel(ctx context.Context, id, result string, evidence ...string) error {
	return l.finish(ctx, id, StateCancelled, result, evidence...)
}

func (l *Ledger) finish(ctx context.Context, id string, state State, result string, evidence ...string) error {
	entry, err := l.Get(ctx, id)
	if err != nil {
		return err
	}
	meta := entry.Metadata
	meta.Evidence = append(meta.Evidence, evidence...)
	if result != "" {
		meta.NextAction = ""
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, `
		UPDATE task_ledger
		SET state = ?, result = ?, completed_at = CURRENT_TIMESTAMP,
		    heartbeat = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP, metadata = ?
		WHERE id = ?`, state, result, string(metaJSON), id)
	return err
}

func (l *Ledger) AddEvidence(ctx context.Context, id string, evidence string) error {
	if evidence == "" {
		return nil
	}
	entry, err := l.Get(ctx, id)
	if err != nil {
		return err
	}
	meta := entry.Metadata
	meta.Evidence = append(meta.Evidence, evidence)
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, `UPDATE task_ledger SET metadata = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(metaJSON), id)
	return err
}

func (l *Ledger) Get(ctx context.Context, id string) (*Entry, error) {
	row := l.db.QueryRowContext(ctx, `
		SELECT id, parent_id, kind, state, title, description, assignee, depends_on,
		       created_at, updated_at, COALESCE(started_at, ''), COALESCE(completed_at, ''),
		       COALESCE(heartbeat, ''), result, metadata
		FROM task_ledger WHERE id = ?`, id)
	return scanEntry(row)
}

func (l *Ledger) List(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT id, parent_id, kind, state, title, description, assignee, depends_on,
		       created_at, updated_at, COALESCE(started_at, ''), COALESCE(completed_at, ''),
		       COALESCE(heartbeat, ''), result, metadata
		FROM task_ledger
		ORDER BY updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *entry)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntry(row rowScanner) (*Entry, error) {
	var e Entry
	var state, metaJSON string
	if err := row.Scan(&e.ID, &e.ParentID, &e.Kind, &state, &e.Title, &e.Description,
		&e.Assignee, &e.DependsOn, &e.CreatedAt, &e.UpdatedAt, &e.StartedAt,
		&e.CompletedAt, &e.Heartbeat, &e.Result, &metaJSON); err != nil {
		return nil, err
	}
	e.State = State(state)
	if strings.TrimSpace(metaJSON) != "" {
		_ = json.Unmarshal([]byte(metaJSON), &e.Metadata)
	}
	return &e, nil
}

type Checkpoint struct {
	ID           string
	SessionID    string
	SubtaskIndex int
	Observations []string
	PlanJSON     string
	CreatedAt    string
}

func (l *Ledger) SaveCheckpoint(ctx context.Context, cp Checkpoint) error {
	if l == nil || l.db == nil {
		return nil
	}
	if cp.SessionID == "" {
		return fmt.Errorf("checkpoint session_id is required")
	}
	if cp.ID == "" {
		cp.ID = "checkpoint_" + cp.SessionID
	}
	if cp.PlanJSON == "" {
		cp.PlanJSON = "{}"
	}
	observations, err := json.Marshal(cp.Observations)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, `
		INSERT INTO task_checkpoints (id, session_id, subtask_index, observations_json, plan_json)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			subtask_index = excluded.subtask_index,
			observations_json = excluded.observations_json,
			plan_json = excluded.plan_json,
			created_at = CURRENT_TIMESTAMP`,
		cp.ID, cp.SessionID, cp.SubtaskIndex, string(observations), cp.PlanJSON)
	return err
}

func (l *Ledger) GetCheckpoint(ctx context.Context, sessionID string) (*Checkpoint, error) {
	row := l.db.QueryRowContext(ctx, `
		SELECT id, session_id, subtask_index, observations_json, plan_json, created_at
		FROM task_checkpoints WHERE session_id = ?`, sessionID)
	var cp Checkpoint
	var observationsJSON string
	if err := row.Scan(&cp.ID, &cp.SessionID, &cp.SubtaskIndex, &observationsJSON, &cp.PlanJSON, &cp.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(observationsJSON), &cp.Observations)
	return &cp, nil
}

func RecentObservations(messages []struct{ Role, Content string }, max int) []string {
	if max <= 0 {
		max = 5
	}
	out := make([]string, 0, max)
	for i := len(messages) - 1; i >= 0 && len(out) < max; i-- {
		if messages[i].Role != "assistant" && messages[i].Role != "tool_result" {
			continue
		}
		line := strings.TrimSpace(strings.Join(strings.Fields(messages[i].Content), " "))
		if line == "" {
			continue
		}
		if len(line) > 240 {
			line = line[:237] + "..."
		}
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func mergeMetadata(base, override Metadata) Metadata {
	if override.Goal != "" {
		base.Goal = override.Goal
	}
	if override.NextAction != "" {
		base.NextAction = override.NextAction
	}
	if override.SessionID != "" {
		base.SessionID = override.SessionID
	}
	if override.SessionChannel != "" {
		base.SessionChannel = override.SessionChannel
	}
	if override.SessionChannelID != "" {
		base.SessionChannelID = override.SessionChannelID
	}
	if override.ScheduledTaskID != "" {
		base.ScheduledTaskID = override.ScheduledTaskID
	}
	if override.BackgroundAgentID != "" {
		base.BackgroundAgentID = override.BackgroundAgentID
	}
	if override.WakeupAt != "" {
		base.WakeupAt = override.WakeupAt
	}
	return base
}

func compactTitle(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func NowUTCString() string {
	return time.Now().UTC().Format(time.RFC3339)
}
