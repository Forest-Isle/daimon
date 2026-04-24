package discord

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	cfg := Config{
		Token:          "test-token",
		AllowedUserIDs: []string{"123", "456"},
	}

	adapter := New(cfg)

	assert.Equal(t, "discord", adapter.Name())
	assert.Equal(t, "test-token", adapter.token)
	assert.True(t, adapter.allowedUserIDs["123"])
	assert.True(t, adapter.allowedUserIDs["456"])
	assert.False(t, adapter.allowedUserIDs["789"])
	assert.Equal(t, 120, adapter.approvalTimeoutSec)
}

func TestNew_EmptyAllowedUsers(t *testing.T) {
	adapter := New(Config{Token: "tok"})

	assert.Empty(t, adapter.allowedUserIDs)
}

func TestSetApprovalTimeout(t *testing.T) {
	adapter := New(Config{Token: "tok"})

	adapter.SetApprovalTimeout(60)
	assert.Equal(t, 60, adapter.approvalTimeoutSec)

	// Zero or negative should not change the value
	adapter.SetApprovalTimeout(0)
	assert.Equal(t, 60, adapter.approvalTimeoutSec)

	adapter.SetApprovalTimeout(-1)
	assert.Equal(t, 60, adapter.approvalTimeoutSec)
}

func TestStreamUpdater_RateLimit(t *testing.T) {
	// Create a streamUpdater with a mock-like setup.
	// We can't call Discord API, but we can verify the rate-limit logic
	// by checking that the lastAt / last fields gate updates correctly.
	u := &streamUpdater{
		// session is nil — we'll test the logic paths that don't reach the API
		last:   "previous",
		lastAt: time.Now(),
	}

	// Update within rate-limit window with existing last text should be skipped
	err := u.Update("new text")
	assert.NoError(t, err)
	// last should NOT have changed because the rate limit suppressed the call
	assert.Equal(t, "previous", u.last)
}

func TestStreamUpdater_SameContent(t *testing.T) {
	// When last update was long enough ago but content is same, skip
	u := &streamUpdater{
		last:   "same text",
		lastAt: time.Now().Add(-2 * time.Second),
	}

	err := u.Update("same text")
	assert.NoError(t, err)
	// No API call made because content is identical
	assert.Equal(t, "same text", u.last)
}

func TestStreamUpdater_ConcurrentRateLimit(t *testing.T) {
	// Set lastAt to now so all concurrent updates are within rate-limit window.
	// This verifies the mutex protects concurrent access without hitting the API.
	u := &streamUpdater{
		last:   "initial",
		lastAt: time.Now(),
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Rate limit will suppress all these — no API call, no panic
			_ = u.Update("concurrent text")
		}()
	}
	wg.Wait()
	// last should be unchanged because rate limit suppressed all updates
	assert.Equal(t, "initial", u.last)
}

func TestFormatForDiscord(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", "(empty response)"},
		{"short", "hello", "hello"},
		{"at limit", string(make([]byte, 1950)), string(make([]byte, 1950))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatForDiscord(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatForDiscord_Truncation(t *testing.T) {
	long := string(make([]byte, 2000))
	result := FormatForDiscord(long)
	require.True(t, len(result) < 2000)
	assert.Contains(t, result, "... (truncated)")
}

// Compile-time interface checks
var (
	_ interface{ Name() string } = (*Adapter)(nil)
)
