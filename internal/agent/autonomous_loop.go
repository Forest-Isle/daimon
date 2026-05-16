package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// DiscoveryOpportunity represents a single improvement opportunity found by the discovery loop.
type DiscoveryOpportunity struct {
	ID          string
	Title       string
	Description string
	FilePath    string
	LineNumber  int
	Category    DiscoveryCategory
	Severity    float64 // 0.0 (minor) to 1.0 (critical)
	Confidence  float64 // how sure we are this is a real issue
	Assignee    string  // suggested sub-agent to handle this
	Discovered  time.Time
	Status      DiscoveryStatus
}

// DiscoveryCategory classifies the type of improvement opportunity.
type DiscoveryCategory string

const (
	DiscoveryCategoryTODO       DiscoveryCategory = "todo"
	DiscoveryCategoryFIXME      DiscoveryCategory = "fixme"
	DiscoveryCategoryTestGap    DiscoveryCategory = "test_gap"
	DiscoveryCategoryLintIssue  DiscoveryCategory = "lint_issue"
	DiscoveryCategoryDeadCode   DiscoveryCategory = "dead_code"
	DiscoveryCategoryDepUpgrade DiscoveryCategory = "dep_upgrade"
	DiscoveryCategoryPerfIssue  DiscoveryCategory = "perf_issue"
	DiscoveryCategorySecurity   DiscoveryCategory = "security"
	DiscoveryCategoryRefactor   DiscoveryCategory = "refactor"
)

// DiscoveryStatus tracks lifecycle of a discovered opportunity.
type DiscoveryStatus string

const (
	DiscoveryStatusNew       DiscoveryStatus = "new"
	DiscoveryStatusClaimed   DiscoveryStatus = "claimed"
	DiscoveryStatusInProgress DiscoveryStatus = "in_progress"
	DiscoveryStatusCompleted DiscoveryStatus = "completed"
	DiscoveryStatusDismissed DiscoveryStatus = "dismissed"
)

// DiscoveryConfig tunes the autonomous discovery loop behavior.
type DiscoveryConfig struct {
	Enabled            bool              `yaml:"enabled"`
	ScanInterval       time.Duration     `yaml:"scan_interval"`        // how often to scan (default: 5m)
	MaxConcurrentFixes int               `yaml:"max_concurrent_fixes"` // max parallel fix attempts (default: 2)
	ScanDirs           []string          `yaml:"scan_dirs"`            // directories to scan (default: ["."])
	ExcludeDirs        []string          `yaml:"exclude_dirs"`         // directories to skip
	Categories         []DiscoveryCategory `yaml:"categories"`         // which categories to scan for
	MinSeverity        float64           `yaml:"min_severity"`         // minimum severity to auto-fix (default: 0.5)
	AutoFix            bool              `yaml:"auto_fix"`             // automatically dispatch fixes (default: false)
	MaxAutoFixesPerDay int               `yaml:"max_auto_fixes_per_day"` // safety cap
	RequireTests       bool              `yaml:"require_tests"`        // require test verification after fix
}

// DefaultDiscoveryConfig returns sensible defaults.
func DefaultDiscoveryConfig() DiscoveryConfig {
	return DiscoveryConfig{
		Enabled:            false,
		ScanInterval:       5 * time.Minute,
		MaxConcurrentFixes: 2,
		ScanDirs:           []string{"."},
		ExcludeDirs:        []string{".git", "node_modules", "vendor", ".idea", "dist", "build", "data"},
		Categories:         []DiscoveryCategory{DiscoveryCategoryTODO, DiscoveryCategoryFIXME, DiscoveryCategoryTestGap, DiscoveryCategoryDeadCode},
		MinSeverity:        0.5,
		AutoFix:            false,
		MaxAutoFixesPerDay: 10,
		RequireTests:       true,
	}
}

// AutonomousDiscoveryLoop continuously scans the project for improvement
// opportunities and optionally dispatches sub-agents to fix them.
// This is the "Devin-like" autonomous work-finding capability.
type AutonomousDiscoveryLoop struct {
	cfg          DiscoveryConfig
	toolRegistry *tool.Registry
	workDir      string

	opportunities map[string]*DiscoveryOpportunity
	scanCount     int
	fixesToday    int
	lastScanDay   string

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Callbacks for integration
	onDiscover  func(opp *DiscoveryOpportunity)
	onFixStart  func(opp *DiscoveryOpportunity)
	onFixDone   func(opp *DiscoveryOpportunity, err error)
}

// NewAutonomousDiscoveryLoop creates a new discovery loop.
func NewAutonomousDiscoveryLoop(cfg DiscoveryConfig, toolRegistry *tool.Registry, workDir string) *AutonomousDiscoveryLoop {
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 5 * time.Minute
	}
	if cfg.MaxConcurrentFixes <= 0 {
		cfg.MaxConcurrentFixes = 2
	}
	if cfg.MinSeverity <= 0 {
		cfg.MinSeverity = 0.5
	}
	return &AutonomousDiscoveryLoop{
		cfg:           cfg,
		toolRegistry:  toolRegistry,
		workDir:       workDir,
		opportunities: make(map[string]*DiscoveryOpportunity),
	}
}

// Start begins the discovery loop. It runs a scan immediately and then
// on the configured interval. Non-blocking — runs in background goroutines.
func (dl *AutonomousDiscoveryLoop) Start(ctx context.Context) error {
	if dl == nil || !dl.cfg.Enabled {
		return nil
	}

	dl.ctx, dl.cancel = context.WithCancel(ctx)

	dl.wg.Add(1)
	go dl.loop()

	slog.Info("autonomous discovery loop started",
		"interval", dl.cfg.ScanInterval,
		"scan_dirs", dl.cfg.ScanDirs,
		"auto_fix", dl.cfg.AutoFix,
	)
	return nil
}

// Stop gracefully stops the discovery loop.
func (dl *AutonomousDiscoveryLoop) Stop() {
	if dl == nil || dl.cancel == nil {
		return
	}
	dl.cancel()
	dl.wg.Wait()
	slog.Info("autonomous discovery loop stopped",
		"total_scans", dl.scanCount,
		"opportunities_found", len(dl.opportunities),
	)
}

// SetCallbacks registers integration callbacks.
func (dl *AutonomousDiscoveryLoop) SetCallbacks(onDiscover, onFixStart, onFixDone interface{}) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if fn, ok := onDiscover.(func(*DiscoveryOpportunity)); ok {
		dl.onDiscover = fn
	}
	if fn, ok := onFixStart.(func(*DiscoveryOpportunity)); ok {
		dl.onFixStart = fn
	}
	if fn, ok := onFixDone.(func(*DiscoveryOpportunity, error)); ok {
		dl.onFixDone = fn
	}
}

// RunOnce performs a single discovery scan and returns opportunities found.
func (dl *AutonomousDiscoveryLoop) RunOnce(ctx context.Context) ([]*DiscoveryOpportunity, error) {
	if dl == nil {
		return nil, fmt.Errorf("discovery loop not initialized")
	}

	var allOpps []*DiscoveryOpportunity

	for _, scanDir := range dl.cfg.ScanDirs {
		absDir := scanDir
		if !filepath.IsAbs(scanDir) {
			absDir = filepath.Join(dl.workDir, scanDir)
		}

		opps, err := dl.scanDirectory(ctx, absDir)
		if err != nil {
			slog.Warn("discovery: scan directory failed", "dir", absDir, "err", err)
			continue
		}
		allOpps = append(allOpps, opps...)
	}

	// Deduplicate and score
	allOpps = dl.deduplicate(allOpps)
	for _, opp := range allOpps {
		opp.Severity = dl.scoreSeverity(opp)
	}

	// Sort by severity descending
	sort.Slice(allOpps, func(i, j int) bool {
		return allOpps[i].Severity > allOpps[j].Severity
	})

	// Store and notify
	dl.mu.Lock()
	dl.scanCount++
	for _, opp := range allOpps {
		dl.opportunities[opp.ID] = opp
		if dl.onDiscover != nil {
			dl.onDiscover(opp)
		}
	}
	dl.mu.Unlock()

	if dl.cfg.AutoFix && len(allOpps) > 0 {
		dl.dispatchFixes(ctx, allOpps)
	}

	return allOpps, nil
}

// GetOpportunities returns all discovered opportunities, optionally filtered by status.
func (dl *AutonomousDiscoveryLoop) GetOpportunities(status DiscoveryStatus) []*DiscoveryOpportunity {
	dl.mu.RLock()
	defer dl.mu.RUnlock()

	var result []*DiscoveryOpportunity
	for _, opp := range dl.opportunities {
		if status == "" || opp.Status == status {
			result = append(result, opp)
		}
	}
	return result
}

// DismissOpportunity marks an opportunity as dismissed.
func (dl *AutonomousDiscoveryLoop) DismissOpportunity(id string) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if opp, ok := dl.opportunities[id]; ok {
		opp.Status = DiscoveryStatusDismissed
	}
}

// ClaimOpportunity claims an opportunity for a specific agent.
func (dl *AutonomousDiscoveryLoop) ClaimOpportunity(id string, agentName string) error {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	opp, ok := dl.opportunities[id]
	if !ok {
		return fmt.Errorf("opportunity %s not found", id)
	}
	if opp.Status != DiscoveryStatusNew {
		return fmt.Errorf("opportunity %s is already %s", id, opp.Status)
	}
	opp.Status = DiscoveryStatusClaimed
	opp.Assignee = agentName
	return nil
}

// ── Internal ────────────────────────────────────────────────────────────

func (dl *AutonomousDiscoveryLoop) loop() {
	defer dl.wg.Done()

	// Initial scan
	if _, err := dl.RunOnce(dl.ctx); err != nil {
		slog.Warn("discovery: initial scan failed", "err", err)
	}

	ticker := time.NewTicker(dl.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dl.ctx.Done():
			return
		case <-ticker.C:
			if _, err := dl.RunOnce(dl.ctx); err != nil {
				if dl.ctx.Err() != nil {
					return
				}
				slog.Warn("discovery: periodic scan failed", "err", err)
			}
		}
	}
}

func (dl *AutonomousDiscoveryLoop) scanDirectory(ctx context.Context, dir string) ([]*DiscoveryOpportunity, error) {
	var opps []*DiscoveryOpportunity

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil // skip inaccessible files
		}

		// Skip excluded dirs
		if info.IsDir() {
			for _, excl := range dl.cfg.ExcludeDirs {
				if info.Name() == excl || strings.HasPrefix(path, filepath.Join(dir, excl)) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Only scan source files
		if !isSourceFile(info.Name()) {
			return nil
		}

		fileOpps, scanErr := dl.scanFile(ctx, path)
		if scanErr != nil {
			slog.Debug("discovery: file scan error", "file", path, "err", scanErr)
			return nil
		}
		opps = append(opps, fileOpps...)
		return nil
	})

	return opps, err
}

var (
	todoPattern     = regexp.MustCompile(`(?i)//\s*TODO[(:]?\s*(.+)`)
	fixmePattern    = regexp.MustCompile(`(?i)//\s*FIXME[(:]?\s*(.+)`)
	deadCodePattern = regexp.MustCompile(`(?i)//\s*(DEPRECATED|UNUSED|DEAD CODE)[(:]?\s*(.+)`)
	perfPattern     = regexp.MustCompile(`(?i)//\s*(HACK|WORKAROUND|OPTIMIZE|SLOW)[(:]?\s*(.+)`)
)

func (dl *AutonomousDiscoveryLoop) scanFile(ctx context.Context, path string) ([]*DiscoveryOpportunity, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(content), "\n")
	var opps []*DiscoveryOpportunity

	for i, line := range lines {
		lineNum := i + 1

		// TODO patterns
		if matches := todoPattern.FindStringSubmatch(line); len(matches) >= 2 {
			desc := strings.TrimSpace(matches[1])
			if desc == "" {
				desc = "Unspecified TODO"
			}
			opps = append(opps, dl.newOpportunity(path, lineNum, DiscoveryCategoryTODO, desc, line))
		}

		// FIXME patterns
		if matches := fixmePattern.FindStringSubmatch(line); len(matches) >= 2 {
			desc := strings.TrimSpace(matches[1])
			if desc == "" {
				desc = "Unspecified FIXME"
			}
			opps = append(opps, dl.newOpportunity(path, lineNum, DiscoveryCategoryFIXME, desc, line))
		}

		// Dead code patterns
		if matches := deadCodePattern.FindStringSubmatch(line); len(matches) >= 3 {
			opps = append(opps, dl.newOpportunity(path, lineNum, DiscoveryCategoryDeadCode, strings.TrimSpace(matches[2]), line))
		}

		// Performance issue patterns
		if matches := perfPattern.FindStringSubmatch(line); len(matches) >= 3 {
			opps = append(opps, dl.newOpportunity(path, lineNum, DiscoveryCategoryPerfIssue, strings.TrimSpace(matches[2]), line))
		}
	}

	// Check for test gaps
	if testOpps := dl.detectTestGaps(path); len(testOpps) > 0 {
		opps = append(opps, testOpps...)
	}

	return opps, nil
}

func (dl *AutonomousDiscoveryLoop) newOpportunity(path string, line int, cat DiscoveryCategory, desc, rawLine string) *DiscoveryOpportunity {
	id := fmt.Sprintf("%s:%d:%s", filepath.Base(path), line, cat)
	return &DiscoveryOpportunity{
		ID:          id,
		Title:       fmt.Sprintf("[%s] %s:%d — %s", cat, filepath.Base(path), line, desc),
		Description: fmt.Sprintf("Found in %s at line %d: %q\nCategory: %s", path, line, strings.TrimSpace(rawLine), cat),
		FilePath:    path,
		LineNumber:  line,
		Category:    cat,
		Confidence:  0.85, // regex-based detection is fairly confident
		Discovered:  time.Now(),
		Status:      DiscoveryStatusNew,
	}
}

// detectTestGaps checks if a source file has a corresponding test file.
func (dl *AutonomousDiscoveryLoop) detectTestGaps(sourcePath string) []*DiscoveryOpportunity {
	base := filepath.Base(sourcePath)
	dir := filepath.Dir(sourcePath)

	// Check for standard Go test patterns
	testPatterns := []string{
		strings.TrimSuffix(base, ".go") + "_test.go",
		base[:len(base)-3] + "_test.go",
	}

	for _, testName := range testPatterns {
		testPath := filepath.Join(dir, testName)
		if _, err := os.Stat(testPath); os.IsNotExist(err) {
			// Found a source file without a test
			id := fmt.Sprintf("%s:test_gap", filepath.Base(sourcePath))
			return []*DiscoveryOpportunity{
				{
					ID:          id,
					Title:       fmt.Sprintf("[test_gap] %s has no corresponding test file", filepath.Base(sourcePath)),
					Description: fmt.Sprintf("Source file %s does not have a corresponding test file %s. Consider adding tests.", sourcePath, testName),
					FilePath:    sourcePath,
					Category:    DiscoveryCategoryTestGap,
					Confidence:  0.6, // file might not need tests (e.g., main.go)
					Severity:    0.3,
					Discovered:  time.Now(),
					Status:      DiscoveryStatusNew,
				},
			}
		}
	}
	return nil
}

func (dl *AutonomousDiscoveryLoop) scoreSeverity(opp *DiscoveryOpportunity) float64 {
	baseScore := 0.3

	switch opp.Category {
	case DiscoveryCategoryFIXME:
		baseScore = 0.75
	case DiscoveryCategorySecurity:
		baseScore = 0.9
	case DiscoveryCategoryTODO:
		baseScore = 0.35
	case DiscoveryCategoryTestGap:
		baseScore = 0.4
	case DiscoveryCategoryDeadCode:
		baseScore = 0.25
	case DiscoveryCategoryPerfIssue:
		baseScore = 0.6
	case DiscoveryCategoryDepUpgrade:
		baseScore = 0.45
	case DiscoveryCategoryLintIssue:
		baseScore = 0.5
	case DiscoveryCategoryRefactor:
		baseScore = 0.3
	}

	return baseScore * opp.Confidence
}

func (dl *AutonomousDiscoveryLoop) deduplicate(opps []*DiscoveryOpportunity) []*DiscoveryOpportunity {
	seen := make(map[string]bool)
	var result []*DiscoveryOpportunity
	for _, opp := range opps {
		if !seen[opp.ID] {
			seen[opp.ID] = true
			result = append(result, opp)
		}
	}
	return result
}

func (dl *AutonomousDiscoveryLoop) dispatchFixes(ctx context.Context, opps []*DiscoveryOpportunity) {
	today := time.Now().Format("2006-01-02")
	dl.mu.Lock()
	if dl.lastScanDay != today {
		dl.fixesToday = 0
		dl.lastScanDay = today
	}
	dl.mu.Unlock()

	dispatched := 0
	for _, opp := range opps {
		if opp.Severity < dl.cfg.MinSeverity {
			continue
		}

		dl.mu.Lock()
		if dl.fixesToday >= dl.cfg.MaxAutoFixesPerDay {
			dl.mu.Unlock()
			slog.Info("discovery: auto-fix daily cap reached", "cap", dl.cfg.MaxAutoFixesPerDay)
			break
		}
		dl.fixesToday++
		dl.mu.Unlock()

		opp.Status = DiscoveryStatusInProgress
		if dl.onFixStart != nil {
			dl.onFixStart(opp)
		}

		// The actual fix is dispatched by the cognitive agent which calls
		// ClaimOpportunity and spawns a sub-agent to handle the fix.
		slog.Info("discovery: auto-fix dispatched",
			"id", opp.ID,
			"title", opp.Title,
			"severity", opp.Severity,
		)

		dispatched++
	}

	if dispatched > 0 {
		slog.Info("discovery: auto-fixes dispatched", "count", dispatched, "total_today", dl.fixesToday)
	}
}

func isSourceFile(filename string) bool {
	ext := filepath.Ext(filename)
	switch ext {
	case ".go", ".java", ".py", ".ts", ".tsx", ".js", ".jsx", ".vue", ".rs", ".c", ".cpp", ".h", ".hpp":
		return true
	}
	// Also check for files like Dockerfile, Makefile, etc.
	base := filepath.Base(filename)
	sourceBases := map[string]bool{
		"Dockerfile":   true,
		"Makefile":     true,
		"CMakeLists.txt": true,
	}
	return sourceBases[base]
}
