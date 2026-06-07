package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Report aggregates the results of running a suite of tasks.
type Report struct {
	Total    int
	Passed   int
	PassRate float64
	Results  []Result
}

// NewReport aggregates a slice of Results into pass counts and a pass rate.
func NewReport(results []Result) Report {
	rep := Report{Total: len(results), Results: results}
	for _, r := range results {
		if r.Passed {
			rep.Passed++
		}
	}
	if rep.Total > 0 {
		rep.PassRate = float64(rep.Passed) / float64(rep.Total)
	}
	return rep
}

// String renders a human-readable summary: a header line with the pass count,
// then one line per task (failures first) with score and any failing scorers.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "eval: %d/%d passed (%.0f%%)\n", r.Passed, r.Total, r.PassRate*100)

	results := append([]Result(nil), r.Results...)
	sort.SliceStable(results, func(i, j int) bool {
		// Failures first, then by task ID for stable output.
		if results[i].Passed != results[j].Passed {
			return !results[i].Passed
		}
		return results[i].TaskID < results[j].TaskID
	})

	for _, res := range results {
		mark := "PASS"
		if !res.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "  [%s] %s (%.0f%%)\n", mark, res.TaskID, res.Score*100)
		for _, v := range res.Verdicts {
			if !v.Passed {
				fmt.Fprintf(&b, "        ✗ %s: %s\n", v.Scorer, v.Detail)
			}
		}
	}
	return b.String()
}

// LoadSuite loads every *.json file in dir as a Task, sorted by filename for
// deterministic ordering. Non-json files are ignored.
func LoadSuite(dir string) ([]*Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read suite dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var tasks []*Task
	for _, name := range names {
		t, err := LoadTask(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
