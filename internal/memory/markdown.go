package memory

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ComputeHash returns a SHA256 hex digest of the input string.
func ComputeHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// MarkdownFrontmatter represents the YAML frontmatter of a facts.md file.
type MarkdownFrontmatter struct {
	Scope     string    `yaml:"scope"`
	UserID    string    `yaml:"user_id,omitempty"`
	SessionID string    `yaml:"session_id,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
	Version   int       `yaml:"version"`
}

// MarkdownFact represents a single fact in the markdown file.
type MarkdownFact struct {
	ID        string
	Category  string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt *time.Time
	Content   string
}

// MarkdownDocument represents a complete facts.md file.
type MarkdownDocument struct {
	Frontmatter MarkdownFrontmatter
	Facts       []MarkdownFact
}

// MarkdownParser handles parsing and serialization of facts.md files.
type MarkdownParser struct{}

// NewMarkdownParser creates a new MarkdownParser.
func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{}
}

// Parse parses a markdown document with YAML frontmatter and facts.
func (p *MarkdownParser) Parse(content []byte) (*MarkdownDocument, error) {
	doc := &MarkdownDocument{}

	// Split frontmatter and body
	parts := bytes.SplitN(content, []byte("---"), 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid markdown format: missing frontmatter delimiters")
	}

	// Parse frontmatter
	if err := yaml.Unmarshal(parts[1], &doc.Frontmatter); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Parse facts from body
	body := string(parts[2])
	facts, err := p.parseFacts(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse facts: %w", err)
	}
	doc.Facts = facts

	return doc, nil
}

// parseFacts extracts individual facts from the markdown body.
func (p *MarkdownParser) parseFacts(body string) ([]MarkdownFact, error) {
	var facts []MarkdownFact
	scanner := bufio.NewScanner(strings.NewReader(body))

	var currentFact *MarkdownFact
	var contentLines []string

	for scanner.Scan() {
		line := scanner.Text()

		// Check for fact header (## fact_id)
		if strings.HasPrefix(line, "## fact_") {
			// Save previous fact if exists
			if currentFact != nil {
				currentFact.Content = strings.TrimSpace(strings.Join(contentLines, "\n"))
				facts = append(facts, *currentFact)
				contentLines = nil
			}

			// Start new fact
			factID := strings.TrimPrefix(line, "## ")
			currentFact = &MarkdownFact{ID: factID}
			continue
		}

		// Parse metadata lines
		if currentFact != nil && strings.HasPrefix(line, "**") {
			if err := p.parseMetadataLine(line, currentFact); err != nil {
				return nil, err
			}
			continue
		}

		// Skip separator lines
		if strings.TrimSpace(line) == "---" {
			continue
		}

		// Collect content lines
		if currentFact != nil && strings.TrimSpace(line) != "" {
			contentLines = append(contentLines, line)
		}
	}

	// Save last fact
	if currentFact != nil {
		currentFact.Content = strings.TrimSpace(strings.Join(contentLines, "\n"))
		facts = append(facts, *currentFact)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	return facts, nil
}

// parseMetadataLine extracts metadata from a bold markdown line.
func (p *MarkdownParser) parseMetadataLine(line string, fact *MarkdownFact) error {
	// Format: **Key**: value
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "**:") {
		return nil // Skip malformed lines
	}

	parts := strings.SplitN(line, "**:", 2)
	if len(parts) != 2 {
		return nil
	}

	key := strings.TrimSpace(strings.TrimPrefix(parts[0], "**"))
	value := strings.TrimSpace(parts[1])

	switch key {
	case "Category":
		fact.Category = value
	case "Version":
		if _, err := fmt.Sscanf(value, "%d", &fact.Version); err != nil {
			return fmt.Errorf("parse Version from %q: %w", value, err)
		}
	case "Created":
		t, _ := time.Parse(time.RFC3339, value)
		fact.CreatedAt = t
	case "Updated":
		t, _ := time.Parse(time.RFC3339, value)
		fact.UpdatedAt = t
	case "Expires":
		if value != "null" && value != "" {
			t, _ := time.Parse(time.RFC3339, value)
			fact.ExpiresAt = &t
		}
	}

	return nil
}

// Serialize converts a MarkdownDocument to markdown bytes.
func (p *MarkdownParser) Serialize(doc *MarkdownDocument) ([]byte, error) {
	var buf bytes.Buffer

	// Write frontmatter
	buf.WriteString("---\n")
	frontmatterBytes, err := yaml.Marshal(&doc.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal frontmatter: %w", err)
	}
	buf.Write(frontmatterBytes)
	buf.WriteString("---\n\n")

	// Write title
	buf.WriteString("# Memory Facts\n\n")

	// Write facts
	for i, fact := range doc.Facts {
		if i > 0 {
			buf.WriteString("\n---\n\n")
		}

		_, _ = fmt.Fprintf(&buf, "## %s\n", fact.ID)
		_, _ = fmt.Fprintf(&buf, "**Category**: %s\n", fact.Category)
		_, _ = fmt.Fprintf(&buf, "**Version**: %d\n", fact.Version)
		_, _ = fmt.Fprintf(&buf, "**Created**: %s\n", fact.CreatedAt.Format(time.RFC3339))
		_, _ = fmt.Fprintf(&buf, "**Updated**: %s\n", fact.UpdatedAt.Format(time.RFC3339))

		if fact.ExpiresAt != nil {
			_, _ = fmt.Fprintf(&buf, "**Expires**: %s\n", fact.ExpiresAt.Format(time.RFC3339))
		} else {
			buf.WriteString("**Expires**: null\n")
		}

		buf.WriteString("\n")
		buf.WriteString(fact.Content)
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}
