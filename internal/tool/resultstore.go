package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/appdir"
)

// ResultStore handles disk persistence of large tool results.
type ResultStore struct {
	cacheDir       string
	thresholdBytes int
	previewChars   int
	ttlHours       int
}

// StoredResult represents a persisted tool result with an inline preview.
type StoredResult struct {
	Preview   string // truncated preview kept in context
	Reference string // stable in-context reference for this stored result
	DiskPath  string // path to full output on disk
	FullSize  int    // size of full output in bytes
}

// NewResultStore creates a new result store with the given configuration.
func NewResultStore(cacheDir string, thresholdBytes, previewChars, ttlHours int) *ResultStore {
	if cacheDir == "" {
		cacheDir = filepath.Join(appdir.BaseDir(), "cache", "tool-results")
	}
	return &ResultStore{
		cacheDir:       cacheDir,
		thresholdBytes: thresholdBytes,
		previewChars:   previewChars,
		ttlHours:       ttlHours,
	}
}

// ShouldPersist returns true if the output exceeds the size threshold.
func (rs *ResultStore) ShouldPersist(output string) bool {
	return len(output) > rs.thresholdBytes
}

// Store writes a large tool result to disk and returns a StoredResult with preview.
func (rs *ResultStore) Store(sessionID, toolUseID, output string) (*StoredResult, error) {
	dir := filepath.Join(rs.cacheDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	path := filepath.Join(dir, toolUseID+".txt")
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return nil, fmt.Errorf("write result: %w", err)
	}

	preview := TruncateAtLineBoundary(output, rs.previewChars)
	reference := fmt.Sprintf("tool-result:%s:%s", sessionID, toolUseID)
	return &StoredResult{
		Preview: preview + fmt.Sprintf(
			"\n\n[Tool result persisted]\nReference: %s\nFull output path: %s\nFull size: %d bytes\nUse file_read on the full output path if the omitted content is needed.",
			reference, path, len(output),
		),
		Reference: reference,
		DiskPath:  path,
		FullSize:  len(output),
	}, nil
}

// Load reads a persisted result from disk.
func (rs *ResultStore) Load(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read result: %w", err)
	}
	return string(data), nil
}

// Cleanup removes persisted results older than the configured TTL.
func (rs *ResultStore) Cleanup() error {
	if rs.ttlHours <= 0 {
		return nil
	}

	cutoff := time.Now().Add(-time.Duration(rs.ttlHours) * time.Hour)

	return filepath.Walk(rs.cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			// Remove empty session directories
			if path != rs.cacheDir {
				entries, readErr := os.ReadDir(path)
				if readErr == nil && len(entries) == 0 {
					_ = os.Remove(path)
				}
			}
			return nil
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

// TruncateAtLineBoundary truncates a string at the nearest line boundary
// at or before maxChars. Returns the original string if it's within the limit.
func TruncateAtLineBoundary(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}

	// Find the last newline at or before maxChars
	truncated := s[:maxChars]
	lastNewline := strings.LastIndex(truncated, "\n")
	if lastNewline > 0 {
		return s[:lastNewline]
	}

	// No newline found — truncate at maxChars
	return truncated
}
