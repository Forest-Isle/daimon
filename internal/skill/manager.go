package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Manager loads and manages skills from one or more directories.
type Manager struct {
	skills []*Skill
	mu     sync.RWMutex
}

// New creates a new skill Manager.
func New() *Manager {
	return &Manager{}
}

// LoadDir scans dir for skills (SKILL.md files in subdirectories or flat .md files),
// parses their frontmatter eagerly, and adds them to the manager.
// Skills whose name already exists are skipped (first-loaded wins).
func (m *Manager) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("skill: read dir %s: %w", dir, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.nameSet()

	for _, e := range entries {
		var skillPath string

		if e.IsDir() {
			// Look for SKILL.md inside the subdirectory
			candidate := filepath.Join(dir, e.Name(), "SKILL.md")
			if _, err := os.Stat(candidate); err == nil {
				skillPath = candidate
			}
		} else {
			name := e.Name()
			if strings.HasSuffix(name, ".md") {
				skillPath = filepath.Join(dir, name)
			}
		}

		if skillPath == "" {
			continue
		}

		s, err := ParseSkill(skillPath)
		if err != nil {
			slog.Warn("skill: failed to parse", "path", skillPath, "err", err)
			continue
		}

		if s.Name == "" {
			slog.Warn("skill: missing name field, skipping", "path", skillPath)
			continue
		}

		if existing[s.Name] {
			slog.Debug("skill: already loaded, skipping duplicate", "name", s.Name)
			continue
		}

		existing[s.Name] = true
		m.skills = append(m.skills, s)
		slog.Info("skill: loaded", "name", s.Name, "version", s.Version, "path", skillPath)
	}

	return nil
}

// All returns all loaded skills.
func (m *Manager) All() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Skill, len(m.skills))
	copy(result, m.skills)
	return result
}

// Select returns skills relevant to userText using keyword/tag matching.
// If no skills match, all skills are returned as fallback.
func (m *Manager) Select(userText string) []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.skills) == 0 {
		return nil
	}

	lower := strings.ToLower(userText)
	var matched []*Skill

	for _, s := range m.skills {
		if skillMatches(s, lower) {
			matched = append(matched, s)
		}
	}

	if len(matched) == 0 {
		// Fallback: include all skills
		result := make([]*Skill, len(m.skills))
		copy(result, m.skills)
		return result
	}

	return matched
}

// BuildPromptSection selects relevant skills and builds the system prompt section
// that will be injected after memories.
func (m *Manager) BuildPromptSection(userText string) string {
	selected := m.Select(userText)
	if len(selected) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Skills\n")

	for _, s := range selected {
		version := s.Version
		if version == "" {
			version = "unknown"
		}
		fmt.Fprintf(&sb, "\n### %s (v%s)\n", s.Name, version)
		if s.Description != "" {
			sb.WriteString(s.Description)
			sb.WriteString("\n")
		}

		content, err := s.Content()
		if err != nil {
			slog.Warn("skill: failed to load content", "name", s.Name, "err", err)
			continue
		}
		if content != "" {
			sb.WriteString("\n")
			sb.WriteString(content)
			sb.WriteString("\n")
		}

		sb.WriteString("\n---\n")
	}

	return sb.String()
}

// skillMatches returns true if the skill's name, description, or tags contain
// any word from the (already lowercased) userText.
func skillMatches(s *Skill, lowerText string) bool {
	// Check name
	if strings.Contains(lowerText, strings.ToLower(s.Name)) ||
		strings.Contains(strings.ToLower(s.Name), lowerText) {
		return true
	}

	// Check description words
	descWords := strings.Fields(strings.ToLower(s.Description))
	for _, word := range descWords {
		if len(word) > 3 && strings.Contains(lowerText, word) {
			return true
		}
	}

	// Check tags
	for _, tag := range s.Tags {
		if strings.Contains(lowerText, strings.ToLower(tag)) {
			return true
		}
	}

	return false
}

// nameSet returns a set of currently loaded skill names.
func (m *Manager) nameSet() map[string]bool {
	set := make(map[string]bool, len(m.skills))
	for _, s := range m.skills {
		set[s.Name] = true
	}
	return set
}
