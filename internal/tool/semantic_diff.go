package tool

import (
	"context"
	"fmt"
	"strings"
)

// DiffResult is the structured result of a semantic diff.
type DiffResult struct {
	Hunks []DiffHunk `json:"hunks"`
	Stats DiffStats  `json:"stats"`
}

// DiffHunk represents a contiguous region of changed or contextual lines.
type DiffHunk struct {
	StartLineOrig int        `json:"start_line_orig"`
	EndLineOrig   int        `json:"end_line_orig"`
	StartLineNew  int        `json:"start_line_new"`
	EndLineNew    int        `json:"end_line_new"`
	Kind          string     `json:"kind"`
	Header        string     `json:"header"`
	Lines         []DiffLine `json:"lines"`
}

// DiffLine is a single diff line with old/new numbering.
type DiffLine struct {
	Kind       string `json:"kind"`
	Content    string `json:"content"`
	OldLineNum int    `json:"old_line_num"`
	NewLineNum int    `json:"new_line_num"`
}

// DiffStats summarizes file-level diff statistics.
type DiffStats struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

type diffOp struct {
	kind string
	line string
	old  int
	new  int
}

// Diff computes a line-based semantic diff using LCS and move detection.
func Diff(_ context.Context, filePath, original, modified string) (*DiffResult, error) {
	origLines := splitDiffLines(original)
	newLines := splitDiffLines(modified)
	ops := buildDiffOps(origLines, newLines)

	result := &DiffResult{
		Hunks: buildDiffHunks(filePath, ops),
		Stats: DiffStats{FilesChanged: 1},
	}
	for _, op := range ops {
		switch op.kind {
		case "add":
			result.Stats.Insertions++
		case "delete":
			result.Stats.Deletions++
		}
	}
	markMovedHunks(result.Hunks)
	return result, nil
}

func splitDiffLines(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func buildDiffOps(orig, modified []string) []diffOp {
	m, n := len(orig), len(modified)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if orig[i] == modified[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]diffOp, 0, m+n)
	i, j := 0, 0
	oldLine, newLine := 1, 1
	for i < m && j < n {
		if orig[i] == modified[j] {
			ops = append(ops, diffOp{kind: "context", line: orig[i], old: oldLine, new: newLine})
			i++
			j++
			oldLine++
			newLine++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffOp{kind: "delete", line: orig[i], old: oldLine})
			i++
			oldLine++
			continue
		}
		ops = append(ops, diffOp{kind: "add", line: modified[j], new: newLine})
		j++
		newLine++
	}
	for i < m {
		ops = append(ops, diffOp{kind: "delete", line: orig[i], old: oldLine})
		i++
		oldLine++
	}
	for j < n {
		ops = append(ops, diffOp{kind: "add", line: modified[j], new: newLine})
		j++
		newLine++
	}
	return ops
}

func buildDiffHunks(filePath string, ops []diffOp) []DiffHunk {
	hunks := make([]DiffHunk, 0)
	var current *DiffHunk

	flush := func() {
		if current == nil || len(current.Lines) == 0 {
			current = nil
			return
		}
		if current.Kind == "" {
			current.Kind = "modify"
		}
		hunks = append(hunks, *current)
		current = nil
	}

	for _, op := range ops {
		if op.kind == "context" {
			flush()
			continue
		}
		if current == nil {
			current = &DiffHunk{
				Kind:   op.kind,
				Header: fmt.Sprintf("%s", filePath),
			}
		}
		if current.Kind != op.kind && current.Kind != "modify" {
			current.Kind = "modify"
		}
		line := DiffLine{Kind: op.kind, Content: op.line, OldLineNum: op.old, NewLineNum: op.new}
		current.Lines = append(current.Lines, line)
		if op.old > 0 {
			if current.StartLineOrig == 0 {
				current.StartLineOrig = op.old
			}
			current.EndLineOrig = op.old
		}
		if op.new > 0 {
			if current.StartLineNew == 0 {
				current.StartLineNew = op.new
			}
			current.EndLineNew = op.new
		}
	}
	flush()
	return hunks
}

func markMovedHunks(hunks []DiffHunk) {
	for i := range hunks {
		if hunks[i].Kind != "delete" {
			continue
		}
		for j := range hunks {
			if hunks[j].Kind != "add" {
				continue
			}
			if blockSimilarity(hunks[i].Lines, hunks[j].Lines) > 0.8 {
				hunks[i].Kind = "moved"
				hunks[j].Kind = "moved"
				hunks[i].Header += " [moved]"
				hunks[j].Header += " [moved]"
				break
			}
		}
	}
}

func blockSimilarity(a, b []DiffLine) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	as := normalizeBlock(a)
	bs := normalizeBlock(b)
	if as == "" || bs == "" {
		return 0
	}
	if as == bs {
		return 1
	}
	aset := strings.Fields(as)
	bset := strings.Fields(bs)
	shared := 0
	seen := make(map[string]int, len(aset))
	for _, token := range aset {
		seen[token]++
	}
	for _, token := range bset {
		if seen[token] > 0 {
			shared++
			seen[token]--
		}
	}
	denom := len(aset)
	if len(bset) > denom {
		denom = len(bset)
	}
	if denom == 0 {
		return 0
	}
	return float64(shared) / float64(denom)
}

func normalizeBlock(lines []DiffLine) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimSpace(line.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}
