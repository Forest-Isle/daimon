package eval

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadTaskSetYAML loads a []TaskCase from a YAML file.
// Supports the same fields as the JSON format via yaml struct tags.
func LoadTaskSetYAML(path string) ([]TaskCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read task file %q: %w", path, err)
	}
	var tasks []TaskCase
	if err := yaml.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("eval: parse YAML task file %q: %w", path, err)
	}
	return tasks, nil
}

// LoadTaskSetJSON loads a []TaskCase from a JSON file.
func LoadTaskSetJSON(path string) ([]TaskCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read task file %q: %w", path, err)
	}
	var tasks []TaskCase
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("eval: parse JSON task file %q: %w", path, err)
	}
	return tasks, nil
}
