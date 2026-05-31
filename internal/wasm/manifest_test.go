package wasm

import (
	"context"
	"testing"

	"github.com/Forest-Isle/IronClaw/internal/feature"
)

func TestRegisterFeature(t *testing.T) {
	reg := feature.NewRegistry()
	RegisterFeature(reg)

	// Verify it was registered by checking feature names
	reg.ResolveAndInit(context.Background())
	features := reg.List()
	found := false
	for _, f := range features {
		if f.Name == "wasm_plugins" {
			found = true
			if f.HotReloadable != true {
				t.Error("expected wasm_plugins to be hot-reloadable")
			}
			break
		}
	}
	if !found {
		t.Error("wasm_plugins feature not found after registration")
	}
}
