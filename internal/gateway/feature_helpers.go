package gateway

import (
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/feature"
)

// findFeatureInfo looks up a feature by name in the registry.
func (gw *Gateway) findFeatureInfo(name string) *feature.FeatureInfo {
	for _, f := range gw.features.List() {
		if f.Name == name {
			return &f
		}
	}
	return nil
}

// persistFeatureState saves the current runtime feature overrides to disk.
// Logs a warning on failure but does not return an error to keep command
// handlers simple.
func (gw *Gateway) persistFeatureState() {
	if gw.featureStatePath == "" {
		return
	}
	overrides := gw.features.RuntimeOverrides()
	if err := feature.SaveOverrides(gw.featureStatePath, overrides); err != nil {
		slog.Warn("gateway: failed to persist feature state", "err", err)
	}
}
