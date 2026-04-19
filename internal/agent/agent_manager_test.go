package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMarkdownAgentSpec(t *testing.T) {
	content := `---
name: "test-reviewer"
description: "Reviews code for issues."
model: haiku-model
max_iterations: 3
tools:
  - bash
  - file
timeout: "60s"
failure_strategy: fail_fast
tags:
  - review
---

You are a code reviewer.

Focus on correctness and security.
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test-reviewer.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	spec, err := loadMarkdownAgentSpec(path)
	if err != nil {
		t.Fatal(err)
	}

	if spec.Name != "test-reviewer" {
		t.Errorf("name = %q", spec.Name)
	}
	if spec.Model != "haiku-model" {
		t.Errorf("model = %q", spec.Model)
	}
	if spec.MaxIterations != 3 {
		t.Errorf("max_iterations = %d", spec.MaxIterations)
	}
	if len(spec.Tools) != 2 {
		t.Errorf("tools = %v", spec.Tools)
	}
	if spec.FailureStrategy != StrategyFailFast {
		t.Errorf("failure_strategy = %q", spec.FailureStrategy)
	}
	expected := "You are a code reviewer.\n\nFocus on correctness and security."
	if spec.SystemPrompt != expected {
		t.Errorf("system_prompt = %q, want %q", spec.SystemPrompt, expected)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantYAML string
		wantBody string
		wantErr  bool
	}{
		{"valid", "---\nname: test\n---\nBody text.", "name: test", "Body text.", false},
		{"no frontmatter", "Just plain text.", "", "", true},
		{"unclosed frontmatter", "---\nname: test\nno closing", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml, body, err := splitFrontmatter(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if yaml != tt.wantYAML {
					t.Errorf("yaml = %q, want %q", yaml, tt.wantYAML)
				}
				if body != tt.wantBody {
					t.Errorf("body = %q, want %q", body, tt.wantBody)
				}
			}
		})
	}
}

func TestLoadDir_MixedFormats(t *testing.T) {
	dir := t.TempDir()

	yamlContent := "name: yaml-agent\ndescription: yaml agent\n"
	if err := os.WriteFile(filepath.Join(dir, "agent1.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	mdContent := "---\nname: md-agent\ndescription: markdown agent\n---\nYou are helpful.\n"
	if err := os.WriteFile(filepath.Join(dir, "agent2.md"), []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := &AgentManager{}
	if err := mgr.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	specs := mgr.All()
	if len(specs) != 2 {
		t.Errorf("expected 2 specs, got %d", len(specs))
	}
}
