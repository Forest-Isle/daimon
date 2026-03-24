package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// IndexVersion is the current version of the index format.
const IndexVersion = "2.0"

// Index represents the index.json structure.
type Index struct {
	Version         string                 `json:"version"`
	LastUpdated     time.Time              `json:"last_updated"`
	TotalFacts      int                    `json:"total_facts"`
	Scopes          map[string]*ScopeIndex `json:"scopes"`
	EmbeddingsSynced bool                  `json:"embeddings_synced"`
	mu              sync.RWMutex           `json:"-"`
}

// ScopeIndex represents index data for a specific scope.
type ScopeIndex struct {
	Count    int                       `json:"count"`
	Sessions map[string]*SessionIndex  `json:"sessions,omitempty"`
	Users    map[string]*UserIndex     `json:"users,omitempty"`
	File     string                    `json:"file,omitempty"`      // for global scope
	LastModified time.Time             `json:"last_modified,omitempty"`
	Hash     string                    `json:"hash,omitempty"`
	FactIDs  []string                  `json:"fact_ids,omitempty"`
}

// SessionIndex represents index data for a session.
type SessionIndex struct {
	File         string    `json:"file"`
	ChunkCount   int       `json:"chunk_count"`
	Chunks       []string  `json:"chunks,omitempty"`
	LastModified time.Time `json:"last_modified"`
	Hash         string    `json:"hash"`
	FactIDs      []string  `json:"fact_ids"`
}

// UserIndex represents index data for a user.
type UserIndex struct {
	File         string    `json:"file"`
	ChunkCount   int       `json:"chunk_count"`
	Chunks       []string  `json:"chunks,omitempty"`
	LastModified time.Time `json:"last_modified"`
	Hash         string    `json:"hash"`
	FactIDs      []string  `json:"fact_ids"`
}

// IndexManager manages the index.json file.
type IndexManager struct {
	indexPath string
	index     *Index
	mu        sync.RWMutex
}

// NewIndexManager creates a new IndexManager.
func NewIndexManager(storageDir string) *IndexManager {
	return &IndexManager{
		indexPath: filepath.Join(storageDir, "index.json"),
		index: &Index{
			Version:         IndexVersion,
			LastUpdated:     time.Now(),
			TotalFacts:      0,
			Scopes:          make(map[string]*ScopeIndex),
			EmbeddingsSynced: false,
		},
	}
}

// Load loads the index from disk, or creates a new one if it doesn't exist.
func (im *IndexManager) Load() error {
	im.mu.Lock()
	defer im.mu.Unlock()

	data, err := os.ReadFile(im.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new index
			return im.saveUnlocked()
		}
		return fmt.Errorf("failed to read index: %w", err)
	}

	if err := json.Unmarshal(data, &im.index); err != nil {
		return fmt.Errorf("failed to unmarshal index: %w", err)
	}

	// Initialize maps if nil
	if im.index.Scopes == nil {
		im.index.Scopes = make(map[string]*ScopeIndex)
	}

	return nil
}

// Save saves the index to disk.
func (im *IndexManager) Save() error {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.saveUnlocked()
}

// saveUnlocked saves without acquiring the lock (internal use).
func (im *IndexManager) saveUnlocked() error {
	im.index.LastUpdated = time.Now()

	data, err := json.MarshalIndent(im.index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(im.indexPath), 0755); err != nil {
		return fmt.Errorf("failed to create index directory: %w", err)
	}

	// Write atomically using temp file
	tempPath := im.indexPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp index: %w", err)
	}

	if err := os.Rename(tempPath, im.indexPath); err != nil {
		return fmt.Errorf("failed to rename temp index: %w", err)
	}

	return nil
}

// UpdateSession updates the index for a session.
func (im *IndexManager) UpdateSession(sessionID string, file string, hash string, factIDs []string) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	scope := im.getOrCreateScope("session")
	if scope.Sessions == nil {
		scope.Sessions = make(map[string]*SessionIndex)
	}

	oldCount := 0
	if existing, ok := scope.Sessions[sessionID]; ok {
		oldCount = len(existing.FactIDs)
	}

	scope.Sessions[sessionID] = &SessionIndex{
		File:         file,
		ChunkCount:   1,
		LastModified: time.Now(),
		Hash:         hash,
		FactIDs:      factIDs,
	}

	// Update counts
	scope.Count = scope.Count - oldCount + len(factIDs)
	im.recalculateTotalFacts()

	return im.saveUnlocked()
}

// UpdateUser updates the index for a user.
func (im *IndexManager) UpdateUser(userID string, file string, hash string, factIDs []string) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	scope := im.getOrCreateScope("user")
	if scope.Users == nil {
		scope.Users = make(map[string]*UserIndex)
	}

	oldCount := 0
	if existing, ok := scope.Users[userID]; ok {
		oldCount = len(existing.FactIDs)
	}

	scope.Users[userID] = &UserIndex{
		File:         file,
		ChunkCount:   1,
		LastModified: time.Now(),
		Hash:         hash,
		FactIDs:      factIDs,
	}

	// Update counts
	scope.Count = scope.Count - oldCount + len(factIDs)
	im.recalculateTotalFacts()

	return im.saveUnlocked()
}

// UpdateGlobal updates the index for global scope.
func (im *IndexManager) UpdateGlobal(file string, hash string, factIDs []string) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	scope := im.getOrCreateScope("global")
	oldCount := len(scope.FactIDs)

	scope.File = file
	scope.LastModified = time.Now()
	scope.Hash = hash
	scope.FactIDs = factIDs
	scope.Count = len(factIDs)

	// Update total
	im.index.TotalFacts = im.index.TotalFacts - oldCount + len(factIDs)

	return im.saveUnlocked()
}

// GetSessionFile returns the file path for a session.
func (im *IndexManager) GetSessionFile(sessionID string) (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	scope, ok := im.index.Scopes["session"]
	if !ok || scope.Sessions == nil {
		return "", false
	}

	session, ok := scope.Sessions[sessionID]
	if !ok {
		return "", false
	}

	return session.File, true
}

// GetUserFile returns the file path for a user.
func (im *IndexManager) GetUserFile(userID string) (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	scope, ok := im.index.Scopes["user"]
	if !ok || scope.Users == nil {
		return "", false
	}

	user, ok := scope.Users[userID]
	if !ok {
		return "", false
	}

	return user.File, true
}

// GetGlobalFile returns the file path for global scope.
func (im *IndexManager) GetGlobalFile() (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	scope, ok := im.index.Scopes["global"]
	if !ok || scope.File == "" {
		return "", false
	}

	return scope.File, true
}

// ListSessionIDs returns all session IDs.
func (im *IndexManager) ListSessionIDs() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()

	scope, ok := im.index.Scopes["session"]
	if !ok || scope.Sessions == nil {
		return nil
	}

	ids := make([]string, 0, len(scope.Sessions))
	for id := range scope.Sessions {
		ids = append(ids, id)
	}
	return ids
}

// ListUserIDs returns all user IDs.
func (im *IndexManager) ListUserIDs() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()

	scope, ok := im.index.Scopes["user"]
	if !ok || scope.Users == nil {
		return nil
	}

	ids := make([]string, 0, len(scope.Users))
	for id := range scope.Users {
		ids = append(ids, id)
	}
	return ids
}

// MarkEmbeddingsSynced marks embeddings as synced.
func (im *IndexManager) MarkEmbeddingsSynced(synced bool) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	im.index.EmbeddingsSynced = synced
	return im.saveUnlocked()
}

// getOrCreateScope returns or creates a scope index (must hold lock).
func (im *IndexManager) getOrCreateScope(scope string) *ScopeIndex {
	if im.index.Scopes[scope] == nil {
		im.index.Scopes[scope] = &ScopeIndex{
			Count:    0,
			Sessions: make(map[string]*SessionIndex),
			Users:    make(map[string]*UserIndex),
		}
	}
	return im.index.Scopes[scope]
}

// recalculateTotalFacts recalculates the total fact count (must hold lock).
func (im *IndexManager) recalculateTotalFacts() {
	total := 0
	for _, scope := range im.index.Scopes {
		total += scope.Count
	}
	im.index.TotalFacts = total
}
