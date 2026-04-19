package memory

import (
	"sort"
	"time"
)

// ProfileSection defines metadata for a single user profile section.
type ProfileSection struct {
	ID            string
	Name          string
	Priority      int           // 0 = highest
	FactThreshold int           // pending facts before triggering update
	TimeThreshold time.Duration // max time since last update before triggering
}

// ProfileSectionRegistry holds all known profile sections and their routing rules.
type ProfileSectionRegistry struct {
	sections    map[string]ProfileSection
	categoryMap map[string]string // fact category -> section ID
}

// NewProfileSectionRegistry creates a registry with the default section definitions.
func NewProfileSectionRegistry() *ProfileSectionRegistry {
	r := &ProfileSectionRegistry{
		sections: map[string]ProfileSection{
			"communication": {
				ID: "communication", Name: "沟通偏好",
				Priority: 0, FactThreshold: 3, TimeThreshold: 1 * time.Hour,
			},
			"tech_stack": {
				ID: "tech_stack", Name: "技术栈画像",
				Priority: 0, FactThreshold: 3, TimeThreshold: 1 * time.Hour,
			},
			"work_pattern": {
				ID: "work_pattern", Name: "工作模式",
				Priority: 1, FactThreshold: 5, TimeThreshold: 4 * time.Hour,
			},
			"projects": {
				ID: "projects", Name: "项目上下文",
				Priority: 1, FactThreshold: 5, TimeThreshold: 4 * time.Hour,
			},
			"feedback": {
				ID: "feedback", Name: "反馈模式",
				Priority: 2, FactThreshold: 8, TimeThreshold: 24 * time.Hour,
			},
			"identity": {
				ID: "identity", Name: "身份画像",
				Priority: 2, FactThreshold: 8, TimeThreshold: 24 * time.Hour,
			},
		},
		categoryMap: map[string]string{
			"preference":   "communication",
			"identity":     "identity",
			"relationship": "identity",
			"task":         "projects",
		},
	}
	return r
}

// Get returns a section by ID.
func (r *ProfileSectionRegistry) Get(id string) (ProfileSection, bool) {
	s, ok := r.sections[id]
	return s, ok
}

// All returns all sections as a slice.
func (r *ProfileSectionRegistry) All() []ProfileSection {
	out := make([]ProfileSection, 0, len(r.sections))
	for _, s := range r.sections {
		out = append(out, s)
	}
	return out
}

// ByPriority returns sections sorted by priority (ascending), then by ID for stable ordering.
func (r *ProfileSectionRegistry) ByPriority() []ProfileSection {
	out := r.All()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RouteCategory maps a fact category to a section ID.
// Returns ("", false) if no direct mapping exists (caller should use LLM fallback).
func (r *ProfileSectionRegistry) RouteCategory(category string) (string, bool) {
	id, ok := r.categoryMap[category]
	return id, ok
}
