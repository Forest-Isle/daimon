package agent

import (
	"strings"
	"testing"
)

func TestExtractStructuredResult_ValidXML(t *testing.T) {
	raw := `Here is what I did.

<result>
<status>success</status>
<summary>Created the user authentication module with JWT support.</summary>
<artifacts>/src/auth.go, /src/auth_test.go</artifacts>
</result>`

	result := extractStructuredResult(raw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != StatusSuccess {
		t.Errorf("status = %q, want %q", result.Status, StatusSuccess)
	}
	if result.Summary != "Created the user authentication module with JWT support." {
		t.Errorf("summary = %q", result.Summary)
	}
	if len(result.Artifacts) != 2 {
		t.Errorf("artifacts len = %d, want 2", len(result.Artifacts))
	}
}

func TestExtractStructuredResult_NoBlock(t *testing.T) {
	raw := "Just some plain text output without any structured block."
	result := extractStructuredResult(raw)
	if result != nil {
		t.Errorf("expected nil for missing block, got %+v", result)
	}
}

func TestExtractStructuredResult_ErrorStatus(t *testing.T) {
	raw := `<result>
<status>error</status>
<summary>Failed to compile: missing dependency.</summary>
<artifacts></artifacts>
</result>`

	result := extractStructuredResult(raw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != StatusError {
		t.Errorf("status = %q, want %q", result.Status, StatusError)
	}
	if len(result.Artifacts) != 0 {
		t.Errorf("artifacts should be empty, got %v", result.Artifacts)
	}
}

func TestExtractStructuredResult_NoArtifacts(t *testing.T) {
	raw := `<result>
<status>success</status>
<summary>Reviewed the code and found no issues.</summary>
</result>`

	result := extractStructuredResult(raw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != StatusSuccess {
		t.Errorf("status = %q, want %q", result.Status, StatusSuccess)
	}
	if len(result.Artifacts) != 0 {
		t.Errorf("artifacts should be empty, got %v", result.Artifacts)
	}
}

func TestFormatResultForParent(t *testing.T) {
	r := &SubAgentResult{
		AgentName: "reviewer",
		Status:    StatusSuccess,
		Summary:   "Found 3 issues.",
		Artifacts: []string{"/src/fix.go"},
	}
	out := formatResultForParent(r)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "reviewer") {
		t.Error("output should contain agent name")
	}
	if !strings.Contains(out, "success") {
		t.Error("output should contain status")
	}
	if !strings.Contains(out, "/src/fix.go") {
		t.Error("output should contain artifacts")
	}
}
