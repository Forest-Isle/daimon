package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed builtin/*/SKILL.md
var builtinSkills embed.FS

// Manager loads and manages skills from one or more directories.
type Manager struct {
	skills []*Skill
	mu     sync.RWMutex
}

// New creates a new skill Manager.
func New() *Manager {
	return &Manager{}
}

// LoadBuiltin loads embedded builtin skills from the binary.
// Builtin skills are skipped if a skill with the same name is already loaded.
func (m *Manager) LoadBuiltin() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.nameSet()

	entries, err := fs.ReadDir(builtinSkills, "builtin")
	if err != nil {
		return fmt.Errorf("skill: read builtin dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := "builtin/" + e.Name() + "/SKILL.md"
		data, err := builtinSkills.ReadFile(skillFile)
		if err != nil {
			slog.Warn("skill: failed to read builtin", "name", e.Name(), "err", err)
			continue
		}

		// Write to temp file for ParseSkill
		tmpDir, err := os.MkdirTemp("", "ironclaw-skill-*")
		if err != nil {
			continue
		}
		tmpPath := filepath.Join(tmpDir, "SKILL.md")
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			_ = os.RemoveAll(tmpDir)
			continue
		}

		s, err := ParseSkill(tmpPath)
		if err != nil {
			slog.Warn("skill: failed to parse builtin", "name", e.Name(), "err", err)
			_ = os.RemoveAll(tmpDir)
			continue
		}

		if s.Name == "" || existing[s.Name] {
			_ = os.RemoveAll(tmpDir)
			continue
		}

		existing[s.Name] = true
		m.skills = append(m.skills, s)
		slog.Info("skill: loaded builtin", "name", s.Name)
	}

	return nil
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
//
// Only skill metadata (name, version, description, tags) is included in the prompt.
// Full skill content is lazily loaded by the agent via the read_skill tool,
// following the progressive disclosure pattern (ref: langchain-ai/deepagents).
func (m *Manager) BuildPromptSection(userText string) string {
	selected := m.Select(userText)
	if len(selected) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Skills System\n\n")
	sb.WriteString("You have access to a skills library that provides specialized capabilities and domain knowledge.\n\n")
	sb.WriteString("**Available Skills:**\n\n")

	for _, s := range selected {
		version := s.Version
		if version == "" {
			version = "unknown"
		}
		fmt.Fprintf(&sb, "- **%s** (v%s)", s.Name, version)
		if s.Description != "" {
			fmt.Fprintf(&sb, ": %s", s.Description)
		}
		if len(s.Tags) > 0 {
			fmt.Fprintf(&sb, " [%s]", strings.Join(s.Tags, ", "))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n**How to Use Skills (Progressive Disclosure):**\n\n")
	sb.WriteString("Skills follow a progressive disclosure pattern — you see their name and description above, ")
	sb.WriteString("but only load full instructions when needed:\n\n")
	sb.WriteString("1. **Recognize when a skill applies**: Check if the user's task matches a skill's description\n")
	sb.WriteString("2. **Load the skill**: Call the `read_skill` tool with `{\"action\": \"read\", \"name\": \"<skill-name>\"}` to get full instructions\n")
	sb.WriteString("3. **Follow the skill's workflow**: The loaded instructions contain step-by-step workflows and best practices\n")
	sb.WriteString("4. **Access supporting files**: Skills may reference helper scripts or configs — use absolute paths\n\n")
	sb.WriteString("**Important**: Do NOT guess or improvise — always load the skill first. Skills make you more capable and consistent.\n")

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

// GetContent returns the lazily-loaded markdown body of a skill by name.
// This is the core API for progressive disclosure: metadata is in the prompt,
// full content is loaded only when the agent explicitly requests it.
func (m *Manager) GetContent(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.skills {
		if strings.EqualFold(s.Name, name) {
			return s.Content()
		}
	}
	return "", fmt.Errorf("skill not found: %s", name)
}

// ListNames returns the names of all loaded skills.
func (m *Manager) ListNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, len(m.skills))
	for i, s := range m.skills {
		names[i] = s.Name
	}
	return names
}

// nameSet returns a set of currently loaded skill names.
func (m *Manager) nameSet() map[string]bool {
	set := make(map[string]bool, len(m.skills))
	for _, s := range m.skills {
		set[s.Name] = true
	}
	return set
}
