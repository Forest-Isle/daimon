package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// FileGuard validates file paths against allowed directories.
type FileGuard struct {
	allowedDirs  []string
	readonlyDirs []string
}

// NewFileGuard creates a FileGuard. Empty allowed dirs means no restriction.
func NewFileGuard(allowed, readonly []string) (*FileGuard, error) {
	resolved := make([]string, 0, len(allowed))
	for _, d := range allowed {
		abs, err := resolveDir(d)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed dir %q: %w", d, err)
		}
		resolved = append(resolved, abs)
	}
	resolvedRO := make([]string, 0, len(readonly))
	for _, d := range readonly {
		abs, err := resolveDir(d)
		if err != nil {
			return nil, fmt.Errorf("resolve readonly dir %q: %w", d, err)
		}
		resolvedRO = append(resolvedRO, abs)
	}
	return &FileGuard{allowedDirs: resolved, readonlyDirs: resolvedRO}, nil
}

// ValidateAccess checks if a path is within allowed directories.
func (g *FileGuard) ValidateAccess(path string, write bool) error {
	if len(g.allowedDirs) == 0 {
		return nil
	}
	cleaned := filepath.Clean(path)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	resolved, err := resolvePathSafe(abs)
	if err != nil {
		resolved = abs
	}
	for _, dir := range g.allowedDirs {
		if isSubPath(dir, resolved) {
			if write {
				for _, ro := range g.readonlyDirs {
					if isSubPath(ro, resolved) {
						return fmt.Errorf("write denied: %s is in readonly directory %s", path, ro)
					}
				}
			}
			return nil
		}
	}
	return fmt.Errorf("access denied: %s is outside allowed directories", path)
}

func (g *FileGuard) AllowedDirs() []string  { return g.allowedDirs }
func (g *FileGuard) ReadonlyDirs() []string { return g.readonlyDirs }

func resolveDir(d string) (string, error) {
	abs, err := filepath.Abs(d)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func resolvePathSafe(abs string) (string, error) {
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	// Walk up to find the nearest existing ancestor, then reattach the tail.
	var tail []string
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...), nil
		}
	}
	return "", fmt.Errorf("cannot resolve any ancestor of %s", abs)
}

func isSubPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}
