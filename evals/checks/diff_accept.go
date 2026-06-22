package checks

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Gate names, in evaluation order.
const (
	gateTestsGreen   = "tests-green"
	gateNoTestTamper = "no-test-tamper"
	gateInScope      = "in-scope"
)

var (
	testDeclRe        = regexp.MustCompile(`^func (Test|Benchmark|Fuzz)\w*\(`)
	assertRe          = regexp.MustCompile(`t\.Error|t\.Errorf|t\.Fatal|t\.Fatalf|t\.FailNow|require\.|assert\.`)
	skipRe            = regexp.MustCompile(`\.Skip(Now)?\(`)
	buildConstraintRe = regexp.MustCompile(`^(//go:build|// \+build)`)
)

// TestOutcome is the result of running the required test set (e.g. via the
// test_run tool). Ran=false fails the tests-green gate closed.
type TestOutcome struct {
	Ran     bool
	Success bool
	Failed  int
}

// DiffAcceptInput is everything the acceptance gate needs. AllowedPaths are
// path.Match globs ('/'-separated); an empty slice disables the in-scope gate.
type DiffAcceptInput struct {
	Diff         string
	AllowedPaths []string
	Tests        TestOutcome
}

// GateResult is one gate's pass/fail with a human-actionable reason on failure.
type GateResult struct {
	Name   string
	Pass   bool
	Reason string
}

// DiffVerdict is the acceptance decision. Accept is true iff every gate passed.
// Gates is always the three gates in order: tests-green, no-test-tamper,
// in-scope.
type DiffVerdict struct {
	Accept bool
	Gates  []GateResult
}

// EvaluateDiff runs the three deterministic acceptance gates. It fails closed: a
// parse error or un-run tests reject. An error is returned only when the diff
// cannot be parsed, in which case every gate is failed with the parse reason.
func EvaluateDiff(in DiffAcceptInput) (DiffVerdict, error) {
	tests := evalTestsGreen(in.Tests)

	changes, err := ParseUnifiedDiff(in.Diff)
	if err != nil {
		reason := fmt.Sprintf("diff parse failed: %v", err)
		return DiffVerdict{
			Accept: false,
			Gates: []GateResult{
				tests,
				{Name: gateNoTestTamper, Pass: false, Reason: reason},
				{Name: gateInScope, Pass: false, Reason: reason},
			},
		}, err
	}

	tamper := evalNoTestTamper(changes)
	scope := evalInScope(changes, in.AllowedPaths)
	return DiffVerdict{
		Accept: tests.Pass && tamper.Pass && scope.Pass,
		Gates:  []GateResult{tests, tamper, scope},
	}, nil
}

// evalTestsGreen passes only when the required test set ran and was fully green.
func evalTestsGreen(t TestOutcome) GateResult {
	switch {
	case !t.Ran:
		return GateResult{Name: gateTestsGreen, Pass: false, Reason: "tests not run"}
	case !t.Success:
		if t.Failed > 0 {
			return GateResult{Name: gateTestsGreen, Pass: false, Reason: fmt.Sprintf("%d test(s) failing", t.Failed)}
		}
		return GateResult{Name: gateTestsGreen, Pass: false, Reason: "test run reported failure"}
	case t.Failed > 0:
		return GateResult{Name: gateTestsGreen, Pass: false, Reason: fmt.Sprintf("%d test(s) failing", t.Failed)}
	default:
		return GateResult{Name: gateTestsGreen, Pass: true}
	}
}

// evalNoTestTamper is the antihack gate. It guards reward hacking, so it errs
// strict. It catches STRUCTURAL tampering: a deleted test file, a rename that
// drops the _test.go suffix, a net removal of test declarations, an added skip
// or build constraint, or a net removal of assertions. It deliberately does NOT
// attempt to catch SEMANTIC weakening that preserves line structure (e.g.
// wrapping a check in `if false {`, relaxing an expected value to `want := got`,
// or emptying a table without touching assertion lines) — those slip any
// line-count heuristic and are left to the human merge sign-off and an optional
// LLM-judge layer above this gate.
func evalNoTestTamper(changes []FileChange) GateResult {
	for _, fc := range changes {
		if !isTestFile(fc) {
			continue
		}
		if reason := tamperReason(fc); reason != "" {
			return GateResult{Name: gateNoTestTamper, Pass: false, Reason: reason}
		}
	}
	return GateResult{Name: gateNoTestTamper, Pass: true}
}

// tamperReason returns the first tamper signal for a test file, or "" if clean.
func tamperReason(fc FileChange) string {
	p := testFilePath(fc)
	if fc.Op == OpDeleted {
		return "test file deleted: " + p
	}
	// A rename that drops the _test.go suffix removes the file from `go test`.
	if hasTestSuffix(fc.OldPath) && !hasTestSuffix(fc.Path) {
		return "test file renamed to non-test: " + fc.OldPath + " -> " + fc.Path
	}
	if netCount(fc.RemovedLines, fc.AddedLines, testDeclRe) > 0 {
		return "test function(s) removed: " + p
	}
	for _, l := range fc.AddedLines {
		t := strings.TrimSpace(l)
		if skipRe.MatchString(t) {
			return "test skip added: " + p
		}
		if buildConstraintRe.MatchString(t) {
			return "test build constraint changed: " + p
		}
	}
	if netCount(fc.RemovedLines, fc.AddedLines, assertRe) > 0 {
		return "assertions weakened/removed: " + p
	}
	return ""
}

// evalInScope passes when every changed file matches at least one allowed glob.
// An empty allowlist disables the gate. Renames must be in scope on both sides.
// Globs use path.Match semantics, which are NON-recursive: `*` does not cross
// `/`, so `internal/foo/*.go` matches `internal/foo/x.go` but not
// `internal/foo/bar/x.go`. List each directory level explicitly when a subtree
// must be covered.
func evalInScope(changes []FileChange, allowed []string) GateResult {
	if len(allowed) == 0 {
		return GateResult{Name: gateInScope, Pass: true}
	}
	if bad := invalidGlob(allowed); bad != "" {
		return GateResult{Name: gateInScope, Pass: false, Reason: "invalid scope pattern: " + bad}
	}
	var violators []string
	for _, fc := range changes {
		for _, p := range changedPaths(fc) {
			if !matchesAny(p, allowed) {
				violators = append(violators, p)
			}
		}
	}
	if len(violators) > 0 {
		return GateResult{Name: gateInScope, Pass: false, Reason: "out-of-scope file(s): " + strings.Join(violators, ", ")}
	}
	return GateResult{Name: gateInScope, Pass: true}
}

// netCount returns (matches in removed) - (matches in added) for a pattern.
func netCount(removed, added []string, re *regexp.Regexp) int {
	return countMatches(removed, re) - countMatches(added, re)
}

// countMatches counts lines matching re.
func countMatches(lines []string, re *regexp.Regexp) int {
	n := 0
	for _, l := range lines {
		if re.MatchString(strings.TrimSpace(l)) {
			n++
		}
	}
	return n
}

// matchesAny reports whether candidate matches any glob via path.Match.
func matchesAny(candidate string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, candidate); ok {
			return true
		}
	}
	return false
}

// invalidGlob returns the first malformed glob pattern, or "" if all are valid.
func invalidGlob(globs []string) string {
	for _, g := range globs {
		if _, err := path.Match(g, ""); err == path.ErrBadPattern {
			return g
		}
	}
	return ""
}

// isTestFile reports whether a change touches a Go test file on either side, so
// a rename that drops the _test.go suffix is still inspected for tampering.
func isTestFile(fc FileChange) bool {
	return hasTestSuffix(fc.Path) || hasTestSuffix(fc.OldPath)
}

// hasTestSuffix reports whether p names a Go test file.
func hasTestSuffix(p string) bool {
	return p != "" && strings.HasSuffix(path.Base(p), "_test.go")
}

// testFilePath returns the most relevant path for reporting a test file.
func testFilePath(fc FileChange) string {
	if fc.Path != "" {
		return fc.Path
	}
	return fc.OldPath
}

// changedPaths returns the path(s) an in-scope check must validate.
func changedPaths(fc FileChange) []string {
	var ps []string
	if fc.Path != "" {
		ps = append(ps, fc.Path)
	}
	if fc.OldPath != "" && fc.OldPath != fc.Path {
		ps = append(ps, fc.OldPath)
	}
	return ps
}
