// Package score is the presentation and persistence layer of the eval harness:
// it holds the comparable snapshot of one run (Scorecard), persists it for
// run-over-run delta comparison, and renders a human-readable table. It is a
// pure leaf (stdlib only) with no dependency on the graded agent.
package score

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"
)

// Scorecard is the persisted, comparable snapshot of one eval run. Field names
// are stable JSON keys so an older .last_score.json stays decodable.
type Scorecard struct {
	Sessions         int            `json:"sessions"`
	ToolCalls        int            `json:"tool_calls"`
	Failures         int            `json:"failures"`
	GovernanceDenied int            `json:"governance_denied"`
	AgentError       int            `json:"agent_error"`
	EnvError         int            `json:"env_error"`
	Salvaged         int            `json:"salvaged"`
	DeniedByTool     map[string]int `json:"denied_by_tool,omitempty"`
}

// Load reads a scorecard from path. The boolean is false (with a nil error)
// when the file does not exist yet, so a first run has no baseline to diff.
func Load(path string) (Scorecard, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Scorecard{}, false, nil
	}
	if err != nil {
		return Scorecard{}, false, fmt.Errorf("read scorecard: %w", err)
	}
	var sc Scorecard
	if err := json.Unmarshal(b, &sc); err != nil {
		return Scorecard{}, false, fmt.Errorf("decode scorecard: %w", err)
	}
	return sc, true, nil
}

// Save writes the scorecard to path as indented JSON.
func Save(path string, sc Scorecard) error {
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scorecard: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write scorecard: %w", err)
	}
	return nil
}

// Render writes a metric table to w. When prev is non-nil, a signed Δ column
// shows the change versus the previous run (the "change one line, see the score
// move" mechanism). Below the table it lists governance denials by tool — the
// FM-1 signal.
func Render(w io.Writer, cur Scorecard, prev *Scorecard) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "METRIC\tVALUE\tΔ")
	for _, row := range metricRows(cur) {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", row.name, row.val, deltaStr(row.val, row.name, prev))
	}
	tw.Flush()

	if len(cur.DeniedByTool) > 0 {
		fmt.Fprintln(w, "\ngovernance denials by tool (FM-1):")
		for _, kv := range sortedCounts(cur.DeniedByTool) {
			fmt.Fprintf(w, "  %-12s %d\n", kv.key, kv.val)
		}
	}
}

type metricRow struct {
	name string
	val  int
}

func metricRows(sc Scorecard) []metricRow {
	return []metricRow{
		{"sessions", sc.Sessions},
		{"tool_calls", sc.ToolCalls},
		{"failures", sc.Failures},
		{"governance_denied", sc.GovernanceDenied},
		{"agent_error", sc.AgentError},
		{"env_error", sc.EnvError},
		{"salvaged", sc.Salvaged},
	}
}

// deltaStr returns the signed change for a named metric versus prev, or "-" when
// there is no baseline.
func deltaStr(cur int, name string, prev *Scorecard) string {
	if prev == nil {
		return "-"
	}
	d := cur - metricValue(*prev, name)
	switch {
	case d > 0:
		return fmt.Sprintf("+%d", d)
	case d < 0:
		return fmt.Sprintf("%d", d)
	default:
		return "0"
	}
}

func metricValue(sc Scorecard, name string) int {
	for _, r := range metricRows(sc) {
		if r.name == name {
			return r.val
		}
	}
	return 0
}

type countKV struct {
	key string
	val int
}

// sortedCounts returns map entries ordered by descending count, then by key, so
// rendering is deterministic.
func sortedCounts(m map[string]int) []countKV {
	out := make([]countKV, 0, len(m))
	for k, v := range m {
		out = append(out, countKV{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].val != out[j].val {
			return out[i].val > out[j].val
		}
		return out[i].key < out[j].key
	})
	return out
}
