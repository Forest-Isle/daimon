package collective

import (
	"log/slog"
)

// SpecializationEngine detects emergent roles from agent performance patterns.
type SpecializationEngine struct {
	reputation *ReputationSystem
	minTasks   int     // minimum tasks before a role can emerge
	minScore   float64 // minimum category score to claim specialization
}

// NewSpecializationEngine creates a new specialization engine.
func NewSpecializationEngine(rep *ReputationSystem) *SpecializationEngine {
	return &SpecializationEngine{
		reputation: rep,
		minTasks:   5,
		minScore:   0.65,
	}
}

// EmergentRole describes a detected agent specialization.
type EmergentRole struct {
	AgentID    string  `json:"agent_id"`
	Role       string  `json:"role"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Label      string  `json:"label"`
}

// Role labels map categories to human-readable role names.
var roleLabels = map[string]string{
	"coding":   "Code Architect",
	"analysis": "Data Analyst",
	"debate":   "Devil's Advocate",
	"review":   "Quality Guardian",
	"browser":  "Web Explorer",
	"writing":  "Content Writer",
	"planning": "Strategic Planner",
	"research": "Research Specialist",
	"simple":   "Quick Task Handler",
	"moderate": "General Problem Solver",
	"complex":  "Complex System Architect",
}

// DetectRoles analyzes all agents and detects emergent specializations.
func (se *SpecializationEngine) DetectRoles(agentIDs []string) []*EmergentRole {
	var roles []*EmergentRole

	for _, agentID := range agentIDs {
		stats := se.reputation.GetAgentStats(agentID)
		if stats.TotalTasks < se.minTasks {
			continue
		}

		for _, cat := range stats.TopCategories {
			score := se.reputation.GetCategoryReputation(agentID, cat)
			if score >= se.minScore {
				role := &EmergentRole{
					AgentID:    agentID,
					Role:       cat,
					Category:   cat,
					Confidence: score,
					Label:      roleLabel(cat),
				}
				roles = append(roles, role)

				slog.Info("collective: emergent role detected",
					"agent", agentID,
					"role", role.Label,
					"confidence", score,
				)
			}
		}
	}

	return roles
}

func roleLabel(category string) string {
	if label, ok := roleLabels[category]; ok {
		return label
	}
	return category
}

// SuggestAgent finds the best agent for a given task category.
func (se *SpecializationEngine) SuggestAgent(agentIDs []string, category string) (string, float64) {
	var bestAgent string
	var bestScore float64

	for _, agentID := range agentIDs {
		score := se.reputation.GetCategoryReputation(agentID, category)
		if score > bestScore {
			bestScore = score
			bestAgent = agentID
		}
	}

	if bestAgent == "" && len(agentIDs) > 0 {
		// No specialist found — pick the agent with highest overall reputation
		for _, agentID := range agentIDs {
			rep := se.reputation.GetReputation(agentID)
			if rep > bestScore {
				bestScore = rep
				bestAgent = agentID
			}
		}
	}

	return bestAgent, bestScore
}
