package memory

import (
	"sync"
	"time"
)

// SectionBuffer accumulates facts for a single profile section and
// determines when to trigger an update based on count and time thresholds.
type SectionBuffer struct {
	section     ProfileSection
	mu          sync.Mutex
	pending     []string
	lastUpdated time.Time
}

// NewSectionBuffer creates a buffer for the given section.
func NewSectionBuffer(section ProfileSection) *SectionBuffer {
	return &SectionBuffer{
		section:     section,
		lastUpdated: time.Now(),
	}
}

// Add appends a fact to the pending buffer.
func (b *SectionBuffer) Add(fact string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, fact)
}

// ShouldUpdate returns true if the buffer has accumulated enough facts
// or enough time has passed since the last update.
func (b *SectionBuffer) ShouldUpdate() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.pending) == 0 {
		return false
	}
	if len(b.pending) >= b.section.FactThreshold {
		return true
	}
	if time.Since(b.lastUpdated) >= b.section.TimeThreshold {
		return true
	}
	return false
}

// Drain returns all pending facts and resets the buffer.
func (b *SectionBuffer) Drain() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]string, len(b.pending))
	copy(out, b.pending)
	b.pending = b.pending[:0]
	b.lastUpdated = time.Now()
	return out
}

// PendingCount returns the number of facts waiting in the buffer.
func (b *SectionBuffer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
