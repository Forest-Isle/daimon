package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ChunkMetadata represents metadata for a chunked facts file.
type ChunkMetadata struct {
	Scope        string       `json:"scope"`
	UserID       string       `json:"user_id,omitempty"`
	SessionID    string       `json:"session_id,omitempty"`
	TotalFacts   int          `json:"total_facts"`
	TotalChunks  int          `json:"total_chunks"`
	Chunks       []ChunkInfo  `json:"chunks"`
	LastModified time.Time    `json:"last_modified"`
	Version      int          `json:"version"`
}

// ChunkInfo represents information about a single chunk file.
type ChunkInfo struct {
	ChunkID    string    `json:"chunk_id"`
	File       string    `json:"file"`
	FactRange  []string  `json:"fact_range"` // [first_fact_id, last_fact_id]
	FactCount  int       `json:"fact_count"`
	SizeBytes  int       `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// MetadataManager manages metadata.json files for chunked storage.
type MetadataManager struct {
	storageDir string
}

// NewMetadataManager creates a new MetadataManager.
func NewMetadataManager(storageDir string) *MetadataManager {
	return &MetadataManager{
		storageDir: storageDir,
	}
}

// Load loads metadata from a directory.
func (mm *MetadataManager) Load(dir string) (*ChunkMetadata, error) {
	metadataPath := filepath.Join(dir, "metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty metadata if file doesn't exist
			return &ChunkMetadata{
				Chunks:  []ChunkInfo{},
				Version: 1,
			}, nil
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata ChunkMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &metadata, nil
}

// Save saves metadata to a directory.
func (mm *MetadataManager) Save(dir string, metadata *ChunkMetadata) error {
	metadataPath := filepath.Join(dir, "metadata.json")

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	metadata.LastModified = time.Now()

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Write atomically using temp file
	tempPath := metadataPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp metadata: %w", err)
	}

	if err := os.Rename(tempPath, metadataPath); err != nil {
		return fmt.Errorf("failed to rename temp metadata: %w", err)
	}

	return nil
}

// GetChunkDir returns the chunks directory for a scope.
func (mm *MetadataManager) GetChunkDir(scope MemoryScope, userID, sessionID string) (string, error) {
	switch scope {
	case ScopeSession:
		if sessionID == "" {
			return "", fmt.Errorf("session_id required for session scope")
		}
		return filepath.Join(mm.storageDir, "session", sessionID, "chunks"), nil
	case ScopeUser:
		if userID == "" {
			return "", fmt.Errorf("user_id required for user scope")
		}
		return filepath.Join(mm.storageDir, "user", userID, "chunks"), nil
	case ScopeGlobal:
		return filepath.Join(mm.storageDir, "global", "chunks"), nil
	default:
		return "", fmt.Errorf("unknown scope: %s", scope)
	}
}

// AddChunk adds a new chunk to the metadata.
func (mm *MetadataManager) AddChunk(metadata *ChunkMetadata, chunkInfo ChunkInfo) {
	metadata.Chunks = append(metadata.Chunks, chunkInfo)
	metadata.TotalChunks = len(metadata.Chunks)
	metadata.TotalFacts += chunkInfo.FactCount
	metadata.Version++
}

// UpdateChunk updates an existing chunk in the metadata.
func (mm *MetadataManager) UpdateChunk(metadata *ChunkMetadata, chunkID string, chunkInfo ChunkInfo) error {
	for i, chunk := range metadata.Chunks {
		if chunk.ChunkID == chunkID {
			oldCount := chunk.FactCount
			metadata.Chunks[i] = chunkInfo
			metadata.TotalFacts = metadata.TotalFacts - oldCount + chunkInfo.FactCount
			metadata.Version++
			return nil
		}
	}
	return fmt.Errorf("chunk not found: %s", chunkID)
}

// RemoveChunk removes a chunk from the metadata.
func (mm *MetadataManager) RemoveChunk(metadata *ChunkMetadata, chunkID string) error {
	for i, chunk := range metadata.Chunks {
		if chunk.ChunkID == chunkID {
			metadata.TotalFacts -= chunk.FactCount
			metadata.Chunks = append(metadata.Chunks[:i], metadata.Chunks[i+1:]...)
			metadata.TotalChunks = len(metadata.Chunks)
			metadata.Version++
			return nil
		}
	}
	return fmt.Errorf("chunk not found: %s", chunkID)
}

// GetAllChunkFiles returns all chunk file paths.
func (mm *MetadataManager) GetAllChunkFiles(metadata *ChunkMetadata) []string {
	var files []string
	for _, chunk := range metadata.Chunks {
		files = append(files, chunk.File)
	}
	return files
}
