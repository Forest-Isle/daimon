package memory

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogOperation represents a type of operation in the transaction log.
type LogOperation string

const (
	LogOpAdd    LogOperation = "add"
	LogOpUpdate LogOperation = "update"
	LogOpDelete LogOperation = "delete"
)

// LogEntry represents a single entry in the transaction log.
type LogEntry struct {
	Timestamp time.Time    `json:"timestamp"`
	Operation LogOperation `json:"operation"`
	FactID    string       `json:"fact_id"`
	FilePath  string       `json:"file_path"`
	Content   string       `json:"content"`
	Hash      string       `json:"hash"`
}

// TransactionLog manages the pending.log file for batch processing.
type TransactionLog struct {
	logPath       string
	embedder      EmbeddingProvider
	embeddingsDB  EmbeddingsDB
	flushInterval time.Duration
	mu            sync.Mutex
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// EmbeddingsDB is an interface for managing embeddings in the database.
type EmbeddingsDB interface {
	SaveEmbedding(ctx context.Context, factID, filePath, scope, userID, sessionID, contentHash string, embedding []float32, version int, expiresAt *time.Time) error
	DeleteEmbedding(ctx context.Context, factID string) error
	UpdateFTS5Content(ctx context.Context, factID, content string) error
}

// NewTransactionLog creates a new TransactionLog.
func NewTransactionLog(storageDir string, embedder EmbeddingProvider, embeddingsDB EmbeddingsDB, flushInterval time.Duration) *TransactionLog {
	syncDir := filepath.Join(storageDir, ".sync")
	os.MkdirAll(syncDir, 0755)

	return &TransactionLog{
		logPath:       filepath.Join(syncDir, "pending.log"),
		embedder:      embedder,
		embeddingsDB:  embeddingsDB,
		flushInterval: flushInterval,
		stopCh:        make(chan struct{}),
	}
}

// Append adds a new entry to the transaction log.
func (tl *TransactionLog) Append(entry LogEntry) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(tl.logPath), 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Open log file in append mode
	f, err := os.OpenFile(tl.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	// Write JSON line
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write log entry: %w", err)
	}

	return nil
}

// StartBackgroundProcessor starts the background task that processes pending logs.
func (tl *TransactionLog) StartBackgroundProcessor(ctx context.Context) {
	tl.wg.Add(1)
	go func() {
		defer tl.wg.Done()
		ticker := time.NewTicker(tl.flushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("transaction log: stopping background processor")
				return
			case <-tl.stopCh:
				slog.Info("transaction log: stopping background processor")
				return
			case <-ticker.C:
				if err := tl.Flush(ctx); err != nil {
					slog.Error("transaction log: flush failed", "err", err)
				}
			}
		}
	}()
}

// Stop stops the background processor.
func (tl *TransactionLog) Stop() {
	close(tl.stopCh)
	tl.wg.Wait()
}

// Flush processes all pending log entries and clears the log.
func (tl *TransactionLog) Flush(ctx context.Context) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	// Check if log file exists
	if _, err := os.Stat(tl.logPath); os.IsNotExist(err) {
		return nil // Nothing to flush
	}

	// Read all entries
	entries, err := tl.readEntriesUnlocked()
	if err != nil {
		return fmt.Errorf("failed to read log entries: %w", err)
	}

	if len(entries) == 0 {
		return nil
	}

	slog.Info("transaction log: flushing entries", "count", len(entries))

	// Process entries in batches
	batchSize := 50
	for i := 0; i < len(entries); i += batchSize {
		end := i + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[i:end]

		if err := tl.processBatch(ctx, batch); err != nil {
			slog.Error("transaction log: batch processing failed", "err", err, "batch_start", i)
			// Continue processing remaining batches
		}
	}

	// Clear the log file
	if err := os.Truncate(tl.logPath, 0); err != nil {
		return fmt.Errorf("failed to truncate log file: %w", err)
	}

	slog.Info("transaction log: flush completed", "processed", len(entries))
	return nil
}

// readEntriesUnlocked reads all entries from the log file (must hold lock).
func (tl *TransactionLog) readEntriesUnlocked() ([]LogEntry, error) {
	f, err := os.Open(tl.logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry LogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			slog.Warn("transaction log: skipping malformed entry", "err", err)
			continue
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	return entries, nil
}

// processBatch processes a batch of log entries.
func (tl *TransactionLog) processBatch(ctx context.Context, entries []LogEntry) error {
	// Collect contents for batch embedding generation
	var contents []string
	var validEntries []LogEntry

	for _, entry := range entries {
		if entry.Operation == LogOpDelete {
			// Handle delete immediately
			if err := tl.embeddingsDB.DeleteEmbedding(ctx, entry.FactID); err != nil {
				slog.Error("transaction log: failed to delete embedding", "fact_id", entry.FactID, "err", err)
			}
			continue
		}

		contents = append(contents, entry.Content)
		validEntries = append(validEntries, entry)
	}

	if len(contents) == 0 {
		return nil
	}

	// Generate embeddings in batch
	embeddings, err := tl.embedder.EmbedBatch(ctx, contents)
	if err != nil {
		return fmt.Errorf("failed to generate batch embeddings: %w", err)
	}

	if len(embeddings) != len(validEntries) {
		return fmt.Errorf("embedding count mismatch: got %d, expected %d", len(embeddings), len(validEntries))
	}

	// Save embeddings to database
	for i, entry := range validEntries {
		// Extract scope info from file path
		scope, userID, sessionID := extractScopeFromPath(entry.FilePath)

		if err := tl.embeddingsDB.SaveEmbedding(
			ctx,
			entry.FactID,
			entry.FilePath,
			scope,
			userID,
			sessionID,
			entry.Hash,
			embeddings[i],
			1, // version
			nil, // expiresAt
		); err != nil {
			slog.Error("transaction log: failed to save embedding", "fact_id", entry.FactID, "err", err)
			continue
		}

		// Update FTS5 content
		if err := tl.embeddingsDB.UpdateFTS5Content(ctx, entry.FactID, entry.Content); err != nil {
			slog.Error("transaction log: failed to update FTS5 content", "fact_id", entry.FactID, "err", err)
		}
	}

	return nil
}

// extractScopeFromPath extracts scope, userID, and sessionID from file path.
func extractScopeFromPath(filePath string) (scope, userID, sessionID string) {
	// Expected format: .../session/{session_id}/facts.md or .../user/{user_id}/facts.md
	parts := strings.Split(filepath.ToSlash(filePath), "/")
	if len(parts) < 2 {
		return "global", "", ""
	}

	for i, part := range parts {
		switch part {
		case "session":
			if i+1 < len(parts) {
				return "session", "", parts[i+1]
			}
		case "user":
			if i+1 < len(parts) {
				return "user", parts[i+1], ""
			}
		case "global":
			return "global", "", ""
		}
	}

	return "global", "", ""
}

// ComputeHash computes SHA256 hash of content.
func ComputeHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}
