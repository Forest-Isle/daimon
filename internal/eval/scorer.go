// Package eval provides a deterministic, provider-agnostic harness for scoring
// agent task outcomes. The framework here is fully unit-testable with a mock
// agent and no network; real-LLM eval runs are driven by a separate command
// entry point that humans invoke manually.
package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Verdict is the outcome of applying a single Scorer to a task result.
type Verdict struct {
	Scorer string // name of the scorer that produced this verdict
	Passed bool
	Detail string // human-readable explanation, especially on failure
}

// Scorer evaluates a task result deterministically. workdir is the directory the
// task ran in; output is the agent's final textual output. Implementations must
// not depend on network access or wall-clock nondeterminism.
type Scorer interface {
	Name() string
	Score(workdir, output string) Verdict
}

// resolve interprets path as relative to workdir unless it is already absolute.
func resolve(workdir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workdir, path)
}

// FileExists passes when the target file is present.
type FileExists struct{ Path string }

func (s FileExists) Name() string { return "file_exists:" + s.Path }

func (s FileExists) Score(workdir, _ string) Verdict {
	p := resolve(workdir, s.Path)
	if _, err := os.Stat(p); err != nil {
		return Verdict{Scorer: s.Name(), Passed: false, Detail: "not found: " + p}
	}
	return Verdict{Scorer: s.Name(), Passed: true}
}

// FileContains passes when the target file exists and contains Substr.
type FileContains struct {
	Path   string
	Substr string
}

func (s FileContains) Name() string { return "file_contains:" + s.Path }

func (s FileContains) Score(workdir, _ string) Verdict {
	p := resolve(workdir, s.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return Verdict{Scorer: s.Name(), Passed: false, Detail: "read failed: " + err.Error()}
	}
	if !strings.Contains(string(data), s.Substr) {
		return Verdict{Scorer: s.Name(), Passed: false, Detail: "substring not found: " + s.Substr}
	}
	return Verdict{Scorer: s.Name(), Passed: true}
}

// OutputContains passes when the agent's final output contains Substr.
type OutputContains struct{ Substr string }

func (s OutputContains) Name() string { return "output_contains" }

func (s OutputContains) Score(_, output string) Verdict {
	if !strings.Contains(output, s.Substr) {
		return Verdict{Scorer: s.Name(), Passed: false, Detail: "substring not found: " + s.Substr}
	}
	return Verdict{Scorer: s.Name(), Passed: true}
}

// commandTimeout bounds how long a CommandSucceeds scorer waits.
const commandTimeout = 2 * time.Minute

// CommandSucceeds passes when running Command (via sh -c) in workdir exits 0.
// This is how build/test gates become eval signals (e.g. "go build ./...").
type CommandSucceeds struct{ Command string }

func (s CommandSucceeds) Name() string { return "command_succeeds:" + s.Command }

func (s CommandSucceeds) Score(workdir, _ string) Verdict {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", s.Command)
	if workdir != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := err.Error()
		if len(out) > 0 {
			detail += ": " + strings.TrimSpace(string(out))
		}
		return Verdict{Scorer: s.Name(), Passed: false, Detail: detail}
	}
	return Verdict{Scorer: s.Name(), Passed: true}
}
