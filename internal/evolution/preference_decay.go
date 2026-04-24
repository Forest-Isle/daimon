package evolution

import (
	"math"
	"time"
)

// DecayPreferences applies time-based exponential decay to preference confidence.
// Preferences not reinforced within the decay window lose confidence; entries
// that drop below 0.05 are removed entirely. Returns the number of removed entries.
func (p *PreferenceLearner) DecayPreferences(now time.Time, halfLife time.Duration) int {
	if p == nil || halfLife <= 0 {
		return 0
	}

	decayed := 0
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, entry := range p.preferences {
		age := now.Sub(entry.LastSeen)
		if age <= 0 {
			continue
		}

		// Exponential decay: confidence *= 2^(-age/halfLife)
		factor := math.Pow(2.0, -float64(age)/float64(halfLife))
		newConf := entry.Confidence * factor

		if newConf < 0.05 {
			delete(p.preferences, key)
			decayed++
			continue
		}

		entry.Confidence = newConf
	}

	return decayed
}
