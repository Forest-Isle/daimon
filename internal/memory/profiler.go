package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// defaultProfileTriggerCount is the number of L1 reflections before generating a profile.
const defaultProfileTriggerCount = 5

// Profiler generates and maintains user profiles from reflection memories.
type Profiler struct {
	store     Store
	completer Completer
	db        *sql.DB
	baseDir   string
	cfg       MemoryConfig

	mu                  sync.Mutex
	l1CountSinceProfile int
	lastProfileUserID   string
}

// NewProfiler creates a new Profiler instance.
func NewProfiler(store Store, completer Completer, db *sql.DB, baseDir string, cfg MemoryConfig) *Profiler {
	return &Profiler{
		store:     store,
		completer: completer,
		db:        db,
		baseDir:   baseDir,
		cfg:       cfg,
	}
}

// GenerateProfile creates or updates a user profile from reflection memories.
func (p *Profiler) GenerateProfile(ctx context.Context, userID string) error {
	slog.Info("generating user profile", "user_id", userID)

	// Load existing profile if present
	existingProfile, err := p.LoadProfile(ctx, userID)
	if err != nil {
		slog.Warn("failed to load existing profile, continuing without it", "error", err)
		existingProfile = ""
	}

	// Collect reflections from user/ directory
	reflections, err := p.collectReflections(userID)
	if err != nil {
		return fmt.Errorf("collect reflections: %w", err)
	}

	if len(reflections) == 0 {
		slog.Info("no reflections found, skipping profile generation", "user_id", userID)
		return nil
	}

	// Build the LLM prompt
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

	// Call LLM to generate profile
	profileContent, err := p.completer.Complete(ctx, profileGenerationPrompt, promptBuilder.String())
	if err != nil {
		return fmt.Errorf("LLM profile generation: %w", err)
	}

	// Save profile as memory file
	if err := p.saveProfile(ctx, userID, profileContent); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}

	slog.Info("user profile generated successfully", "user_id", userID)
	return nil
}

// OnReflectionCreated is called after a reflection is generated to potentially trigger profile generation.
func (p *Profiler) OnReflectionCreated(ctx context.Context, userID string, level int) error {
	if level != 1 {
		return nil
	}

	p.mu.Lock()

	// Reset counter if user changed
	if p.lastProfileUserID != userID {
		p.l1CountSinceProfile = 0
		p.lastProfileUserID = userID
	}

	p.l1CountSinceProfile++
	count := p.l1CountSinceProfile

	triggerCount := defaultProfileTriggerCount

	if count < triggerCount {
		p.mu.Unlock()
		return nil
	}

	// Reset counter before generating
	p.l1CountSinceProfile = 0
	p.mu.Unlock()

	slog.Info("profile trigger threshold reached", "user_id", userID, "l1_count", count)
	return p.GenerateProfile(ctx, userID)
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

	// Parse frontmatter and return content
	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) < 3 {
		// No frontmatter, return raw content
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
		// Skip profile files themselves
		if strings.HasPrefix(filepath.Base(filePath), "profile_") {
			continue
		}

		mf, err := parseMemoryFile(filePath)
		if err != nil {
			slog.Debug("skipping unparseable file", "path", filePath, "error", err)
			continue
		}

		// Check if this is a reflection for the target user
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

	// Check if profile already exists to preserve created_at
	if existing, err := parseMemoryFile(profilePath); err == nil {
		mf.CreatedAt = existing.CreatedAt
	}

	// Write file atomically
	if err := writeProfileAtomic(profilePath, mf); err != nil {
		return fmt.Errorf("write profile file: %w", err)
	}

	// Sync to memory_index
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
