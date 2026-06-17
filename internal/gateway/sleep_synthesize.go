package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/heart"
	"github.com/Forest-Isle/daimon/internal/sleep"
	"github.com/Forest-Isle/daimon/internal/vcs"
)

// feedbackCorrectionSource adapts the attention feedback store + heart event
// store to sleep.correctionSource: it loads recent corrections and joins each to
// its event's source/kind so the synthesize job can key a rule. The join lives
// here (the I/O boundary), keeping the sleep job pure.
type feedbackCorrectionSource struct {
	feedback *attention.FeedbackStore
	events   *heart.Store
}

func (s feedbackCorrectionSource) Corrections(ctx context.Context, limit int) ([]sleep.RoutingCorrection, error) {
	fbs, err := s.feedback.Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	if len(fbs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(fbs))
	for _, fb := range fbs {
		if fb.EventID != "" {
			ids = append(ids, fb.EventID)
		}
	}
	kinds, err := s.events.KindsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	var out []sleep.RoutingCorrection
	for _, fb := range fbs {
		if fb.ExpectedAction == "" || fb.ExpectedAction == fb.GivenAction {
			continue // not a correction: nothing was asked to change
		}
		ev, ok := kinds[fb.EventID]
		if !ok {
			continue // event no longer exists; cannot key a rule by source/kind
		}
		out = append(out, sleep.RoutingCorrection{
			EventID:  fb.EventID,
			Source:   ev.Source,
			Kind:     ev.Kind,
			Expected: fb.ExpectedAction,
		})
	}
	return out, nil
}

// rulesFileSink adapts the attention rules.yaml file to sleep.ruleSink. Reads are
// fail-loud on a malformed file so the synthesize job aborts rather than clobber
// hand-written rules; a missing file is an empty (not error) rule set.
type rulesFileSink struct {
	path string
}

func (s rulesFileSink) Existing(_ context.Context) ([]attention.Rule, error) {
	return readRulesFile(s.path)
}

func (s rulesFileSink) Append(ctx context.Context, candidates []attention.Rule) error {
	before := fileSignature(s.path)
	existing, err := readRulesFile(s.path)
	if err != nil {
		return err
	}
	merged := existing
	nAppended := 0
	for _, c := range candidates {
		if containsRule(merged, c) {
			continue
		}
		merged = append(merged, c)
		nAppended++
	}
	// Nothing new to add: leave the file (and any hand-written formatting,
	// comments, or ordering) untouched rather than re-marshalling it on every
	// cycle. This also keeps the git history to one commit per real change.
	if nAppended == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("ensure attention dir: %w", err)
	}
	data, err := yaml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshal attention rules: %w", err)
	}
	// Refuse to clobber a concurrent hand-edit: if the file changed since we read
	// it, abort and let the next cycle re-read the new content.
	if fileSignature(s.path) != before {
		return fmt.Errorf("attention rules changed during synthesis; skipping write to avoid clobber")
	}
	// Write atomically (temp + rename) so a crash mid-write cannot leave a
	// truncated, unparseable rules file.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".rules-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp rules file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp rules file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp rules file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace attention rules: %w", err)
	}
	// Best-effort: record the synthesis as a single revertable git commit so a
	// bad autonomous rule can be rolled back (`daimon attention revert`). A VCS
	// failure never fails synthesis — the rules file is already written.
	dir := filepath.Dir(s.path)
	if err := vcs.EnsureRepo(ctx, dir); err != nil {
		slog.Warn("attention rules git init failed", "path", s.path, "error", err)
		return nil
	}
	if _, _, err := vcs.Commit(ctx, dir, fmt.Sprintf("synthesize: %d attention rule(s)", nAppended), "rules.yaml"); err != nil {
		slog.Warn("attention rules git commit failed", "path", s.path, "error", err)
	}
	return nil
}

// fileSignature is a cheap change token (size + modtime in ns) for detecting a
// concurrent edit between read and write. Returns "" when the file is absent.
func fileSignature(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

func readRulesFile(path string) ([]attention.Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read attention rules: %w", err)
	}
	var rules []attention.Rule
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("parse attention rules: %w", err)
	}
	return rules, nil
}

func containsRule(rules []attention.Rule, c attention.Rule) bool {
	for _, r := range rules {
		if r.Source == c.Source && r.Kind == c.Kind && r.Contains == c.Contains && r.Action == c.Action {
			return true
		}
	}
	return false
}
