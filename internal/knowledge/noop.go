package knowledge

import "context"

// noopSearcher implements Searcher with a no-op Search method.
// Used when the knowledge feature is disabled.
type noopSearcher struct{}

func (noopSearcher) Search(_ context.Context, _ KnowledgeQuery) ([]KnowledgeResult, error) {
	return nil, nil
}

// NoopSearcher returns a Searcher that returns empty results.
func NoopSearcher() Searcher { return noopSearcher{} }

var _ Searcher = noopSearcher{}
