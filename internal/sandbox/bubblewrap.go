package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Bubblewrap implements the Sandbox interface using Linux bubblewrap (bwrap).
// It creates lightweight user-namespace sandboxes with fine-grained filesystem
// and network isolation.
type Bubblewrap struct {
	cfg       Config
	available bool
}

// NewBubblewrap creates a Bubblewrap sandbox. It checks for bwrap at creation time.
func NewBubblewrap(cfg Config) *Bubblewrap {
	_, err := exec.LookPath("bwrap")
	return &Bubblewrap{
		cfg:       cfg,
		available: err == nil,
	}
}

func (b *Bubblewrap) Name() string    { return "bubblewrap" }
func (b *Bubblewrap) Available() bool { return b.available }

// Exec runs a command inside a bubblewrap sandbox with filesystem and network isolation.
func (b *Bubblewrap) Exec(ctx context.Context, command string, workDir string, opts ExecOptions) (*ExecResult, error) {
	if !b.available {
		return nil, fmt.Errorf("bubblewrap sandbox not available")
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	args := b.buildArgs(workDir, opts)
	args = append(args, "/bin/bash", "-c", command)

	cmd := exec.CommandContext(ctx, "bwrap", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("bwrap failed: %w", runErr)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// buildArgs constructs the bwrap command-line arguments.
func (b *Bubblewrap) buildArgs(workDir string, opts ExecOptions) []string {
	var args []string

	// Bind root filesystem read-only
	args = append(args, "--ro-bind", "/", "/")

	// Mount work directory read-write
	if workDir != "" {
		args = append(args, "--bind", workDir, workDir)
	}

	// Additional allowed paths (read-write)
	for _, p := range opts.AllowedPaths {
		args = append(args, "--bind", p, p)
	}

	// Additional read-only paths
	for _, p := range opts.ReadOnlyPaths {
		args = append(args, "--ro-bind", p, p)
	}

	// Config-level read-only dirs
	for _, p := range b.cfg.ReadonlyDirs {
		args = append(args, "--ro-bind", p, p)
	}

	// Temp filesystem
	args = append(args, "--tmpfs", "/tmp")

	// Mount /dev and /proc for basic operation
	args = append(args, "--dev", "/dev")
	args = append(args, "--proc", "/proc")

	// Network isolation
	if !opts.NetworkAllowed {
		args = append(args, "--unshare-net")
	}

	// Die with parent process
	args = append(args, "--die-with-parent")

	// Set working directory
	if workDir != "" {
		args = append(args, "--chdir", workDir)
	}

	return args
}
