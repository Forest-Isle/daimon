package wasm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/tetratelabs/wazero"
)

// PluginHost manages the lifecycle of WASM plugins using the wazero runtime.
type PluginHost struct {
	runtime wazero.Runtime
	plugins map[string]*LoadedPlugin
	mu      sync.RWMutex
}

// LoadedPlugin represents a loaded and compiled WASM plugin.
type LoadedPlugin struct {
	Manifest *PluginManifest
	Module   wazero.CompiledModule
	Caps     *CapabilitySet
	Pool     *InstancePool
}

// NewPluginHost creates a new plugin host with a fresh wazero runtime.
func NewPluginHost(ctx context.Context) *PluginHost {
	rt := wazero.NewRuntime(ctx)
	_ = ensureWASI(ctx, rt) // best-effort; tools that don't need WASI still work
	return &PluginHost{
		runtime: rt,
		plugins: make(map[string]*LoadedPlugin),
	}
}

// LoadPlugin loads a plugin from its YAML manifest path.
func (h *PluginHost) LoadPlugin(ctx context.Context, manifestPath string) (*LoadedPlugin, error) {
	manifest, err := ParseManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	wasmPath := manifest.Runtime.WasmFile
	if !filepath.IsAbs(wasmPath) {
		wasmPath = filepath.Join(filepath.Dir(manifestPath), wasmPath)
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read wasm: %w", err)
	}

	module, err := h.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile wasm: %w", err)
	}

	caps := BuildCapabilitySet(manifest.Capabilities, manifest.Runtime)

	pool, err := NewInstancePool(ctx, h.runtime, module, caps, manifest.Runtime.MaxInstances)
	if err != nil {
		module.Close(ctx)
		return nil, fmt.Errorf("create pool: %w", err)
	}

	lp := &LoadedPlugin{
		Manifest: manifest,
		Module:   module,
		Caps:     caps,
		Pool:     pool,
	}

	h.mu.Lock()
	h.plugins[manifest.Name] = lp
	h.mu.Unlock()

	slog.Info("wasm: plugin loaded", "name", manifest.Name, "version", manifest.Version)
	return lp, nil
}

// UnloadPlugin unloads a plugin by name.
func (h *PluginHost) UnloadPlugin(ctx context.Context, name string) {
	h.mu.Lock()
	lp, ok := h.plugins[name]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.plugins, name)
	h.mu.Unlock()

	lp.Pool.Close(ctx)
	lp.Module.Close(ctx)
	slog.Info("wasm: plugin unloaded", "name", name)
}

// GetPlugin returns a loaded plugin by name, or nil.
func (h *PluginHost) GetPlugin(name string) *LoadedPlugin {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.plugins[name]
}

// ListPlugins returns all loaded plugin names.
func (h *PluginHost) ListPlugins() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.plugins))
	for name := range h.plugins {
		names = append(names, name)
	}
	return names
}

// Close shuts down the wazero runtime and all plugins.
func (h *PluginHost) Close(ctx context.Context) error {
	h.mu.Lock()
	for name, lp := range h.plugins {
		lp.Pool.Close(ctx)
		lp.Module.Close(ctx)
		delete(h.plugins, name)
	}
	h.mu.Unlock()
	return h.runtime.Close(ctx)
}
