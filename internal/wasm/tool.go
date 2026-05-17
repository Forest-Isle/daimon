package wasm

import (
	"context"
	"fmt"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/tool"
	"github.com/tetratelabs/wazero/api"
)

// WasmTool implements tool.Tool by delegating to a WASM module.
type WasmTool struct {
	plugin *LoadedPlugin
}

// NewWasmTool creates a new WasmTool from a loaded plugin.
func NewWasmTool(plugin *LoadedPlugin) *WasmTool {
	return &WasmTool{plugin: plugin}
}

// Name returns the plugin name as the tool name.
func (wt *WasmTool) Name() string {
	if wt.plugin == nil || wt.plugin.Manifest == nil {
		return "wasm-unknown"
	}
	return wt.plugin.Manifest.Name
}

// Description returns the plugin description.
func (wt *WasmTool) Description() string {
	if wt.plugin == nil || wt.plugin.Manifest == nil {
		return "WASM plugin"
	}
	desc := wt.plugin.Manifest.Description
	if desc == "" {
		desc = fmt.Sprintf("WASM plugin %s v%s", wt.plugin.Manifest.Name, wt.plugin.Manifest.Version)
	}
	return desc
}

// RequiresApproval returns false — WASM tools run sandboxed.
func (wt *WasmTool) RequiresApproval() bool {
	return false
}

// InputSchema converts the manifest's interface to JSON Schema format.
func (wt *WasmTool) InputSchema() map[string]any {
	if wt.plugin == nil || wt.plugin.Manifest == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}

	m := wt.plugin.Manifest
	properties := make(map[string]any)
	required := make([]string, 0)

	for name, param := range m.Interface.Inputs {
		prop := map[string]any{
			"type":        param.Type,
			"description": param.Description,
		}
		properties[name] = prop
		if param.Required {
			required = append(required, name)
		}
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// Execute runs the WASM module's "execute" function with the given input.
func (wt *WasmTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	if wt.plugin == nil {
		return tool.Result{}, fmt.Errorf("wasm: plugin not loaded")
	}

	// Apply timeout from manifest config
	timeout := wt.plugin.Manifest.Runtime.TimeoutMS
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
	}

	// Acquire instance from pool
	inst, err := wt.plugin.Pool.Acquire(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("wasm: acquire instance: %w", err)
	}
	defer wt.plugin.Pool.Release(inst)

	// Write input to WASM linear memory
	inputPtr, err := wt.writeToMemory(ctx, inst, input)
	if err != nil {
		return tool.Result{}, fmt.Errorf("wasm: write input: %w", err)
	}

	// Call the execute function: execute(ptr, len) -> (ptr, len)
	results, err := inst.ExportedFunction("execute").Call(ctx, inputPtr, uint64(len(input)))
	if err != nil {
		return tool.Result{}, fmt.Errorf("wasm: execute call: %w", err)
	}

	if len(results) < 2 {
		return tool.Result{}, fmt.Errorf("wasm: expected 2 return values (ptr, len), got %d", len(results))
	}

	// Read output from linear memory
	output := wt.readFromMemory(inst, uint32(results[0]), uint32(results[1]))
	return tool.Result{Output: string(output), Type: tool.ResultText}, nil
}

// writeToMemory writes data to WASM linear memory using the module's malloc.
func (wt *WasmTool) writeToMemory(ctx context.Context, inst api.Module, data []byte) (uint64, error) {
	malloc := inst.ExportedFunction("malloc")
	if malloc == nil {
		return 0, fmt.Errorf("wasm: malloc export not found")
	}
	results, err := malloc.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("wasm: malloc call: %w", err)
	}
	if len(results) < 1 {
		return 0, fmt.Errorf("wasm: malloc returned no value")
	}
	ptr := uint32(results[0])
	if !inst.Memory().Write(ptr, data) {
		return 0, fmt.Errorf("wasm: write to memory failed")
	}
	return uint64(ptr), nil
}

// readFromMemory reads data from WASM linear memory.
func (wt *WasmTool) readFromMemory(inst api.Module, ptr, length uint32) []byte {
	if length == 0 {
		return nil
	}
	data, ok := inst.Memory().Read(ptr, length)
	if !ok {
		return nil
	}
	return data
}
