package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePromoteSkillFile(t *testing.T, root, slug, name string, distilled bool, body string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	content := "---\n" +
		"name: " + name + "\n" +
		"description: Test description\n" +
		"version: 0.1.0\n" +
		"metadata:\n" +
		"  distilled: " + promoteBoolString(distilled) + "\n" +
		"  source_candidate: candidate-1\n" +
		"  source_episodes: [e1, e2, e3]\n" +
		"---\n" +
		body + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	return path
}

func promoteBoolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestValidSlug(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		wantErr bool
	}{
		{name: "valid", slug: "draft-one"},
		{name: "parent", slug: "..", wantErr: true},
		{name: "nested", slug: "a/b", wantErr: true},
		{name: "empty", slug: "", wantErr: true},
		{name: "dot", slug: ".", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidSlug(tt.slug)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateDraft(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		slug    string
		skill   string
		distill bool
		body    string
		wantErr string
	}{
		{name: "valid", slug: "valid", skill: "Valid Draft", distill: true, body: "Use this skill."},
		{name: "empty name", slug: "empty-name", skill: "", distill: true, body: "Use this skill.", wantErr: "missing name"},
		{name: "not distilled", slug: "not-distilled", skill: "Not Distilled", distill: false, body: "Use this skill.", wantErr: "not a distilled draft"},
		{name: "empty body", slug: "empty-body", skill: "Empty Body", distill: true, body: "  \n", wantErr: "empty body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePromoteSkillFile(t, dir, tt.slug, tt.skill, tt.distill, tt.body)
			_, err := ValidateDraft(path)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestListDrafts(t *testing.T) {
	staging := t.TempDir()
	writePromoteSkillFile(t, staging, "z-valid", "Valid Draft", true, "Use this skill.")
	writePromoteSkillFile(t, staging, "a-invalid", "Invalid Draft", false, "Use this skill.")
	if err := os.MkdirAll(filepath.Join(staging, "m-missing"), 0755); err != nil {
		t.Fatalf("mkdir missing draft: %v", err)
	}

	drafts, err := ListDrafts(staging)
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 3 {
		t.Fatalf("got %d drafts, want 3", len(drafts))
	}
	if drafts[0].Slug != "a-invalid" || drafts[1].Slug != "m-missing" || drafts[2].Slug != "z-valid" {
		t.Fatalf("drafts not sorted by slug: %#v", drafts)
	}
	if drafts[0].Status == "valid" || !strings.Contains(drafts[0].Status, "not a distilled draft") {
		t.Fatalf("unexpected invalid status: %q", drafts[0].Status)
	}
	if drafts[1].Status != "missing SKILL.md" {
		t.Fatalf("unexpected missing status: %q", drafts[1].Status)
	}
	if drafts[2].Status != "valid" {
		t.Fatalf("unexpected valid status: %q", drafts[2].Status)
	}
	if drafts[2].Episodes != 3 {
		t.Fatalf("got %d episodes, want 3", drafts[2].Episodes)
	}

	empty, err := ListDrafts(filepath.Join(staging, "does-not-exist"))
	if err != nil {
		t.Fatalf("list missing staging: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("got %d drafts for missing staging, want 0", len(empty))
	}
}

func TestPromoteDraft(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		staging := t.TempDir()
		active := t.TempDir()
		writePromoteSkillFile(t, staging, "draft", "Draft Skill", true, "Use this skill.")

		target, err := PromoteDraft(staging, active, "draft")
		if err != nil {
			t.Fatalf("promote draft: %v", err)
		}
		if target != filepath.Join(active, "draft") {
			t.Fatalf("target = %q, want %q", target, filepath.Join(active, "draft"))
		}
		if _, err := os.Stat(filepath.Join(staging, "draft")); !os.IsNotExist(err) {
			t.Fatalf("staging draft still exists or stat failed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(active, "draft", "SKILL.md")); err != nil {
			t.Fatalf("active skill missing: %v", err)
		}
	})

	t.Run("invalid slug", func(t *testing.T) {
		_, err := PromoteDraft(t.TempDir(), t.TempDir(), "../escape")
		if err == nil {
			t.Fatalf("expected invalid slug error")
		}
	})

	t.Run("target exists", func(t *testing.T) {
		staging := t.TempDir()
		active := t.TempDir()
		writePromoteSkillFile(t, staging, "draft", "Draft Skill", true, "Use this skill.")
		if err := os.MkdirAll(filepath.Join(active, "draft"), 0755); err != nil {
			t.Fatalf("mkdir target: %v", err)
		}
		_, err := PromoteDraft(staging, active, "draft")
		if err == nil || !strings.Contains(err.Error(), "already promoted") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("active name conflict", func(t *testing.T) {
		staging := t.TempDir()
		active := t.TempDir()
		writePromoteSkillFile(t, staging, "draft", "Same Name", true, "Use this skill.")
		writePromoteSkillFile(t, active, "existing", "Same Name", false, "Already active.")
		_, err := PromoteDraft(staging, active, "draft")
		if err == nil || !strings.Contains(err.Error(), `a skill named "Same Name" is already active`) {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("symlink SKILL.md refused", func(t *testing.T) {
		staging := t.TempDir()
		active := t.TempDir()
		outside := t.TempDir()
		outsideSkill := writePromoteSkillFile(t, outside, "outside", "Symlink Skill", true, "Use this skill.")
		draftDir := filepath.Join(staging, "draft")
		if err := os.MkdirAll(draftDir, 0755); err != nil {
			t.Fatalf("mkdir draft dir: %v", err)
		}
		if err := os.Symlink(outsideSkill, filepath.Join(draftDir, "SKILL.md")); err != nil {
			t.Fatalf("symlink skill: %v", err)
		}

		_, err := PromoteDraft(staging, active, "draft")
		if err == nil || !strings.Contains(err.Error(), "is a symlink; refusing") {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(active, "draft")); !os.IsNotExist(err) {
			t.Fatalf("active draft should not exist: %v", err)
		}
	})

	t.Run("symlink dir refused", func(t *testing.T) {
		staging := t.TempDir()
		active := t.TempDir()
		outside := t.TempDir()
		writePromoteSkillFile(t, outside, "outside", "Symlink Dir Skill", true, "Use this skill.")
		if err := os.Symlink(filepath.Join(outside, "outside"), filepath.Join(staging, "draft")); err != nil {
			t.Fatalf("symlink draft dir: %v", err)
		}

		_, err := PromoteDraft(staging, active, "draft")
		if err == nil || !strings.Contains(err.Error(), "is a symlink; refusing") {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(active, "draft")); !os.IsNotExist(err) {
			t.Fatalf("active draft should not exist: %v", err)
		}
	})
}

func TestDemoteSkill(t *testing.T) {
	t.Run("distilled", func(t *testing.T) {
		active := t.TempDir()
		staging := t.TempDir()
		writePromoteSkillFile(t, active, "draft", "Draft Skill", true, "Use this skill.")
		if err := DemoteSkill(active, staging, "draft"); err != nil {
			t.Fatalf("demote skill: %v", err)
		}
		if _, err := os.Stat(filepath.Join(active, "draft")); !os.IsNotExist(err) {
			t.Fatalf("distilled skill still exists or stat failed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(staging, "draft", "SKILL.md")); err != nil {
			t.Fatalf("staged draft missing: %v", err)
		}
	})

	t.Run("not distilled", func(t *testing.T) {
		active := t.TempDir()
		staging := t.TempDir()
		writePromoteSkillFile(t, active, "manual", "Manual Skill", false, "Use this skill.")
		err := DemoteSkill(active, staging, "manual")
		if err == nil || !strings.Contains(err.Error(), "not a distilled skill") {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(active, "manual", "SKILL.md")); err != nil {
			t.Fatalf("manual skill should remain: %v", err)
		}
	})
}
