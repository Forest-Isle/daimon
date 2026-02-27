package skill

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Skill represents a loaded skill with eagerly-loaded metadata and lazily-loaded content.
type Skill struct {
	// Eagerly-loaded metadata (from YAML frontmatter)
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	Version     string    `yaml:"version"`
	Author      string    `yaml:"author"`
	Tags        []string  `yaml:"tags"`
	Metadata    SkillMeta `yaml:"metadata"`
	Path        string    // absolute path to SKILL.md

	// Lazily-loaded content (markdown body after frontmatter)
	content     string
	contentOnce sync.Once
	contentErr  error
}

// SkillMeta holds optional openclaw-specific metadata.
type SkillMeta struct {
	OpenClaw OpenClawMeta `yaml:"openclaw"`
}

// OpenClawMeta holds the openclaw integration metadata.
type OpenClawMeta struct {
	Requires   OpenClawRequires `yaml:"requires"`
	PrimaryEnv string           `yaml:"primaryEnv"`
}

// OpenClawRequires specifies environment variables and binaries required by the skill.
type OpenClawRequires struct {
	Env  []string `yaml:"env"`
	Bins []string `yaml:"bins"`
}

// ParseSkill reads a SKILL.md file (or flat .md file), parses the YAML frontmatter
// into the Skill struct metadata fields, and returns the skill with lazy content loading.
func ParseSkill(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: read %s: %w", path, err)
	}

	frontmatter, _, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("skill: parse frontmatter %s: %w", path, err)
	}

	var s Skill
	if err := yaml.Unmarshal(frontmatter, &s); err != nil {
		return nil, fmt.Errorf("skill: unmarshal yaml %s: %w", path, err)
	}
	s.Path = path

	return &s, nil
}

// Content lazily loads and returns the markdown body of the skill (everything after frontmatter).
func (s *Skill) Content() (string, error) {
	s.contentOnce.Do(func() {
		data, err := os.ReadFile(s.Path)
		if err != nil {
			s.contentErr = fmt.Errorf("skill: read content %s: %w", s.Path, err)
			return
		}
		_, body, err := splitFrontmatter(data)
		if err != nil {
			s.contentErr = fmt.Errorf("skill: parse content %s: %w", s.Path, err)
			return
		}
		s.content = string(bytes.TrimSpace(body))
	})
	return s.content, s.contentErr
}

// splitFrontmatter splits a SKILL.md file into YAML frontmatter and markdown body.
// Frontmatter is delimited by "---" lines. If no frontmatter is found, the entire
// content is treated as body with empty frontmatter.
func splitFrontmatter(data []byte) (frontmatter []byte, body []byte, err error) {
	const delim = "---"

	// Normalize line endings
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	// Must start with ---
	if !bytes.HasPrefix(data, []byte(delim+"\n")) && !bytes.Equal(bytes.TrimSpace(data[:min(3, len(data))]), []byte(delim)) {
		return nil, data, nil
	}

	// Find closing ---
	rest := data[len(delim)+1:] // skip opening ---\n
	idx := bytes.Index(rest, []byte("\n"+delim))
	if idx == -1 {
		return nil, data, nil
	}

	frontmatter = rest[:idx]
	body = rest[idx+len("\n"+delim):]

	// Trim leading newline from body
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}

	return frontmatter, body, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
