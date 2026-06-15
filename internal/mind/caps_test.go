package mind

import (
	"testing"

	"github.com/Forest-Isle/daimon/internal/config"
)

func TestClaudeCapabilitiesCacheBreakpoint(t *testing.T) {
	// A caching-capable Claude model offers one caller-placed cache breakpoint.
	caching := NewClaudeProvider("k", "claude-sonnet-4-6", "")
	if got := caching.Capabilities().CacheBreakpoints; got != 1 {
		t.Fatalf("caching Claude model CacheBreakpoints = %d, want 1", got)
	}
	// A model the caching heuristic does not recognize offers none.
	noncaching := NewClaudeProvider("k", "some-legacy-model", "")
	if got := noncaching.Capabilities().CacheBreakpoints; got != 0 {
		t.Fatalf("non-caching Claude model CacheBreakpoints = %d, want 0", got)
	}
}

func TestOpenAICapabilitiesNoBreakpoint(t *testing.T) {
	p := NewOpenAIProvider("k", "gpt-4o", "")
	if got := p.Capabilities().CacheBreakpoints; got != 0 {
		t.Fatalf("OpenAI CacheBreakpoints = %d, want 0 (caching is automatic)", got)
	}
}

func TestRetryProviderForwardsCapabilities(t *testing.T) {
	inner := &mockProvider{caps: Caps{CacheBreakpoints: 3}}
	r := NewRetryProvider(inner, config.RetryConfig{MaxRetries: 1})
	if got := r.Capabilities().CacheBreakpoints; got != 3 {
		t.Fatalf("RetryProvider.Capabilities did not forward inner: got %d, want 3", got)
	}
}
