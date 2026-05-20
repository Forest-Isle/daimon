package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileMemory persists the conversation log as newline-delimited JSON (NDJSON)
// in a single file. Each line is one core.Message marshalled as JSON. The
// format is human-readable with `cat` or `tail` and trivially replayable.
//
// FileMemory is safe for concurrent use.
type FileMemory struct {
	mu   sync.Mutex
	path string
	log  []Message
}

// NewFileMemory opens or creates path. Existing messages are loaded into
// the in-memory snapshot. Write errors on Append cause the operation to
// fail — the caller should decide whether to continue or abort.
func NewFileMemory(path string) (*FileMemory, error) {
	fm := &FileMemory{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("core.FileMemory: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("core.FileMemory: read %s: %w", path, err)
		}
		return fm, nil
	}
	if len(data) == 0 {
		return fm, nil
	}
	// NDJSON: one JSON object per line.
	lines := splitLines(string(data))
	for _, line := range lines {
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			// Skip corrupted lines; a real system would log.
			continue
		}
		fm.log = append(fm.log, m)
	}
	return fm, nil
}

// Append writes the message to disk immediately (sync write).
func (f *FileMemory) Append(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("core.FileMemory: marshal: %w", err)
	}
	b = append(b, '\n')

	file, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("core.FileMemory: open: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(b); err != nil {
		return fmt.Errorf("core.FileMemory: write: %w", err)
	}
	f.log = append(f.log, m)
	return nil
}

// Snapshot returns a copy of the full conversation.
func (f *FileMemory) Snapshot(_ context.Context) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.log))
	copy(out, f.log)
	return out, nil
}

// Stats returns basic metrics about the stored conversation.
func (f *FileMemory) Stats() (msgCount, userTurns, toolCalls int, since time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.log {
		msgCount++
		switch m.Role {
		case RoleUser:
			userTurns++
		case RoleTool:
			toolCalls++
		}
	}
	if len(f.log) > 0 {
		_ = since // not available from Message
	}
	return
}

// Path returns the file backing this memory.
func (f *FileMemory) Path() string { return f.path }

// splitLines is a simple line splitter that avoids importing bufio.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		line := s[start:]
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
