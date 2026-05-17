package wasm

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PluginManifest describes a WASM plugin's metadata and requirements.
type PluginManifest struct {
	Name         string         `yaml:"name"`
	Version      string         `yaml:"version"`
	Description  string         `yaml:"description"`
	Author       string         `yaml:"author,omitempty"`
	License      string         `yaml:"license,omitempty"`
	Capabilities CapabilityDecl `yaml:"capabilities"`
	Interface    ToolInterface  `yaml:"interface"`
	Runtime      RuntimeConfig  `yaml:"runtime"`
}

// CapabilityDecl declares what permissions a plugin needs.
type CapabilityDecl struct {
	Network    *NetworkDecl    `yaml:"network,omitempty"`
	Filesystem *FilesystemDecl `yaml:"filesystem,omitempty"`
	Env        []string        `yaml:"env,omitempty"`
}

// NetworkDecl declares network access permissions.
type NetworkDecl struct {
	AllowHosts []string `yaml:"allow_hosts"`
	AllowPorts []int    `yaml:"allow_ports"`
	DenyHosts  []string `yaml:"deny_hosts"`
}

// FilesystemDecl declares filesystem access permissions.
type FilesystemDecl struct {
	Read  []string `yaml:"read"`
	Write []string `yaml:"write"`
}

// ToolInterface describes the tool's input/output schema.
type ToolInterface struct {
	Inputs  map[string]ParamSchema `yaml:"inputs"`
	Outputs map[string]ParamSchema `yaml:"outputs"`
}

// ParamSchema describes a single parameter.
type ParamSchema struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

// RuntimeConfig configures the WASM runtime for this plugin.
type RuntimeConfig struct {
	WasmFile      string `yaml:"wasm_file"`
	MemoryLimitMB int64  `yaml:"memory_limit_mb"`
	TimeoutMS     int64  `yaml:"timeout_ms"`
	MaxInstances  int    `yaml:"max_instances"`
}

// ParseManifest reads and validates a plugin.yaml file.
func ParseManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}
	m.applyDefaults()
	return &m, nil
}

// Validate checks required fields.
func (m *PluginManifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("version is required")
	}
	if m.Runtime.WasmFile == "" {
		return fmt.Errorf("runtime.wasm_file is required")
	}
	return nil
}

func (m *PluginManifest) applyDefaults() {
	if m.Runtime.MemoryLimitMB <= 0 {
		m.Runtime.MemoryLimitMB = 64
	}
	if m.Runtime.TimeoutMS <= 0 {
		m.Runtime.TimeoutMS = 30000
	}
	if m.Runtime.MaxInstances <= 0 {
		m.Runtime.MaxInstances = 4
	}
}
