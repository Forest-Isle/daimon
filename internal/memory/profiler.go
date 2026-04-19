package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const profileGenerationPrompt = `You are a user profile synthesizer. Given a set of reflection-level observations about a user (and optionally an existing profile), create or update a structured user profile.

Output the profile in this exact format:

## Identity
[Who the user is - role, expertise, background]

## Preferences
[How they like to work - communication style, tool preferences, approach preferences]

## Current Focus
[What they're currently working on - active projects, goals, challenges]

Rules:
1. Be specific and evidence-based - every claim should be grounded in the reflections.
2. If updating an existing profile, preserve accurate information and update what has changed.
3. Keep each section to 2-4 sentences.
4. Focus on information that would help personalize future assistance.`

const sectionUpdatePrompt = `你是用户画像维护助手。根据以下新观察，更新用户的「%s」画像。

规则:
1. 保留当前画像中仍然成立的信息
2. 整合新观察，如有矛盾以更近期的观察为准
3. 用简洁的要点列表格式输出
4. 如果新观察不改变当前画像，原样返回当前画像内容
5. 只输出画像内容，不要输出其他文字`

const classifyFactPrompt = `你是一个用户画像分类器。给定一条用户相关的事实，判断它属于以下哪个画像分类:

- communication: 沟通偏好（语言风格、回复格式偏好）
- tech_stack: 技术栈画像（使用的语言、框架、工具）
- work_pattern: 工作模式（工作时间、习惯、流程）
- projects: 项目上下文（当前项目、目标、任务）
- feedback: 反馈模式（对建议的接受度、常见反馈类型）
- identity: 身份画像（角色、背景、专业领域）
- none: 不属于以上任何分类

只输出分类ID（如 "communication"），不要输出其他文字。`

// Profiler generates and maintains user profiles from reflection memories.
type Profiler struct {
	store     Store
	completer Completer
	db        *sql.DB
	baseDir   string
	cfg       MemoryConfig

	registry *ProfileSectionRegistry
	buffers  map[string]*SectionBuffer
}

// NewProfiler creates a new Profiler instance.
func NewProfiler(store Store, completer Completer, db *sql.DB, baseDir string, cfg MemoryConfig) *Profiler {
	reg := NewProfileSectionRegistry()
	buffers := make(map[string]*SectionBuffer, len(reg.All()))
	for _, sec := range reg.All() {
		buffers[sec.ID] = NewSectionBuffer(sec)
	}
	return &Profiler{
		store:     store,
		completer: completer,
		db:        db,
		baseDir:   baseDir,
		cfg:       cfg,
		registry:  reg,
		buffers:   buffers,
	}
}

// RouteFact routes an extracted fact to the appropriate profile section buffer.
// Layer 1: direct category mapping via registry. Layer 2: LLM-based classification.
func (p *Profiler) RouteFact(ctx context.Context, fact ExtractedFact) {
	if sectionID, ok := p.registry.RouteCategory(fact.Category); ok {
		if buf, exists := p.buffers[sectionID]; exists {
			buf.Add(fact.Content)
			return
		}
	}

	sectionID := p.classifyFactByLLM(ctx, fact.Content)
	if sectionID != "" {
		if buf, exists := p.buffers[sectionID]; exists {
			buf.Add(fact.Content)
		}
	}
}

func (p *Profiler) classifyFactByLLM(ctx context.Context, content string) string {
	if p.completer == nil {
		return ""
	}

	resp, err := p.completer.Complete(ctx, classifyFactPrompt, content)
	if err != nil {
		slog.Warn("classifyFactByLLM failed", "error", err)
		return ""
	}

	sectionID := strings.TrimSpace(resp)
	if sectionID == "none" {
		return ""
	}
	if _, ok := p.registry.Get(sectionID); ok {
		return sectionID
	}
	return ""
}

// CheckAndUpdateSections iterates all buffers and updates sections that meet their threshold.
func (p *Profiler) CheckAndUpdateSections(ctx context.Context, userID string) error {
	for sectionID, buf := range p.buffers {
		if buf.ShouldUpdate() {
			if err := p.UpdateSection(ctx, sectionID, userID); err != nil {
				slog.Warn("section update failed", "section", sectionID, "error", err)
			}
		}
	}
	return nil
}

// UpdateSection drains the buffer for a section, loads existing content, calls LLM for
// incremental update, and saves the new section file.
func (p *Profiler) UpdateSection(ctx context.Context, sectionID, userID string) error {
	sec, ok := p.registry.Get(sectionID)
	if !ok {
		return fmt.Errorf("unknown section: %s", sectionID)
	}

	buf, ok := p.buffers[sectionID]
	if !ok {
		return fmt.Errorf("no buffer for section: %s", sectionID)
	}

	facts := buf.Drain()
	if len(facts) == 0 {
		return nil
	}

	profilePath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", sectionID))
	var existingContent string
	var existingMF *MemoryFile
	var evidenceCount int

	if mf, err := parseMemoryFile(profilePath); err == nil {
		existingContent = mf.Content
		existingMF = mf
		if ec, ok := mf.Metadata["evidence_count"]; ok {
			if v, err := strconv.Atoi(ec); err == nil {
				evidenceCount = v
			}
		}
	}

	prompt := fmt.Sprintf(sectionUpdatePrompt, sec.Name)
	var userMsg strings.Builder
	if existingContent != "" {
		userMsg.WriteString("当前画像:\n")
		userMsg.WriteString(existingContent)
		userMsg.WriteString("\n\n")
	}
	userMsg.WriteString("新观察:\n")
	for _, f := range facts {
		userMsg.WriteString("- ")
		userMsg.WriteString(f)
		userMsg.WriteString("\n")
	}

	updatedContent, err := p.completer.Complete(ctx, prompt, userMsg.String())
	if err != nil {
		for _, f := range facts {
			buf.Add(f)
		}
		return fmt.Errorf("LLM section update for %s: %w", sectionID, err)
	}

	if existingMF != nil {
		archivedDir := filepath.Join(p.baseDir, "archived")
		_ = os.MkdirAll(archivedDir, 0755)
		archivedPath := filepath.Join(archivedDir, fmt.Sprintf("profile_%s.md", sectionID))
		_ = os.Rename(profilePath, archivedPath)
	}

	evidenceCount += len(facts)
	confidence := float64(evidenceCount) * 0.1
	if confidence > 1.0 {
		confidence = 1.0
	}

	now := time.Now()
	createdAt := now
	if existingMF != nil {
		createdAt = existingMF.CreatedAt
	}

	mf := MemoryFile{
		ID:        fmt.Sprintf("profile_%s", sectionID),
		Scope:     "user",
		UserID:    userID,
		Type:      "profile",
		CreatedAt: createdAt,
		UpdatedAt: now,
		Strength:  1.0,
		Metadata: map[string]string{
			"type":           "profile",
			"section":        sectionID,
			"priority":       strconv.Itoa(sec.Priority),
			"confidence":     fmt.Sprintf("%.2f", confidence),
			"evidence_count": strconv.Itoa(evidenceCount),
		},
		Content: strings.TrimSpace(updatedContent),
	}

	if err := writeProfileAtomic(profilePath, mf); err != nil {
		return fmt.Errorf("write section file %s: %w", sectionID, err)
	}

	slog.Info("profile section updated",
		"section", sectionID,
		"evidence_count", evidenceCount,
		"confidence", confidence,
	)
	return nil
}

// OnReflectionCreated is called after a reflection is generated to potentially trigger profile updates.
func (p *Profiler) OnReflectionCreated(ctx context.Context, userID string, level int) error {
	if level != 1 {
		return nil
	}
	return p.CheckAndUpdateSections(ctx, userID)
}

// LoadProfileSections scans user/profile_*.md files, sorts by priority, and concatenates
// them into a formatted string with a 3200-character budget.
func LoadProfileSections(baseDir string) (string, error) {
	userDir := filepath.Join(baseDir, "user")
	matches, err := filepath.Glob(filepath.Join(userDir, "profile_*.md"))
	if err != nil {
		return "", fmt.Errorf("glob profile sections: %w", err)
	}

	reg := NewProfileSectionRegistry()

	type sectionEntry struct {
		name       string
		priority   int
		confidence float64
		content    string
	}

	var sections []sectionEntry
	for _, path := range matches {
		mf, err := parseMemoryFile(path)
		if err != nil {
			continue
		}
		if mf.Type != "profile" {
			if mf.Metadata == nil || mf.Metadata["type"] != "profile" {
				continue
			}
		}

		sectionID := mf.Metadata["section"]
		if sectionID == "" {
			sectionID = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "profile_"), ".md")
		}

		displayName := sectionID
		if sec, ok := reg.Get(sectionID); ok {
			displayName = sec.Name
		}

		priority := 99
		if p, ok := mf.Metadata["priority"]; ok {
			if v, err := strconv.Atoi(p); err == nil {
				priority = v
			}
		}

		var confidence float64
		if c, ok := mf.Metadata["confidence"]; ok {
			if v, err := strconv.ParseFloat(c, 64); err == nil {
				confidence = v
			}
		}

		sections = append(sections, sectionEntry{
			name:       displayName,
			priority:   priority,
			confidence: confidence,
			content:    mf.Content,
		})
	}

	if len(sections) == 0 {
		return "", nil
	}

	sort.Slice(sections, func(i, j int) bool {
		if sections[i].priority != sections[j].priority {
			return sections[i].priority < sections[j].priority
		}
		return sections[i].name < sections[j].name
	})

	const budget = 3200
	var result strings.Builder
	for _, s := range sections {
		label := ""
		if s.confidence < 0.5 {
			label = " (初步观察)"
		}
		section := fmt.Sprintf("## %s%s\n%s\n\n", s.name, label, s.content)
		if result.Len()+len(section) > budget {
			break
		}
		result.WriteString(section)
	}

	return strings.TrimSpace(result.String()), nil
}

// ColdStartPrompt returns a system prompt snippet to guide the agent in learning
// about the user during early interactions. Returns "" if the profile is already
// sufficiently populated.
func (p *Profiler) ColdStartPrompt() string {
	userDir := filepath.Join(p.baseDir, "user")
	files, _ := filepath.Glob(filepath.Join(userDir, "profile_*.md"))

	highConfCount := 0
	for _, f := range files {
		mf, err := parseMemoryFile(f)
		if err != nil || mf.Type != "profile" {
			continue
		}
		if c, ok := mf.Metadata["confidence"]; ok {
			conf, _ := strconv.ParseFloat(c, 64)
			if conf >= 0.5 {
				highConfCount++
			}
		}
	}

	if highConfCount >= 3 {
		return ""
	}

	return `[Profile Building Mode]
你对当前用户的了解还很少。在自然对话中，注意观察并记录以下信息：
- 用户使用的语言和沟通风格
- 提到的技术栈和工具
- 工作方式和偏好
不要直接询问这些信息，而是从交互中自然提取。`
}

// MigrateLegacyProfile converts a legacy single-file profile (profile_<userID>.md)
// into the new multi-section format.
func (p *Profiler) MigrateLegacyProfile(ctx context.Context, userID string) error {
	oldPath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", userID))
	mf, err := parseMemoryFile(oldPath)
	if err != nil {
		return nil
	}

	if mf.Metadata != nil {
		if _, hasSection := mf.Metadata["section"]; hasSection {
			return nil
		}
	}

	sectionMap := map[string]string{
		"Identity":      "identity",
		"Preferences":   "communication",
		"Current Focus": "projects",
	}

	parts := strings.Split(mf.Content, "## ")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lines := strings.SplitN(part, "\n", 2)
		header := strings.TrimSpace(lines[0])
		content := ""
		if len(lines) > 1 {
			content = strings.TrimSpace(lines[1])
		}
		if sectionID, ok := sectionMap[header]; ok && content != "" {
			sec, _ := p.registry.Get(sectionID)
			profileID := fmt.Sprintf("profile_%s", sectionID)
			profilePath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", sectionID))
			now := time.Now()
			newMF := MemoryFile{
				ID: profileID, Scope: "user", Type: "profile",
				CreatedAt: now, UpdatedAt: now, Strength: 1.0,
				Metadata: map[string]string{
					"type": "profile", "section": sectionID,
					"priority":       strconv.Itoa(sec.Priority),
					"confidence":     "0.30",
					"evidence_count": "3",
				},
				Content: content,
			}
			if err := writeProfileAtomic(profilePath, newMF); err != nil {
				slog.Warn("migration: failed to save section", "section", sectionID, "error", err)
			}
		}
	}

	archivedDir := filepath.Join(p.baseDir, "archived")
	_ = os.MkdirAll(archivedDir, 0755)
	archivedPath := filepath.Join(archivedDir, filepath.Base(oldPath))
	_ = os.Rename(oldPath, archivedPath)

	slog.Info("legacy profile migrated", "user_id", userID)
	return nil
}

// --- Legacy / fallback methods preserved below ---

// GenerateProfile creates or updates a user profile from reflection memories (legacy single-file mode).
func (p *Profiler) GenerateProfile(ctx context.Context, userID string) error {
	slog.Info("generating user profile", "user_id", userID)

	existingProfile, err := p.LoadProfile(ctx, userID)
	if err != nil {
		slog.Warn("failed to load existing profile, continuing without it", "error", err)
		existingProfile = ""
	}

	reflections, err := p.collectReflections(userID)
	if err != nil {
		return fmt.Errorf("collect reflections: %w", err)
	}

	if len(reflections) == 0 {
		slog.Info("no reflections found, skipping profile generation", "user_id", userID)
		return nil
	}

	var promptBuilder strings.Builder
	promptBuilder.WriteString("Reflections about this user:\n\n")
	for i, r := range reflections {
		_, _ = fmt.Fprintf(&promptBuilder, "--- Reflection %d ---\n%s\n\n", i+1, r)
	}

	if existingProfile != "" {
		promptBuilder.WriteString("Existing profile:\n\n")
		promptBuilder.WriteString(existingProfile)
		promptBuilder.WriteString("\n")
	}

	profileContent, err := p.completer.Complete(ctx, profileGenerationPrompt, promptBuilder.String())
	if err != nil {
		return fmt.Errorf("LLM profile generation: %w", err)
	}

	if err := p.saveProfile(ctx, userID, profileContent); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}

	slog.Info("user profile generated successfully", "user_id", userID)
	return nil
}

// LoadProfile reads and returns the content of the user's profile if it exists.
func (p *Profiler) LoadProfile(ctx context.Context, userID string) (string, error) {
	return LoadUserProfile(p.baseDir, userID)
}

// LoadUserProfile is a standalone function that reads a user's profile file.
// It is intended for use from agent/runtime.go's buildSystemPrompt.
func LoadUserProfile(baseDir string, userID string) (string, error) {
	profilePath := filepath.Join(baseDir, "user", fmt.Sprintf("profile_%s.md", userID))

	data, err := os.ReadFile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read profile: %w", err)
	}

	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) < 3 {
		return strings.TrimSpace(string(data)), nil
	}

	return strings.TrimSpace(parts[2]), nil
}

// collectReflections scans the user/ directory for reflection-type memory files.
func (p *Profiler) collectReflections(userID string) ([]string, error) {
	userDir := filepath.Join(p.baseDir, "user")
	files, err := filepath.Glob(filepath.Join(userDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob user directory: %w", err)
	}

	var reflections []string
	for _, filePath := range files {
		if strings.HasPrefix(filepath.Base(filePath), "profile_") {
			continue
		}

		mf, err := parseMemoryFile(filePath)
		if err != nil {
			slog.Debug("skipping unparseable file", "path", filePath, "error", err)
			continue
		}

		if mf.Type != "reflection" {
			if mf.Metadata == nil || mf.Metadata["type"] != "reflection" {
				continue
			}
		}

		if mf.UserID != "" && mf.UserID != userID {
			continue
		}

		if mf.Content != "" {
			reflections = append(reflections, mf.Content)
		}
	}

	return reflections, nil
}

// parseMemoryFile parses a Markdown file with YAML frontmatter.
func parseMemoryFile(path string) (*MemoryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid frontmatter format")
	}

	var mf MemoryFile
	if err := yaml.Unmarshal([]byte(parts[1]), &mf); err != nil {
		return nil, err
	}

	mf.Content = strings.TrimSpace(parts[2])
	return &mf, nil
}

// saveProfile writes the profile to a Markdown file and syncs to the SQLite index.
func (p *Profiler) saveProfile(ctx context.Context, userID, content string) error {
	now := time.Now()
	profileID := fmt.Sprintf("profile_%s", userID)
	profilePath := filepath.Join(p.baseDir, "user", fmt.Sprintf("profile_%s.md", userID))

	mf := MemoryFile{
		ID:        profileID,
		Scope:     "user",
		UserID:    userID,
		Type:      "profile",
		CreatedAt: now,
		UpdatedAt: now,
		Strength:  1.0,
		Metadata: map[string]string{
			"type":    "profile",
			"user_id": userID,
		},
		Content: content,
	}

	if existing, err := parseMemoryFile(profilePath); err == nil {
		mf.CreatedAt = existing.CreatedAt
	}

	if err := writeProfileAtomic(profilePath, mf); err != nil {
		return fmt.Errorf("write profile file: %w", err)
	}

	entry := Entry{
		ID:        profileID,
		Scope:     ScopeUser,
		UserID:    userID,
		Content:   content,
		CreatedAt: mf.CreatedAt,
		UpdatedAt: now,
		Metadata: map[string]string{
			"type":    "profile",
			"user_id": userID,
		},
	}

	return p.store.Save(ctx, entry)
}

// writeProfileAtomic writes a MemoryFile to disk atomically using a temp file + rename.
func writeProfileAtomic(path string, mf MemoryFile) error {
	tmpPath := path + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString("---\n"); err != nil {
		return err
	}

	enc := yaml.NewEncoder(f)
	if err := enc.Encode(mf); err != nil {
		return err
	}
	_ = enc.Close()

	if _, err := f.WriteString("---\n\n"); err != nil {
		return err
	}

	if _, err := f.WriteString(mf.Content); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}
	_ = f.Close()

	return os.Rename(tmpPath, path)
}
