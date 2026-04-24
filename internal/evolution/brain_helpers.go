package evolution

// brain_helpers.go adds lightweight methods needed by the Brain cross-loop
// feedback channels. Each method is a thin accessor on its respective type.

// BoostTool increases preference confidence for a specific tool based on
// external feedback (e.g., from skill activation results). The boost is
// proportional to the provided reward signal. Thread-safe.
func (p *PreferenceLearner) BoostTool(toolName string, reward float64) {
	if toolName == "" || reward <= 0 {
		return
	}

	boost := clampUnit(reward * 0.1) // max 0.1 per call
	prefKey := prefMapKey("tool_preference", toolName)

	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.preferences[prefKey]
	if !ok {
		// Create a new entry if within capacity.
		if p.cfg.MaxPreferences > 0 && len(p.preferences) >= p.cfg.MaxPreferences {
			p.evictLowestLocked()
		}
		p.preferences[prefKey] = &PreferenceEntry{
			Category:   "tool_preference",
			Key:        toolName,
			Value:      "skill_boosted",
			Confidence: boost,
			Count:      1,
		}
		return
	}

	entry.Confidence = clampUnit(entry.Confidence + boost)
}

// GetToolPriorities returns a snapshot of all evolved tool priorities.
// Thread-safe.
func (so *StrategyOptimizer) GetToolPriorities() map[string]float64 {
	so.mu.Lock()
	defer so.mu.Unlock()

	result := make(map[string]float64, len(so.strategy.ToolPriorities))
	for tool, param := range so.strategy.ToolPriorities {
		result[tool] = param.Value
	}
	return result
}

// SetToolPriorities stores a snapshot of tool priorities from the strategy
// optimizer, which the synthesizer can use when scoring pattern candidates.
// Thread-safe.
func (s *SkillSynthesizer) SetToolPriorities(priorities map[string]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Store as a simple field; the synthesizer can consult this when ranking
	// candidates. We add the field to the struct in brain.go's init or
	// accept it as advisory data.
	s.toolPriorities = priorities
}
