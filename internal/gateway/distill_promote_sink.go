package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Forest-Isle/daimon/internal/appdir"
)

type fileDraftSink struct {
	dir string
}

func newFileDraftSink(dir string) *fileDraftSink {
	return &fileDraftSink{dir: dir}
}

func (s *fileDraftSink) WriteDraft(_ context.Context, slug string, content []byte) (bool, error) {
	target := filepath.Clean(filepath.Join(s.dir, slug, "SKILL.md"))
	if err := ensureDraftWithinRoot(s.dir, target); err != nil {
		return false, err
	}
	if _, err := os.Stat(target); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat draft skill: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, fmt.Errorf("ensure draft skill dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".SKILL-*.md")
	if err != nil {
		return false, fmt.Errorf("create temp draft skill: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write temp draft skill: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("chmod temp draft skill: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temp draft skill: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return false, fmt.Errorf("replace draft skill: %w", err)
	}
	return true, nil
}

func defaultDistillStagingDir() string {
	return appdir.SkillsStagingDir()
}

// sameResolvedDir reports whether two directory paths point at the same location
// after abs/clean and best-effort symlink resolution. InitSkills uses it to keep
// the inert distill staging dir out of the active skill-load path even when an
// operator mistakenly lists it under skills.extra_dirs.
func sameResolvedDir(a, b string) bool {
	ra := resolveDir(a)
	rb := resolveDir(b)
	return ra != "" && ra == rb
}

func resolveDir(p string) string {
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return ""
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		return resolved
	}
	return abs
}

func ensureDraftWithinRoot(root, target string) error {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("draft sink: resolve root: %w", err)
	}
	// Resolve symlinks on the staging root when it already exists, so a root that
	// has been replaced by a symlink pointing into an active skills directory cannot
	// smuggle a draft out of the inert staging area and get it auto-loaded.
	if resolved, rerr := filepath.EvalSymlinks(rootAbs); rerr == nil {
		rootAbs = resolved
	}
	targetAbs, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("draft sink: resolve target: %w", err)
	}
	// Resolve symlinks on the existing portion of the target's parent so a symlinked
	// slug directory cannot escape either; the leaf (SKILL.md) need not exist yet.
	if resolvedParent, perr := filepath.EvalSymlinks(filepath.Dir(targetAbs)); perr == nil {
		targetAbs = filepath.Join(resolvedParent, filepath.Base(targetAbs))
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("draft sink: target %q escapes staging root", target)
	}
	return nil
}
