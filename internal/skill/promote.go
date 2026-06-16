package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ValidSlug rejects slugs that are empty or could escape a single directory
// level (path separators, "." or ".."). A draft slug must name exactly one
// directory under the staging/active root.
func ValidSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("empty slug")
	}
	if slug == "." || slug == ".." {
		return fmt.Errorf("invalid slug %q", slug)
	}
	if strings.Contains(slug, "/") || strings.ContainsRune(slug, os.PathSeparator) {
		return fmt.Errorf("invalid slug %q: must be a single directory name", slug)
	}
	return nil
}

// ensureNotSymlink refuses a path that is a symlink, so promotion/demotion never
// follows a link out of the staging/active root (§706 safety guard).
func ensureNotSymlink(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%q is a symlink; refusing", p)
	}
	return nil
}

// ensureWithinRoot verifies that p resolves to a location inside root after
// symlink evaluation, so a draft move cannot escape its tree (§706 safety guard).
func ensureWithinRoot(root, p string) error {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	if r, e := filepath.EvalSymlinks(rootAbs); e == nil {
		rootAbs = r
	}
	pAbs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if r, e := filepath.EvalSymlinks(pAbs); e == nil {
		pAbs = r
	}
	rel, err := filepath.Rel(rootAbs, pAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%q escapes %s", p, root)
	}
	return nil
}

// ValidateDraft parses a staged SKILL.md and confirms it is a usable distilled
// draft: a non-empty name, the distilled marker, and a non-empty body.
func ValidateDraft(skillMdPath string) (*Skill, error) {
	s, err := ParseSkill(skillMdPath)
	if err != nil {
		return nil, err
	}
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return nil, fmt.Errorf("missing name")
	}
	if !s.Metadata.Distilled {
		return nil, fmt.Errorf("not a distilled draft (metadata.distilled != true)")
	}
	body, err := s.Content()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("empty body")
	}
	return s, nil
}

// DraftInfo summarizes one staged draft for listing.
type DraftInfo struct {
	Slug        string
	Name        string
	Version     string
	Description string
	Distilled   bool
	Episodes    int
	Status      string
}

// ListDrafts enumerates the staged distilled drafts under stagingDir, sorted by
// slug. A missing staging directory yields no drafts (not an error).
func ListDrafts(stagingDir string) ([]DraftInfo, error) {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read staging dir: %w", err)
	}

	var drafts []DraftInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := DraftInfo{Slug: e.Name()}
		path := filepath.Join(stagingDir, e.Name(), "SKILL.md")
		s, err := ParseSkill(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				info.Status = "missing SKILL.md"
			} else {
				info.Status = err.Error()
			}
			drafts = append(drafts, info)
			continue
		}
		info.Name = s.Name
		info.Version = s.Version
		info.Description = s.Description
		info.Distilled = s.Metadata.Distilled
		info.Episodes = len(s.Metadata.SourceEpisodes)
		if _, err := ValidateDraft(path); err != nil {
			info.Status = err.Error()
		} else {
			info.Status = "valid"
		}
		drafts = append(drafts, info)
	}

	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].Slug < drafts[j].Slug
	})
	return drafts, nil
}

// PromoteDraft moves a validated distilled draft from stagingDir to activeDir,
// where it becomes an active prompt-reference skill. It refuses an invalid slug,
// a missing/invalid draft, a name already active, an existing target, and any
// symlinked source (§706 safety guards). Returns the promoted target directory.
func PromoteDraft(stagingDir, activeDir, slug string) (string, error) {
	if err := ValidSlug(slug); err != nil {
		return "", err
	}

	srcDir := filepath.Join(stagingDir, slug)
	src := filepath.Join(srcDir, "SKILL.md")
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("draft %q not found in %s", slug, stagingDir)
		}
		return "", fmt.Errorf("stat draft: %w", err)
	}

	draft, err := ValidateDraft(src)
	if err != nil {
		return "", err
	}

	mgr := New()
	if err := mgr.LoadBuiltin(); err != nil {
		return "", fmt.Errorf("load builtin skills: %w", err)
	}
	if err := mgr.LoadDir(activeDir); err != nil {
		return "", fmt.Errorf("load active skills: %w", err)
	}
	for _, active := range mgr.All() {
		if strings.TrimSpace(active.Name) == draft.Name {
			return "", fmt.Errorf("a skill named %q is already active", draft.Name)
		}
	}

	target := filepath.Join(activeDir, slug)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("%q already promoted (target dir exists)", slug)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat target: %w", err)
	}
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}
	if err := ensureNotSymlink(srcDir); err != nil {
		return "", err
	}
	if err := ensureNotSymlink(src); err != nil {
		return "", err
	}
	if err := ensureWithinRoot(stagingDir, src); err != nil {
		return "", err
	}
	if err := os.Rename(srcDir, target); err != nil {
		return "", fmt.Errorf("promote draft: %w", err)
	}
	return target, nil
}

// DemoteSkill returns an active distilled skill to the staging drafts. It refuses
// a non-distilled skill (use `daimon skill remove`), an already-staged draft, and
// any symlinked source (§706 safety guards).
func DemoteSkill(activeDir, stagingDir, slug string) error {
	if err := ValidSlug(slug); err != nil {
		return err
	}

	dir := filepath.Join(activeDir, slug)
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("skill %q not found in %s", slug, activeDir)
		}
		return fmt.Errorf("stat skill: %w", err)
	}
	s, err := ParseSkill(path)
	if err != nil {
		return err
	}
	if !s.Metadata.Distilled {
		return fmt.Errorf("%q is not a distilled skill; use `daimon skill remove`", slug)
	}
	if err := ensureNotSymlink(dir); err != nil {
		return err
	}
	if err := ensureNotSymlink(path); err != nil {
		return err
	}
	if err := ensureWithinRoot(activeDir, path); err != nil {
		return err
	}
	target := filepath.Join(stagingDir, slug)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("draft %q already staged", slug)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	if err := os.Rename(dir, target); err != nil {
		return fmt.Errorf("demote skill: %w", err)
	}
	return nil
}
