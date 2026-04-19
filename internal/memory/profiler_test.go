package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type profilerMockCompleter struct {
	response string
	err      error
}

func (m *profilerMockCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return m.response, m.err
}

func TestProfiler_RouteFactToSection_DirectMapping(t *testing.T) {
	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	p.RouteFact(context.Background(), ExtractedFact{
		Content:  "user prefers concise answers",
		Category: "preference",
	})
	if p.buffers["communication"].PendingCount() != 1 {
		t.Error("preference fact should route to communication section")
	}

	p.RouteFact(context.Background(), ExtractedFact{
		Content:  "user is working on IronClaw",
		Category: "task",
	})
	if p.buffers["projects"].PendingCount() != 1 {
		t.Error("task fact should route to projects section")
	}
}

func TestProfiler_RouteFactToSection_LLMFallback(t *testing.T) {
	p := &Profiler{
		registry:  NewProfileSectionRegistry(),
		buffers:   make(map[string]*SectionBuffer),
		completer: &profilerMockCompleter{response: "tech_stack"},
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	p.RouteFact(context.Background(), ExtractedFact{
		Content:  "user writes Go code daily",
		Category: "unknown_category",
	})
	if p.buffers["tech_stack"].PendingCount() != 1 {
		t.Error("LLM-classified fact should route to tech_stack section")
	}
}

func TestProfiler_RouteFactToSection_LLMReturnsNone(t *testing.T) {
	p := &Profiler{
		registry:  NewProfileSectionRegistry(),
		buffers:   make(map[string]*SectionBuffer),
		completer: &profilerMockCompleter{response: "none"},
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	p.RouteFact(context.Background(), ExtractedFact{
		Content:  "the weather is nice",
		Category: "irrelevant",
	})

	for id, buf := range p.buffers {
		if buf.PendingCount() != 0 {
			t.Errorf("buffer %s should be empty when LLM returns 'none', got %d", id, buf.PendingCount())
		}
	}
}

func TestProfiler_UpdateSection(t *testing.T) {
	tmpDir := t.TempDir()
	userDir := filepath.Join(tmpDir, "user")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}
	archivedDir := filepath.Join(tmpDir, "archived")
	if err := os.MkdirAll(archivedDir, 0755); err != nil {
		t.Fatal(err)
	}

	p := &Profiler{
		completer: &profilerMockCompleter{response: "- 偏好简洁回答\n- 喜欢中文交流"},
		baseDir:   tmpDir,
		registry:  NewProfileSectionRegistry(),
		buffers:   make(map[string]*SectionBuffer),
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	p.buffers["communication"].Add("user prefers concise answers")
	p.buffers["communication"].Add("user likes Chinese communication")
	p.buffers["communication"].Add("user wants bullet points")

	err := p.UpdateSection(context.Background(), "communication", "test-user")
	if err != nil {
		t.Fatalf("UpdateSection failed: %v", err)
	}

	profilePath := filepath.Join(userDir, "profile_communication.md")
	mf, err := parseMemoryFile(profilePath)
	if err != nil {
		t.Fatalf("failed to parse saved profile: %v", err)
	}

	if mf.Type != "profile" {
		t.Errorf("expected type=profile, got %q", mf.Type)
	}
	if mf.Metadata["section"] != "communication" {
		t.Errorf("expected section=communication, got %q", mf.Metadata["section"])
	}
	if mf.Metadata["evidence_count"] != "3" {
		t.Errorf("expected evidence_count=3, got %q", mf.Metadata["evidence_count"])
	}
	if mf.Metadata["confidence"] != "0.30" {
		t.Errorf("expected confidence=0.30, got %q", mf.Metadata["confidence"])
	}
	if mf.Strength != 1.0 {
		t.Errorf("expected strength=1.0, got %f", mf.Strength)
	}
}

func TestProfiler_UpdateSection_RequeuesOnError(t *testing.T) {
	tmpDir := t.TempDir()
	userDir := filepath.Join(tmpDir, "user")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}

	p := &Profiler{
		completer: &profilerMockCompleter{err: fmt.Errorf("LLM unavailable")},
		baseDir:   tmpDir,
		registry:  NewProfileSectionRegistry(),
		buffers:   make(map[string]*SectionBuffer),
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	p.buffers["communication"].Add("fact one")
	p.buffers["communication"].Add("fact two")

	err := p.UpdateSection(context.Background(), "communication", "test-user")
	if err == nil {
		t.Fatal("expected error from UpdateSection")
	}

	if p.buffers["communication"].PendingCount() != 2 {
		t.Errorf("expected 2 re-queued facts, got %d", p.buffers["communication"].PendingCount())
	}
}

func TestLoadProfileSections_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "user"), 0755); err != nil {
		t.Fatal(err)
	}
	result, err := LoadProfileSections(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result for empty dir, got %q", result)
	}
}

func TestLoadProfileSections_SortsByPriority(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, "user")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}

	writeTestProfile(t, userDir, "identity", map[string]string{
		"type": "profile", "section": "identity",
		"priority": "2", "confidence": "0.80", "evidence_count": "8",
	}, "Senior Go developer")

	writeTestProfile(t, userDir, "communication", map[string]string{
		"type": "profile", "section": "communication",
		"priority": "0", "confidence": "0.60", "evidence_count": "6",
	}, "Prefers concise answers")

	writeTestProfile(t, userDir, "projects", map[string]string{
		"type": "profile", "section": "projects",
		"priority": "1", "confidence": "0.30", "evidence_count": "3",
	}, "Working on IronClaw")

	result, err := LoadProfileSections(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	commIdx := indexOf(result, "沟通偏好")
	projIdx := indexOf(result, "项目上下文")
	identIdx := indexOf(result, "身份画像")

	if commIdx < 0 || projIdx < 0 || identIdx < 0 {
		t.Fatalf("missing sections in result:\n%s", result)
	}

	if commIdx > projIdx {
		t.Error("communication (priority 0) should come before projects (priority 1)")
	}
	if projIdx > identIdx {
		t.Error("projects (priority 1) should come before identity (priority 2)")
	}

	if !containsString(result, "(初步观察)") {
		t.Error("projects section with confidence 0.30 should have '(初步观察)' label")
	}
}

func writeTestProfile(t *testing.T, dir, sectionID string, meta map[string]string, content string) {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("profile_%s.md", sectionID))

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	mf := MemoryFile{
		ID:       fmt.Sprintf("profile_%s", sectionID),
		Scope:    "user",
		Type:     "profile",
		Strength: 1.0,
		Metadata: meta,
		Content:  content,
	}

	if _, err := f.WriteString("---\n"); err != nil {
		t.Fatal(err)
	}
	enc := yaml.NewEncoder(f)
	if err := enc.Encode(mf); err != nil {
		t.Fatal(err)
	}
	_ = enc.Close()
	if _, err := f.WriteString("---\n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func containsString(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

func TestProfiler_ColdStartPrompt_EmptyDir(t *testing.T) {
	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
		baseDir:  t.TempDir(),
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	prompt := p.ColdStartPrompt()
	if prompt == "" {
		t.Error("expected non-empty cold start prompt for empty profile dir")
	}
	if !containsString(prompt, "Profile Building Mode") {
		t.Error("cold start prompt should contain 'Profile Building Mode'")
	}
}

func TestProfiler_ColdStartPrompt_PopulatedProfile(t *testing.T) {
	baseDir := t.TempDir()
	userDir := filepath.Join(baseDir, "user")
	os.MkdirAll(userDir, 0755)

	for _, sec := range []string{"communication", "tech_stack", "projects"} {
		writeTestProfile(t, userDir, sec, map[string]string{
			"type": "profile", "section": sec,
			"confidence": "0.80", "evidence_count": "10",
		}, "test content for "+sec)
	}

	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
		baseDir:  baseDir,
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	prompt := p.ColdStartPrompt()
	if prompt != "" {
		t.Errorf("expected empty cold start prompt with 3 high-confidence sections, got: %q", prompt)
	}
}

func TestProfiler_MigrateLegacyProfile(t *testing.T) {
	baseDir := t.TempDir()
	userDir := filepath.Join(baseDir, "user")
	os.MkdirAll(userDir, 0755)

	legacyContent := `## Identity
Senior Go developer, 5 years experience

## Preferences
Prefers concise answers in Chinese

## Current Focus
Building IronClaw AI agent framework`

	legacyPath := filepath.Join(userDir, "profile_default.md")
	mf := MemoryFile{
		ID:       "profile_default",
		Scope:    "user",
		Type:     "profile",
		Strength: 1.0,
		Content:  legacyContent,
	}
	if err := writeProfileAtomic(legacyPath, mf); err != nil {
		t.Fatal(err)
	}

	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
		baseDir:  baseDir,
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	if err := p.MigrateLegacyProfile(context.Background(), "default"); err != nil {
		t.Fatalf("MigrateLegacyProfile failed: %v", err)
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("legacy profile should have been moved to archived/")
	}
	archivedPath := filepath.Join(baseDir, "archived", "profile_default.md")
	if _, err := os.Stat(archivedPath); err != nil {
		t.Error("legacy profile should exist in archived/")
	}

	identityPath := filepath.Join(userDir, "profile_identity.md")
	identityMF, err := parseMemoryFile(identityPath)
	if err != nil {
		t.Fatalf("failed to parse migrated identity section: %v", err)
	}
	if identityMF.Metadata["section"] != "identity" {
		t.Errorf("expected section=identity, got %q", identityMF.Metadata["section"])
	}
	if !containsString(identityMF.Content, "Senior Go developer") {
		t.Error("identity section should contain original content")
	}

	commPath := filepath.Join(userDir, "profile_communication.md")
	commMF, err := parseMemoryFile(commPath)
	if err != nil {
		t.Fatalf("failed to parse migrated communication section: %v", err)
	}
	if !containsString(commMF.Content, "concise answers") {
		t.Error("communication section should contain preferences content")
	}
}

func TestProfiler_MigrateLegacyProfile_SkipsNewFormat(t *testing.T) {
	baseDir := t.TempDir()
	userDir := filepath.Join(baseDir, "user")
	os.MkdirAll(userDir, 0755)

	newFormatMF := MemoryFile{
		ID:       "profile_default",
		Scope:    "user",
		Type:     "profile",
		Strength: 1.0,
		Metadata: map[string]string{"section": "communication"},
		Content:  "already migrated",
	}
	writeProfileAtomic(filepath.Join(userDir, "profile_default.md"), newFormatMF)

	p := &Profiler{
		registry: NewProfileSectionRegistry(),
		buffers:  make(map[string]*SectionBuffer),
		baseDir:  baseDir,
	}
	for _, sec := range p.registry.All() {
		p.buffers[sec.ID] = NewSectionBuffer(sec)
	}

	if err := p.MigrateLegacyProfile(context.Background(), "default"); err != nil {
		t.Fatalf("MigrateLegacyProfile should succeed for new-format files: %v", err)
	}

	mf, _ := parseMemoryFile(filepath.Join(userDir, "profile_default.md"))
	if mf.Content != "already migrated" {
		t.Error("new-format profile should not be modified")
	}
}
