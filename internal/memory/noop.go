package memory

import "context"

// noopStore implements Store with all no-op methods.
// Used when memory feature is disabled.
type noopStore struct{}

func (noopStore) Save(_ context.Context, _ Entry) error                          { return nil }
func (noopStore) Search(_ context.Context, _ SearchQuery) ([]SearchResult, error) { return nil, nil }
func (noopStore) ListByScope(_ context.Context, _ MemoryScope, _ string) ([]Entry, error) {
	return nil, nil
}
func (noopStore) Update(_ context.Context, _ string, _ string, _ int) error { return nil }
func (noopStore) Delete(_ context.Context, _ string) error                  { return nil }

// NoopStore returns a Store that discards all operations.
func NoopStore() Store { return noopStore{} }

var _ Store = noopStore{}
