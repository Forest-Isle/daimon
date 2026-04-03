package memory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryIndex represents the MEMORY.md index file.
type MemoryIndex struct {
	baseDir string
}

// IndexEntry represents a single entry in MEMORY.md.
type IndexEntry struct {
	Title    string
	FilePath string
	Scope    string
	Summary  string
}

// NewMemoryIndex creates a new memory index manager.
func NewMemoryIndex(baseDir string) *MemoryIndex {
	return &MemoryIndex{baseDir: baseDir}
}

// Rebuild scans all memory files and regenerates MEMORY.md.
func (mi *MemoryIndex) Rebuild() error {
	entries := make(map[string][]IndexEntry)

	scopes := []string{"user", "session", "feedback", "global"}
	for _, scope := range scopes {
		scopeDir := filepath.Join(mi.baseDir, scope)
		files, err := filepath.Glob(filepath.Join(scopeDir, "*.md"))
		if err != nil {
			continue
		}

		for _, file := range files {
			entry, err := mi.parseFileForIndex(file, scope)
			if err != nil {
				continue
			}
			entries[scope] = append(entries[scope], entry)
		}
	}

	return mi.writeIndex(entries)
}

// AddEntry adds a single entry to MEMORY.md.
func (mi *MemoryIndex) AddEntry(scope, filePath, title, summary string) error {
	indexPath := filepath.Join(mi.baseDir, "MEMORY.md")

	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	relPath, _ := filepath.Rel(mi.baseDir, filePath)
	line := fmt.Sprintf("- [%s](%s) — %s\n", title, relPath, summary)
	_, err = f.WriteString(line)
	return err
}

func (mi *MemoryIndex) parseFileForIndex(filePath, scope string) (IndexEntry, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return IndexEntry{}, err
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	title := filepath.Base(filePath)
	summary := ""

	inContent := false
	for _, line := range lines {
		if strings.HasPrefix(line, "---") {
			if inContent {
				break
			}
			inContent = true
			continue
		}
		if inContent && strings.TrimSpace(line) != "" {
			summary = strings.TrimSpace(line)
			if len(summary) > 80 {
				summary = summary[:77] + "..."
			}
			break
		}
	}

	relPath, _ := filepath.Rel(mi.baseDir, filePath)
	return IndexEntry{
		Title:    title,
		FilePath: relPath,
		Scope:    scope,
		Summary:  summary,
	}, nil
}

func (mi *MemoryIndex) writeIndex(entries map[string][]IndexEntry) error {
	indexPath := filepath.Join(mi.baseDir, "MEMORY.md")

	f, err := os.Create(indexPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()

	_, _ = fmt.Fprintf(w, "# Memory Index\n\n")
	_, _ = fmt.Fprintf(w, "Last updated: %s\n\n", time.Now().Format(time.RFC3339))

	scopeTitles := map[string]string{
		"user":     "User Memories",
		"session":  "Session Memories",
		"feedback": "Feedback Memories",
		"global":   "Global Memories",
	}

	for _, scope := range []string{"user", "feedback", "session", "global"} {
		if len(entries[scope]) == 0 {
			continue
		}

		_, _ = fmt.Fprintf(w, "## %s\n\n", scopeTitles[scope])

		sort.Slice(entries[scope], func(i, j int) bool {
			return entries[scope][i].FilePath < entries[scope][j].FilePath
		})

		for _, entry := range entries[scope] {
			_, _ = fmt.Fprintf(w, "- [%s](%s) — %s\n", entry.Title, entry.FilePath, entry.Summary)
		}
		_, _ = fmt.Fprintf(w, "\n")
	}

	return nil
}

// Parse reads MEMORY.md and returns entries for quick filtering.
func (mi *MemoryIndex) Parse() (map[string][]IndexEntry, error) {
	indexPath := filepath.Join(mi.baseDir, "MEMORY.md")

	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string][]IndexEntry), nil
		}
		return nil, err
	}

	entries := make(map[string][]IndexEntry)
	currentScope := ""

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			title := strings.TrimPrefix(line, "## ")
			switch title {
			case "User Memories":
				currentScope = "user"
			case "Session Memories":
				currentScope = "session"
			case "Feedback Memories":
				currentScope = "feedback"
			case "Global Memories":
				currentScope = "global"
			}
			continue
		}

		if strings.HasPrefix(line, "- [") && currentScope != "" {
			entry := mi.parseIndexLine(line, currentScope)
			entries[currentScope] = append(entries[currentScope], entry)
		}
	}

	return entries, nil
}

func (mi *MemoryIndex) parseIndexLine(line, scope string) IndexEntry {
	start := strings.Index(line, "[")
	end := strings.Index(line, "]")
	pathStart := strings.Index(line, "(")
	pathEnd := strings.Index(line, ")")
	summaryStart := strings.Index(line, "—")

	entry := IndexEntry{Scope: scope}

	if start >= 0 && end > start {
		entry.Title = line[start+1 : end]
	}
	if pathStart >= 0 && pathEnd > pathStart {
		entry.FilePath = line[pathStart+1 : pathEnd]
	}
	if summaryStart >= 0 && summaryStart < len(line)-2 {
		entry.Summary = strings.TrimSpace(line[summaryStart+1:])
	}

	return entry
}
