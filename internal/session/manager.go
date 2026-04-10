package session

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/store"
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

	// Check in-memory.md cache first
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
	defer func() { _ = tx.Rollback() }()

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
	defer func() { _ = stmt.Close() }()

	for _, msg := range sess.History() {
		if _, err := stmt.ExecContext(ctx, msg.ID, sess.ID, msg.Role, msg.Content, msg.ToolName, msg.ToolInput, msg.CreatedAt); err != nil {
			return fmt.Errorf("insert message %s: %w", msg.ID, err)
		}
	}

	return tx.Commit()
}

func (m *Manager) loadFromDB(ctx context.Context, channel, channelID string) (*Session, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(parent_session_id,''), created_at, updated_at FROM sessions WHERE channel = ? AND channel_id = ?`,
		channel, channelID)

	var sess Session
	sess.Channel = channel
	sess.ChannelID = channelID
	sess.Metadata = make(map[string]string)

	if err := row.Scan(&sess.ID, &sess.ParentSessionID, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
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
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &msg.ToolName, &msg.ToolInput, &msg.CreatedAt); err != nil {
			return nil, err
		}
		sess.Messages = append(sess.Messages, msg)
	}

	return &sess, rows.Err()
}

// Reset deletes the session for the given channel+channelID from both memory.md and DB,
// so the next Get call will create a fresh session.
func (m *Manager) Reset(ctx context.Context, channel, channelID string) error {
	key := sessionKey(channel, channelID)

	// Load existing session to get its ID for DB cleanup
	if v, ok := m.sessions.Load(key); ok {
		sess := v.(*Session)
		if _, err := m.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sess.ID); err != nil {
			return fmt.Errorf("delete messages: %w", err)
		}
		if _, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
	}

	m.sessions.Delete(key)
	slog.Info("session reset", "channel", channel, "channel_id", channelID)
	return nil
}

func (m *Manager) insertSession(ctx context.Context, sess *Session) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sessions (id, channel, channel_id, parent_session_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Channel, sess.ChannelID, sess.ParentSessionIDOrNull(), sess.CreatedAt, sess.UpdatedAt)
	return err
}

// GetSessionChain returns the full chain of sessions from the given session back to the root.
// The returned slice is ordered [current, parent, grandparent, ..., root].
// Circular references are detected and stop the traversal.
func (m *Manager) GetSessionChain(ctx context.Context, sessionID string) ([]*Session, error) {
	var chain []*Session
	seen := make(map[string]bool) // guard against circular references

	currentID := sessionID
	for currentID != "" && !seen[currentID] {
		seen[currentID] = true

		row := m.db.QueryRowContext(ctx,
			`SELECT id, channel, channel_id, COALESCE(parent_session_id,''), created_at, updated_at
			 FROM sessions WHERE id = ?`, currentID)

		var sess Session
		sess.Metadata = make(map[string]string)
		if err := row.Scan(&sess.ID, &sess.Channel, &sess.ChannelID,
			&sess.ParentSessionID, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			if err == sql.ErrNoRows {
				break
			}
			return nil, fmt.Errorf("load session %s: %w", currentID, err)
		}

		chain = append(chain, &sess)
		currentID = sess.ParentSessionID
	}

	return chain, nil
}

// GetRootSession returns the root (oldest ancestor) session of the given session's chain.
// If the session has no parent, it returns the session itself.
func (m *Manager) GetRootSession(ctx context.Context, sessionID string) (*Session, error) {
	chain, err := m.GetSessionChain(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return chain[len(chain)-1], nil
}

// CreateChildSession creates a new session linked to the given parent session.
// This is used when compression triggers a new session to maintain continuity.
func (m *Manager) CreateChildSession(ctx context.Context, parentSess *Session) (*Session, error) {
	child := &Session{
		ID:              fmt.Sprintf("sess_%d", time.Now().UnixNano()),
		Channel:         parentSess.Channel,
		ChannelID:       parentSess.ChannelID,
		ParentSessionID: parentSess.ID,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Metadata:        make(map[string]string),
	}

	if err := m.insertSession(ctx, child); err != nil {
		return nil, fmt.Errorf("insert child session: %w", err)
	}

	// Update in-memory cache to point to the new child session
	key := sessionKey(child.Channel, child.ChannelID)
	m.sessions.Store(key, child)

	slog.Info("child session created",
		"id", child.ID,
		"parent_id", parentSess.ID,
		"channel", child.Channel,
	)
	return child, nil
}
