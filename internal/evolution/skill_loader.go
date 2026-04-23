package evolution

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadLevel defines how much skill content is loaded.
type LoadLevel int

const (
	// LoadIndex loads only name + description + trigger (Level 1).
	LoadIndex LoadLevel = iota
	// LoadPartial loads index + key steps (Level 2).
	LoadPartial
	// LoadFull loads the complete skill content (Level 3).
	LoadFull
)

// SkillIndex represents Level 1 metadata for an active skill.
type SkillIndex struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Trigger     ActivationCondition `yaml:"trigger"`
}

// ActivationCondition defines when a skill should be activated.
type ActivationCondition struct {
	Keywords    []string `yaml:"keywords"`
	ToolPattern []string `yaml:"tool_pattern"`
	Complexity  string   `yaml:"complexity"` // "simple" | "moderate" | "complex"
	MinMatch    float64  `yaml:"min_match"`  // 0.0-1.0
}

// SkillLoader manages progressive loading of skills from a directory.
type SkillLoader struct {
	indexDir string // directory containing skill .md files
}

// NewSkillLoader creates a loader that reads skills from indexDir.
func NewSkillLoader(indexDir string) *SkillLoader {
	return &SkillLoader{indexDir: indexDir}
}

// LoadIndex reads all .md files in indexDir and returns Level 1 metadata only.
func (l *SkillLoader) LoadIndex() ([]SkillIndex, error) {
	entries, err := os.ReadDir(l.indexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("skill_loader: read dir: %w", err)
	}

	var index []SkillIndex
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(l.indexDir, e.Name())
		si, err := parseSkillIndex(path)
		if err != nil {
			continue // skip unparseable files
		}
		if si.Name == "" {
			continue
		}
		index = append(index, si)
	}
	return index, nil
}

// LoadPartial returns Level 2 content: frontmatter + the first markdown section
// (up to the second "## " heading or 500 characters, whichever comes first).
func (l *SkillLoader) LoadPartial(name string) (string, error) {
	path, err := l.findSkillFile(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("skill_loader: read %s: %w", path, err)
	}

	_, body := splitSkillFrontmatter(data)
	bodyStr := string(bytes.TrimSpace(body))

	// Find second "## " heading to cut off
	lines := strings.Split(bodyStr, "\n")
	var result []string
	headingCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			headingCount++
			if headingCount > 1 {
				break
			}
		}
		result = append(result, line)
	}

	partial := strings.Join(result, "\n")
	if len(partial) > 500 {
		partial = partial[:500] + "\n..."
	}
	return partial, nil
}

// LoadFull returns Level 3: the complete markdown body after frontmatter.
func (l *SkillLoader) LoadFull(name string) (string, error) {
	path, err := l.findSkillFile(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("skill_loader: read %s: %w", path, err)
	}

	_, body := splitSkillFrontmatter(data)
	return string(bytes.TrimSpace(body)), nil
}

// MatchSkills returns skill indices whose keywords match the given goal text.
// Matching is case-insensitive. If complexity is non-empty, skills with a
// matching complexity level are preferred.
func (l *SkillLoader) MatchSkills(goal string, complexity string) []SkillIndex {
	index, err := l.LoadIndex()
	if err != nil || len(index) == 0 {
		return nil
	}

	goalLower := strings.ToLower(goal)
	var matched []SkillIndex

	for _, si := range index {
		if matchesKeywords(goalLower, si.Trigger.Keywords) {
			// If complexity filter is set, prefer matching skills
			if complexity != "" && si.Trigger.Complexity != "" &&
				si.Trigger.Complexity != complexity {
				continue
			}
			matched = append(matched, si)
		}
	}
	return matched
}

// findSkillFile locates the .md file for the given skill name.
func (l *SkillLoader) findSkillFile(name string) (string, error) {
	entries, err := os.ReadDir(l.indexDir)
	if err != nil {
		return "", fmt.Errorf("skill_loader: read dir: %w", err)
	}

	nameLower := strings.ToLower(name)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(l.indexDir, e.Name())
		si, err := parseSkillIndex(path)
		if err != nil {
			continue
		}
		if strings.ToLower(si.Name) == nameLower {
			return path, nil
		}
	}
	return "", fmt.Errorf("skill_loader: skill not found: %s", name)
}

// parseSkillIndex extracts Level 1 metadata from a skill .md file's YAML frontmatter.
func parseSkillIndex(path string) (SkillIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillIndex{}, err
	}

	fm, _ := splitSkillFrontmatter(data)
	if fm == nil {
		return SkillIndex{}, fmt.Errorf("no frontmatter")
	}

	var si SkillIndex
	if err := yaml.Unmarshal(fm, &si); err != nil {
		return SkillIndex{}, err
	}
	return si, nil
}

// splitSkillFrontmatter splits a markdown file into YAML frontmatter and body.
// Returns (nil, full data) if no frontmatter delimiters are found.
func splitSkillFrontmatter(data []byte) (frontmatter []byte, body []byte) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, data
	}

	rest := data[4:] // skip "---\n"
	idx := bytes.Index(rest, []byte("\n---"))
	if idx == -1 {
		return nil, data
	}

	frontmatter = rest[:idx]
	body = rest[idx+4:] // skip "\n---"
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return frontmatter, body
}

// matchesKeywords checks if any keyword appears in the goal text.
func matchesKeywords(goalLower string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(goalLower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
