package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ProjectContextScanner detects project metadata from the working directory.
type ProjectContextScanner struct {
	mu    sync.Mutex
	cache map[string]*ProjectContext
}

// NewProjectContextScanner creates a new scanner with an empty cache.
func NewProjectContextScanner() *ProjectContextScanner {
	return &ProjectContextScanner{cache: make(map[string]*ProjectContext)}
}

// Scan inspects dir for project manifest files and returns a ProjectContext,
// or nil if no recognised project is detected.
func (s *ProjectContextScanner) Scan(dir string) *ProjectContext {
	s.mu.Lock()
	if cached, ok := s.cache[dir]; ok {
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	ctx := s.scan(dir)

	s.mu.Lock()
	s.cache[dir] = ctx
	s.mu.Unlock()
	return ctx
}

// Invalidate removes the cached entry for dir.
func (s *ProjectContextScanner) Invalidate(dir string) {
	s.mu.Lock()
	delete(s.cache, dir)
	s.mu.Unlock()
}

func (s *ProjectContextScanner) scan(dir string) *ProjectContext {
	pc := &ProjectContext{}
	detected := scanGoMod(dir, pc)
	if scanPackageJSON(dir, pc) {
		detected = true
	}
	if scanCargoToml(dir, pc) {
		detected = true
	}
	if scanPyprojectToml(dir, pc) {
		detected = true
	}
	if scanMakefile(dir, pc) {
		detected = true
	}

	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		pc.HasReadme = true
		detected = true
	}

	pc.KeyDirectories = scanKeyDirectories(dir)

	if !detected && len(pc.KeyDirectories) == 0 {
		return nil
	}

	pc.RawContent = formatProjectContext(pc)
	return pc
}

// --- manifest scanners ---

var (
	goModuleRe   = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	goRequireRe  = regexp.MustCompile(`(?m)^\s+([^\s]+)\s+v([^\s]+)`)
	goIndirectRe = regexp.MustCompile(`(?m)^\s+([^\s]+)\s+v([^\s]+)\s+//\s*indirect`)
)

func scanGoMod(dir string, pc *ProjectContext) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	pc.Language = "go"
	if m := goModuleRe.FindSubmatch(data); len(m) == 2 {
		pc.Name = string(m[1])
	}

	// Parse direct and indirect dependencies
	directDeps := make(map[string]string)
	indirectDeps := make(map[string]string)

	for _, m := range goRequireRe.FindAllStringSubmatch(string(data), -1) {
		if len(m) == 3 {
			directDeps[m[1]] = m[2]
		}
	}
	// Indirect deps override any overlapping direct entries
	for _, m := range goIndirectRe.FindAllStringSubmatch(string(data), -1) {
		if len(m) == 3 {
			delete(directDeps, m[1])
			indirectDeps[m[1]] = m[2]
		}
	}

	pc.Dependencies = make([]ProjectDependency, 0, len(directDeps)+len(indirectDeps))
	for mod, ver := range directDeps {
		pc.Dependencies = append(pc.Dependencies, ProjectDependency{
			Name:    mod,
			Version: ver,
			Direct:  true,
		})
	}
	for mod, ver := range indirectDeps {
		pc.Dependencies = append(pc.Dependencies, ProjectDependency{
			Name:    mod,
			Version: ver,
			Direct:  false,
		})
	}

	pc.BuildCommands = appendUnique(pc.BuildCommands, "go build ./...", "go test ./...")
	return true
}

func scanPackageJSON(dir string, pc *ProjectContext) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	if pc.Language == "" {
		pc.Language = "javascript"
	}

	var pkg struct {
		Name    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return true
	}
	if pc.Name == "" && pkg.Name != "" {
		pc.Name = pkg.Name
	}
	for _, key := range []string{"build", "test", "dev"} {
		if cmd, ok := pkg.Scripts[key]; ok {
			pc.BuildCommands = appendUnique(pc.BuildCommands, fmt.Sprintf("npm run %s (%s)", key, cmd))
		}
	}
	return true
}

var cargoNameRe = regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)

func scanCargoToml(dir string, pc *ProjectContext) bool {
	data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return false
	}
	if pc.Language == "" {
		pc.Language = "rust"
	}
	if m := cargoNameRe.FindSubmatch(data); len(m) == 2 && pc.Name == "" {
		pc.Name = string(m[1])
	}
	pc.BuildCommands = appendUnique(pc.BuildCommands, "cargo build", "cargo test")
	return true
}

var pyNameRe = regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)

func scanPyprojectToml(dir string, pc *ProjectContext) bool {
	data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return false
	}
	if pc.Language == "" {
		pc.Language = "python"
	}
	if m := pyNameRe.FindSubmatch(data); len(m) == 2 && pc.Name == "" {
		pc.Name = string(m[1])
	}
	pc.BuildCommands = appendUnique(pc.BuildCommands, "python -m pytest")
	return true
}

var makeTargetRe = regexp.MustCompile(`(?m)^([a-zA-Z_][a-zA-Z0-9_-]*):`)

func scanMakefile(dir string, pc *ProjectContext) bool {
	f, err := os.Open(filepath.Join(dir, "Makefile"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	interesting := map[string]bool{
		"build": true, "test": true, "lint": true, "run": true,
		"fmt": true, "dev": true, "install": true, "clean": true,
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if m := makeTargetRe.FindStringSubmatch(scanner.Text()); len(m) == 2 {
			target := m[1]
			if interesting[target] {
				pc.BuildCommands = appendUnique(pc.BuildCommands, "make "+target)
			}
		}
	}
	return len(pc.BuildCommands) > 0 || pc.Language != ""
}

// --- helpers ---

var keyDirNames = []string{"cmd", "src", "internal", "pkg", "lib", "app", "test", "tests"}

func scanKeyDirectories(dir string) []string {
	var found []string
	for _, name := range keyDirNames {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && info.IsDir() {
			found = append(found, name)
		}
	}
	return found
}

func appendUnique(slice []string, vals ...string) []string {
	set := make(map[string]bool, len(slice))
	for _, s := range slice {
		set[s] = true
	}
	for _, v := range vals {
		if !set[v] {
			slice = append(slice, v)
			set[v] = true
		}
	}
	return slice
}

func formatProjectContext(pc *ProjectContext) string {
	var sb strings.Builder
	if pc.Name != "" {
		sb.WriteString("Project: " + pc.Name + "\n")
	}
	if pc.Language != "" {
		sb.WriteString("Language: " + pc.Language + "\n")
	}
	if len(pc.BuildCommands) > 0 {
		sb.WriteString("Build/Test commands:\n")
		for _, c := range pc.BuildCommands {
			sb.WriteString("  - " + c + "\n")
		}
	}
	if len(pc.Dependencies) > 0 {
		sb.WriteString("Dependencies:\n")
		directCount := 0
		for _, d := range pc.Dependencies {
			if d.Direct {
				directCount++
				sb.WriteString(fmt.Sprintf("  - %s@%s\n", d.Name, d.Version))
			}
		}
		indirectCount := len(pc.Dependencies) - directCount
		if indirectCount > 0 {
			sb.WriteString(fmt.Sprintf("  (+ %d indirect)\n", indirectCount))
		}
	}
	if len(pc.KeyDirectories) > 0 {
		sb.WriteString("Key directories: " + strings.Join(pc.KeyDirectories, ", ") + "\n")
	}
	if pc.HasReadme {
		sb.WriteString("Has README: yes\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
