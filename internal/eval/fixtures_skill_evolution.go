package eval

// SkillEvolutionSuite returns tasks for the skill_evolution dimension: automatic
// skill draft quality (Loop 2) — distinct from DimSkillLearning (read_skill / user skills).
func SkillEvolutionSuite() []TaskCase {
	return []TaskCase{
		{
			ID:          TaskIDSkillEvolutionDraftQuality,
			Goal:        "[offline] Skill synthesizer: heuristic draft structure and anti-spam checks",
			Complexity:  "simple",
			Tags:        []string{"evolution", "skill_synthesis", "dimension"},
			Dimension:   DimSkillEvolution,
			ExpectTools: nil,
		},
	}
}
