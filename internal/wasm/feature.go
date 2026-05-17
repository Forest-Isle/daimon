package wasm

import "github.com/Forest-Isle/IronClaw/internal/feature"

// RegisterFeature registers "wasm_plugins" in the feature registry.
func RegisterFeature(reg *feature.Registry) {
	reg.Register(feature.Feature{
		Name:          "wasm_plugins",
		Default:       false,
		Phase:         feature.PhaseConstruct,
		Description:   "WASM plugin system — load tools as .wasm modules with capability-based security",
		HotReloadable: true,
		Dependencies:  []string{},
	})
}
