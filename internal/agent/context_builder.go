package agent

import (
	"context"
	"fmt"
	"strings"
)

// ContextFragment is a single piece of dynamic context from a scanner.
type ContextFragment struct {
	Source   string
	Content  string
	Priority int
}

// ContextScanner gathers dynamic context from a specific source (project, git, memory, etc.).
type ContextScanner interface {
	Name() string
	Scan(ctx context.Context) (*ContextFragment, error)
}

// ContextBuilder aggregates dynamic context from multiple scanners for prompt injection.
type ContextBuilder struct {
	scanners []ContextScanner
}

// NewContextBuilder creates a ContextBuilder with the given scanners.
func NewContextBuilder(scanners ...ContextScanner) *ContextBuilder {
	return &ContextBuilder{scanners: scanners}
}

// AddScanner appends a scanner to the builder.
func (cb *ContextBuilder) AddScanner(s ContextScanner) {
	cb.scanners = append(cb.scanners, s)
}

// Build runs all scanners and combines their output into a single string.
// Scanners that fail are reported as "[name: unavailable]" rather than blocking.
func (cb *ContextBuilder) Build(ctx context.Context) string {
	var parts []string
	for _, s := range cb.scanners {
		fragment, err := s.Scan(ctx)
		if err != nil {
			parts = append(parts, fmt.Sprintf("[%s: unavailable]", s.Name()))
			continue
		}
		if fragment != nil && fragment.Content != "" {
			parts = append(parts, fragment.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}
