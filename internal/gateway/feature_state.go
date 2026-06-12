package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Forest-Isle/daimon/internal/appdir"
)

func defaultFeatureStatePath() string {
	return filepath.Join(appdir.BaseDir(), "feature_state.json")
}

func loadFeatureState(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read feature state: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	state := make(map[string]bool)
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse feature state: %w", err)
	}
	return state, nil
}

func saveFeatureState(path string, state map[string]bool) error {
	if path == "" {
		return fmt.Errorf("feature state path is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create feature state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal feature state: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write feature state: %w", err)
	}
	return nil
}
