package session

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/punkopunko/ironclaw/internal/store"
)

// Manager handles session lifecycle: lookup, creation, persistence.
type Manager struct {
	db       *store.DB
	sessions sync.Map // key: "channel:channel_id" → *Session
}

func NewManager(db *store.DB) *Manager {
	return &Manager{db: db}
}

func sessionKey(channel, channelID string) string {
	return channel + ":" + channelID
}

// Get returns an existing session or creates a new one.
func (m *Manager) Get(ctx context.Context, channel, channelID string) (*Session, error) {
	key := sessionKey(channel, channelID)

	// Check in-memory cache first
	if v, ok := m.sessions.Load(key); ok {
		return v.(*Session), nil
	}

	// Try loading from DB
	sess, err := m.loadFromDB(ctx, channel, channelID)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		m.sessions.Store(key, sess)
		return sess, nil
	}

	// Create new session
	sess = &Session{
		ID:        fmt.Sprintf("sess_%d", time.Now().UnixNano()),
		Channel:   channel,
		ChannelID: channelID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  make(map[string]string),
	}

	if err := m.insertSession(ctx, sess); err != nil {
		return nil, err
	}

	m.sessions.Store(key, sess)
	slog.Info("session created", "id", sess.ID, "channel", channel, "channel_id", channelID)
	return sess, nil
}

// Persist saves the session's messages to the database.
func (m *Manager) Persist(ctx context.Context, sess *Session) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update session timestamp
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, sess.ID); err != nil {
		return err
	}

	// Upsert messages
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO messages (id, session_id, role, content, tool_name, tool_input, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, msg := range sess.History() {
		if _, err := stmt.ExecContext(ctx, msg.ID, sess.ID, msg.Role, msg.Content, msg.ToolName, msg.ToolInput, msg.CreatedAt); err != nil {
			return fmt.Errorf("insert message %s: %w", msg.ID, err)
		}
	}

	return tx.Commit()
}

func (m *Manager) loadFromDB(ctx context.Context, channel, channelID string) (*Session, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT id, created_at, updated_at FROM sessions WHERE channel = ? AND channel_id = ?`,
		channel, channelID)

	var sess Session
	sess.Channel = channel
	sess.ChannelID = channelID
	sess.Metadata = make(map[string]string)

	if err := row.Scan(&sess.ID, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	// Load messages
	rows, err := m.db.QueryContext(ctx,
		`SELECT id, role, content, COALESCE(tool_name,''), COALESCE(tool_input,''), created_at
		 FROM messages WHERE session_id = ? ORDER BY created_at`, sess.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &msg.ToolName, &msg.ToolInput, &msg.CreatedAt); err != nil {
			return nil, err
		}
		sess.Messages = append(sess.Messages, msg)
	}

	return &sess, rows.Err()
}

func (m *Manager) insertSession(ctx context.Context, sess *Session) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sessions (id, channel, channel_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		sess.ID, sess.Channel, sess.ChannelID, sess.CreatedAt, sess.UpdatedAt)
	return err
}
