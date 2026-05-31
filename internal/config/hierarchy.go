package config

import (
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigLevel represents a configuration source priority level.
type ConfigLevel int

const (
	LevelSystem  ConfigLevel = iota // /etc/ironclaw/ — admin global rules
	LevelUser                       // ~/.ironclaw/config.yaml — user defaults
	LevelProject                    // .ironclaw/ironclaw.yaml — project-specific
	LevelLocal                      // .ironclaw/local.yaml — local override (gitignored)
)

func (l ConfigLevel) String() string {
	switch l {
	case LevelSystem:
		return "system"
	case LevelUser:
		return "user"
	case LevelProject:
		return "project"
	case LevelLocal:
		return "local"
	default:
		return "unknown"
	}
}

// HierarchySource represents a single config source with its level.
type HierarchySource struct {
	Level ConfigLevel
	Path  string
	Found bool
}

// LoadHierarchy loads configuration from all 4 levels and merges them.
// Priority: Local > Project > User > System
// Returns the merged config and the list of sources that were found.
func LoadHierarchy(workDir string) (*Config, []HierarchySource, error) {
	sources := discoverSources(workDir)

	// Start with defaults
	cfg := defaultConfig()

	// Apply each level in order (lowest priority first)
	for i := range sources {
		if !sources[i].Found {
			continue
		}
		slog.Info("config: loading level", "level", sources[i].Level.String(), "path", sources[i].Path)

		overlay, err := loadSingleYAML(sources[i].Path)
		if err != nil {
			slog.Warn("config: failed to load level", "level", sources[i].Level.String(), "err", err)
			continue
		}
		mergeConfig(&cfg, overlay)
	}

	return &cfg, sources, nil
}

// discoverSources finds config files at each level.
func discoverSources(workDir string) []HierarchySource {
	home, _ := os.UserHomeDir()

	sources := []HierarchySource{
		{Level: LevelSystem, Path: "/etc/ironclaw/config.yaml"},
		{Level: LevelUser, Path: filepath.Join(home, ".ironclaw", "config.yaml")},
		{Level: LevelProject, Path: filepath.Join(workDir, ".ironclaw", "ironclaw.yaml")},
		{Level: LevelLocal, Path: filepath.Join(workDir, ".ironclaw", "local.yaml")},
	}

	for i := range sources {
		if _, err := os.Stat(sources[i].Path); err == nil {
			sources[i].Found = true
		}
	}

	return sources
}

// loadSingleYAML reads and parses a single YAML config file.
func loadSingleYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := ExpandEnv(data)
	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// overlayHierarchy applies project-level and local-level configs on top of an
// already-loaded base config. Called by Load() to transparently support the
// hierarchical config without changing call sites.
func overlayHierarchy(base *Config, workDir string) {
	projectPath := filepath.Join(workDir, ".ironclaw", "ironclaw.yaml")
	localPath := filepath.Join(workDir, ".ironclaw", "local.yaml")

	for _, p := range []string{projectPath, localPath} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		overlay, err := loadSingleYAML(p)
		if err != nil {
			slog.Warn("config: failed to load overlay", "path", p, "err", err)
			continue
		}
		slog.Info("config: applying overlay", "path", p)
		mergeConfig(base, overlay)
	}
}
