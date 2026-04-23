package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillMD(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const testSkillContent = `---
name: test_skill
description: A test skill for unit testing purposes and validation.
trigger:
  keywords: ["test", "validate"]
  tool_pattern: ["bash", "file_read"]
  complexity: simple
  min_match: 0.5
---

# test_skill

## When to use

Use this skill for testing.

## Procedure

1. Read files
2. Run tests
3. Validate output
`

func TestSkillLoader_LoadIndex(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	loader := NewSkillLoader(dir)
	index, err := loader.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 {
		t.Fatalf("expected 1 skill index, got %d", len(index))
	}
	if index[0].Name != "test_skill" {
		t.Errorf("expected name 'test_skill', got %q", index[0].Name)
	}
	if len(index[0].Trigger.Keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(index[0].Trigger.Keywords))
	}
}

func TestSkillLoader_LoadIndex_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	loader := NewSkillLoader(dir)
	index, err := loader.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 0 {
		t.Errorf("expected 0 skills from empty dir, got %d", len(index))
	}
}

func TestSkillLoader_LoadIndex_NonexistentDir(t *testing.T) {
	loader := NewSkillLoader("/tmp/nonexistent-skill-dir-xyz")
	index, err := loader.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if index != nil {
		t.Errorf("expected nil for nonexistent dir, got %v", index)
	}
}

func TestSkillLoader_LoadPartial(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	loader := NewSkillLoader(dir)
	partial, err := loader.LoadPartial("test_skill")
	if err != nil {
		t.Fatal(err)
	}
	// Should contain the first section but not the second
	if !strings.Contains(partial, "# test_skill") {
		t.Error("partial should contain the title")
	}
	if !strings.Contains(partial, "## When to use") {
		t.Error("partial should contain first section heading")
	}
	// Should be cut off before "## Procedure"
	if strings.Contains(partial, "## Procedure") {
		t.Error("partial should not contain second section")
	}
}

func TestSkillLoader_LoadFull(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	loader := NewSkillLoader(dir)
	full, err := loader.LoadFull("test_skill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "## Procedure") {
		t.Error("full content should include all sections")
	}
	if !strings.Contains(full, "3. Validate output") {
		t.Error("full content should include procedure steps")
	}
}

func TestSkillLoader_LoadFull_NotFound(t *testing.T) {
	dir := t.TempDir()
	loader := NewSkillLoader(dir)
	_, err := loader.LoadFull("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

func TestSkillLoader_MatchSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	loader := NewSkillLoader(dir)

	// Match by keyword
	matched := loader.MatchSkills("I need to test something", "")
	if len(matched) != 1 {
		t.Errorf("expected 1 match for 'test', got %d", len(matched))
	}

	// No match
	matched = loader.MatchSkills("deploy to production", "")
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for 'deploy', got %d", len(matched))
	}
}

func TestSkillLoader_MatchSkills_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	loader := NewSkillLoader(dir)
	matched := loader.MatchSkills("VALIDATE the results", "")
	if len(matched) != 1 {
		t.Errorf("expected 1 match (case-insensitive), got %d", len(matched))
	}
}

func TestSkillLoader_MatchSkills_ComplexityFilter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	loader := NewSkillLoader(dir)

	// Matching complexity
	matched := loader.MatchSkills("test this", "simple")
	if len(matched) != 1 {
		t.Errorf("expected 1 match with matching complexity, got %d", len(matched))
	}

	// Non-matching complexity
	matched = loader.MatchSkills("test this", "complex")
	if len(matched) != 0 {
		t.Errorf("expected 0 matches with non-matching complexity, got %d", len(matched))
	}
}

func TestSkillLoader_MultipleSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "SKILL_test.md", testSkillContent)

	deploySkill := `---
name: deploy_skill
description: Deployment workflow for staging and production.
trigger:
  keywords: ["deploy", "release"]
  complexity: complex
---

# deploy_skill

## Steps
1. Build
2. Deploy
`
	writeSkillMD(t, dir, "SKILL_deploy.md", deploySkill)

	loader := NewSkillLoader(dir)
	index, err := loader.LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Errorf("expected 2 skills, got %d", len(index))
	}

	// Match deploy
	matched := loader.MatchSkills("deploy to staging", "")
	if len(matched) != 1 || matched[0].Name != "deploy_skill" {
		t.Errorf("expected deploy_skill match, got %v", matched)
	}
}

func TestSplitSkillFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		hasFM   bool
		fmField string
	}{
		{"with frontmatter", "---\nname: test\n---\nbody", true, "name: test"},
		{"no frontmatter", "just body content", false, ""},
		{"empty", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body := splitSkillFrontmatter([]byte(tt.input))
			if tt.hasFM && fm == nil {
				t.Error("expected frontmatter, got nil")
			}
			if !tt.hasFM && fm != nil {
				t.Errorf("expected no frontmatter, got %q", string(fm))
			}
			if tt.hasFM && !strings.Contains(string(fm), tt.fmField) {
				t.Errorf("frontmatter should contain %q, got %q", tt.fmField, string(fm))
			}
			_ = body // ensure body is returned
		})
	}
}
