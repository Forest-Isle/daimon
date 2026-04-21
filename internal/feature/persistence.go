package feature

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// featureStateFile is the default filename for persisted runtime overrides.
const featureStateFile = "feature_state.json"

// DefaultStatePath returns the default path for persisting feature state.
// Creates parent directories if they don't exist.
func DefaultStatePath(baseDir string) string {
	return filepath.Join(baseDir, featureStateFile)
}

// LoadOverrides reads persisted runtime overrides from a JSON file.
// Returns an empty map (not an error) if the file doesn't exist yet.
func LoadOverrides(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("feature: read state file: %w", err)
	}

	var overrides map[string]bool
	if err := json.Unmarshal(data, &overrides); err != nil {
		return nil, fmt.Errorf("feature: parse state file %q: %w", path, err)
	}
	return overrides, nil
}

// SaveOverrides writes the provided overrides map to a JSON file atomically
// (write to a temp file, rename on success).
func SaveOverrides(path string, overrides map[string]bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("feature: create state dir: %w", err)
	}

	data, err := json.MarshalIndent(overrides, "", "  ")
	if err != nil {
		return fmt.Errorf("feature: marshal state: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return fmt.Errorf("feature: write temp state file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("feature: rename state file: %w", err)
	}
	return nil
}

// RuntimeOverrides returns a map of feature name → enabled for every feature
// whose current enabled state differs from its default (or explicit config override).
// Use this to build the payload for SaveOverrides.
func (r *Registry) RuntimeOverrides() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]bool)
	for name, st := range r.states {
		result[name] = st.enabled
	}
	return result
}
