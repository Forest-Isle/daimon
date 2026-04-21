package gateway

// featureEnabled returns whether a feature is enabled in the registry.
// Falls back to false if the registry is not yet initialized (backward compat during transition).
func (gw *Gateway) featureEnabled(name string) bool {
	if gw.features == nil {
		return false
	}
	return gw.features.IsEnabled(name)
}
