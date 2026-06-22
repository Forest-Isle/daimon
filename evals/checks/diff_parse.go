// Package checks holds deterministic evals that grade a coding diff returned by
// a delegated coding agent. The package is standalone (no internal/* imports) so
// it can be reused as an acceptance gate wherever a diff and a test outcome are
// available.
package checks

import (
	"bufio"
	"strings"
)

// FileOp is the kind of change a unified diff makes to one file.
type FileOp int

const (
	// OpModified is an in-place content change (the default).
	OpModified FileOp = iota
	// OpAdded is a newly created file.
	OpAdded
	// OpDeleted is a removed file.
	OpDeleted
	// OpRenamed is a moved file (with or without content change).
	OpRenamed
)

// FileChange is one file's worth of parsed diff. AddedLines and RemovedLines
// hold the body content with the leading '+'/'-' stripped (header lines such as
// "+++ b/..." are never captured as body).
type FileChange struct {
	Path         string // new path (b/…); for pure deletes, the deleted path
	OldPath      string // source path for renames; "" otherwise
	Op           FileOp
	AddedLines   []string
	RemovedLines []string
	Binary       bool // true for "Binary files … differ"
}

// ParseUnifiedDiff parses a git-style unified diff into per-file changes. It is
// tolerant of multi-file diffs. An empty diff returns (nil, nil); the parser
// does not currently surface a structural error, but the signature reserves one
// so callers can fail closed on future stricter parsing.
func ParseUnifiedDiff(diff string) ([]FileChange, error) {
	if strings.TrimSpace(diff) == "" {
		return nil, nil
	}

	var (
		changes []FileChange
		cur     *FileChange
		inHunk  bool
	)
	flush := func() {
		if cur != nil {
			changes = append(changes, *cur)
			cur = nil
		}
	}

	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = newFileChange(line)
			inHunk = false
		case cur == nil:
			// Preamble before the first file header; ignore.
		case strings.HasPrefix(line, "new file mode"):
			cur.Op = OpAdded
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Op = OpDeleted
		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
			cur.Op = OpRenamed
		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
			cur.Op = OpRenamed
		case strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		case !inHunk && strings.HasPrefix(line, "--- "):
			applyOldHeader(cur, strings.TrimPrefix(line, "--- "))
		case !inHunk && strings.HasPrefix(line, "+++ "):
			applyNewHeader(cur, strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk:
			captureBody(cur, line)
		}
	}
	flush()
	return changes, sc.Err()
}

// newFileChange starts a FileChange from a "diff --git a/<old> b/<new>" header.
func newFileChange(line string) *FileChange {
	fc := &FileChange{Op: OpModified}
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		fc.OldPath = stripABPrefix(rest[:i])
		fc.Path = stripABPrefix(rest[i+1:])
	}
	return fc
}

// applyOldHeader handles a "--- " path header.
func applyOldHeader(fc *FileChange, p string) {
	if strings.TrimSpace(p) == "/dev/null" {
		fc.Op = OpAdded
		return
	}
	if v := stripABPrefix(p); v != "" {
		fc.OldPath = v
	}
}

// applyNewHeader handles a "+++ " path header.
func applyNewHeader(fc *FileChange, p string) {
	if strings.TrimSpace(p) == "/dev/null" {
		fc.Op = OpDeleted
		return
	}
	if v := stripABPrefix(p); v != "" {
		fc.Path = v
	}
}

// captureBody appends a hunk body line to the added or removed set.
func captureBody(fc *FileChange, line string) {
	switch {
	case strings.HasPrefix(line, "+"):
		fc.AddedLines = append(fc.AddedLines, line[1:])
	case strings.HasPrefix(line, "-"):
		fc.RemovedLines = append(fc.RemovedLines, line[1:])
	}
	// context, empty, and "\ No newline…" lines are ignored.
}

// stripABPrefix removes a leading "a/" or "b/" and surrounding whitespace.
func stripABPrefix(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "a/"):
		return s[2:]
	case strings.HasPrefix(s, "b/"):
		return s[2:]
	default:
		return s
	}
}
