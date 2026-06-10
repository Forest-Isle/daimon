package agent

import (
	"context"

	"github.com/Forest-Isle/IronClaw/internal/session"
)

// noopContextManager implements ContextManager with no compression.
type noopContextManager struct{}

func (noopContextManager) Compress(_ context.Context, _ *session.Session, _ string) (bool, error) {
	return false, nil
}
func (noopContextManager) ReactiveCompress(_ context.Context, _ *session.Session, _ string) error {
	return nil
}
func (noopContextManager) Utilization(_ *session.Session, _ string) float64 { return 0 }
func (noopContextManager) SplitSystemPrompt(full string) (string, string)   { return full, "" }

// Compile-time interface checks
var _ ContextManager = noopContextManager{}
