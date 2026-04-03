package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SidechainEntry represents a single event in a sub-agent's execution history.
type SidechainEntry struct {
	ID        string            `json:"id"`
	AgentID   string            `json:"agent_id"`
	ParentID  string            `json:"parent_id"`
	Timestamp time.Time         `json:"timestamp"`
	Type      string            `json:"type"` // "message" | "tool_call" | "tool_result" | "status"
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SidechainStore is the interface for persisting sidechain entries.
type SidechainStore interface {
	Append(entry SidechainEntry) error
	GetByAgent(agentID string) ([]SidechainEntry, error)
	GetByChain(chainID string) ([]SidechainEntry, error)
}

// SidechainRecorder captures the execution history of a single sub-agent
// into a SidechainStore. Each sub-agent gets its own recorder, keeping
// its history separate from the main conversation.
type SidechainRecorder struct {
	agentID  string
	parentID string
	chainID  string
	store    SidechainStore
	mu       sync.Mutex
	entries  []SidechainEntry
}

// NewSidechainRecorder creates a new recorder for the given agent.
func NewSidechainRecorder(agentID, parentID, chainID string, store SidechainStore) *SidechainRecorder {
	return &SidechainRecorder{
		agentID:  agentID,
		parentID: parentID,
		chainID:  chainID,
		store:    store,
	}
}

// Record appends a new entry to the sidechain.
func (r *SidechainRecorder) Record(entryType, content string, metadata map[string]string) error {
	entry := SidechainEntry{
		ID:        uuid.New().String(),
		AgentID:   r.agentID,
		ParentID:  r.parentID,
		Timestamp: time.Now(),
		Type:      entryType,
		Content:   content,
		Metadata:  metadata,
	}

	// Add chainID to metadata
	if entry.Metadata == nil {
		entry.Metadata = make(map[string]string)
	}
	entry.Metadata["chain_id"] = r.chainID

	r.mu.Lock()
	r.entries = append(r.entries, entry)
	r.mu.Unlock()

	if r.store != nil {
		return r.store.Append(entry)
	}
	return nil
}

// RecordMessage is a convenience method for recording a message entry.
func (r *SidechainRecorder) RecordMessage(role, content string) error {
	return r.Record("message", content, map[string]string{"role": role})
}

// RecordToolCall records a tool invocation.
func (r *SidechainRecorder) RecordToolCall(toolName, input string) error {
	return r.Record("tool_call", input, map[string]string{"tool": toolName})
}

// RecordToolResult records a tool execution result.
func (r *SidechainRecorder) RecordToolResult(toolName, output, status string) error {
	return r.Record("tool_result", output, map[string]string{"tool": toolName, "status": status})
}

// RecordStatus records a status change.
func (r *SidechainRecorder) RecordStatus(status, detail string) error {
	return r.Record("status", detail, map[string]string{"status": status})
}

// Entries returns all recorded entries (in-memory copy).
func (r *SidechainRecorder) Entries() []SidechainEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SidechainEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// --- FileSidechainStore ---

// FileSidechainStore persists sidechain entries as JSON files on disk.
// Each agent gets a directory under baseDir, with one JSON file per entry.
// Suitable for debugging and lightweight usage.
type FileSidechainStore struct {
	baseDir string
}

// NewFileSidechainStore creates a new file-based sidechain store.
func NewFileSidechainStore(baseDir string) (*FileSidechainStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sidechain dir: %w", err)
	}
	return &FileSidechainStore{baseDir: baseDir}, nil
}

func (fs *FileSidechainStore) Append(entry SidechainEntry) error {
	agentDir := filepath.Join(fs.baseDir, entry.AgentID)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("create agent sidechain dir: %w", err)
	}

	filename := fmt.Sprintf("%s_%s.json", entry.Timestamp.Format("20060102T150405"), entry.ID[:8])
	path := filepath.Join(agentDir, filename)

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sidechain entry: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}

func (fs *FileSidechainStore) GetByAgent(agentID string) ([]SidechainEntry, error) {
	agentDir := filepath.Join(fs.baseDir, agentID)
	return fs.readEntriesFromDir(agentDir)
}

func (fs *FileSidechainStore) GetByChain(chainID string) ([]SidechainEntry, error) {
	// Scan all agent directories for entries with matching chain_id
	dirEntries, err := os.ReadDir(fs.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var allEntries []SidechainEntry
	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		agentDir := filepath.Join(fs.baseDir, de.Name())
		entries, err := fs.readEntriesFromDir(agentDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Metadata != nil && e.Metadata["chain_id"] == chainID {
				allEntries = append(allEntries, e)
			}
		}
	}

	// Sort by timestamp
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp.Before(allEntries[j].Timestamp)
	})

	return allEntries, nil
}

func (fs *FileSidechainStore) readEntriesFromDir(dir string) ([]SidechainEntry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []SidechainEntry
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var entry SidechainEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	return entries, nil
}

// RecoverFromSidechain reconstructs a sub-agent's message entries from the sidechain store.
func RecoverFromSidechain(store SidechainStore, agentID string) ([]SidechainEntry, error) {
	entries, err := store.GetByAgent(agentID)
	if err != nil {
		return nil, fmt.Errorf("recover sidechain for %s: %w", agentID, err)
	}
	return entries, nil
}

// --- SQLiteSidechainStore ---

// SQLiteSidechainStore persists sidechain entries to a SQLite database.
// Uses the existing store.DB migrations for schema setup.
type SQLiteSidechainStore struct {
	db *sql.DB
}

// NewSQLiteSidechainStore creates a SQLite-based sidechain store.
func NewSQLiteSidechainStore(db *sql.DB) *SQLiteSidechainStore {
	return &SQLiteSidechainStore{db: db}
}

func (ss *SQLiteSidechainStore) Append(entry SidechainEntry) error {
	chainID := ""
	if entry.Metadata != nil {
		chainID = entry.Metadata["chain_id"]
	}

	metaJSON, err := json.Marshal(entry.Metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}

	_, err = ss.db.Exec(
		`INSERT INTO sidechain_entries (id, agent_id, parent_id, chain_id, entry_type, content, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.AgentID, entry.ParentID, chainID,
		entry.Type, entry.Content, string(metaJSON), entry.Timestamp,
	)
	return err
}

func (ss *SQLiteSidechainStore) GetByAgent(agentID string) ([]SidechainEntry, error) {
	rows, err := ss.db.Query(
		`SELECT id, agent_id, parent_id, chain_id, entry_type, content, metadata, created_at
		 FROM sidechain_entries WHERE agent_id = ? ORDER BY created_at ASC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSidechainRows(rows)
}

func (ss *SQLiteSidechainStore) GetByChain(chainID string) ([]SidechainEntry, error) {
	rows, err := ss.db.Query(
		`SELECT id, agent_id, parent_id, chain_id, entry_type, content, metadata, created_at
		 FROM sidechain_entries WHERE chain_id = ? ORDER BY created_at ASC`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSidechainRows(rows)
}

func scanSidechainRows(rows *sql.Rows) ([]SidechainEntry, error) {
	var entries []SidechainEntry
	for rows.Next() {
		var e SidechainEntry
		var chainID, metaJSON string
		if err := rows.Scan(&e.ID, &e.AgentID, &e.ParentID, &chainID, &e.Type, &e.Content, &metaJSON, &e.Timestamp); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(metaJSON), &e.Metadata); err != nil {
			e.Metadata = map[string]string{"chain_id": chainID}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
