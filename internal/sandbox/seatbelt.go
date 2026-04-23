package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Seatbelt implements the Sandbox interface using macOS sandbox-exec (Seatbelt).
// It generates SBPL (Sandbox Profile Language) profiles to restrict file, network,
// and process access for each command execution.
type Seatbelt struct {
	cfg       Config
	available bool
}

// NewSeatbelt creates a Seatbelt sandbox. It checks for /usr/bin/sandbox-exec at creation time.
func NewSeatbelt(cfg Config) *Seatbelt {
	_, err := exec.LookPath("sandbox-exec")
	return &Seatbelt{
		cfg:       cfg,
		available: err == nil,
	}
}

func (s *Seatbelt) Name() string   { return "seatbelt" }
func (s *Seatbelt) Available() bool { return s.available }

// Exec runs a command inside a macOS sandbox-exec sandbox with a generated SBPL profile.
func (s *Seatbelt) Exec(ctx context.Context, command string, workDir string, opts ExecOptions) (*ExecResult, error) {
	if !s.available {
		return nil, fmt.Errorf("seatbelt sandbox not available")
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	profile := s.generateProfile(workDir, opts)

	cmd := exec.CommandContext(ctx, "sandbox-exec", "-p", profile, "/bin/bash", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}

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
			return nil, fmt.Errorf("sandbox-exec failed: %w", runErr)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// generateProfile builds an SBPL profile string for sandbox-exec.
func (s *Seatbelt) generateProfile(workDir string, opts ExecOptions) string {
	var b strings.Builder

	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")

	// Allow reading system libraries and frameworks
	b.WriteString("(allow file-read*\n")
	b.WriteString("  (subpath \"/usr/lib\")\n")
	b.WriteString("  (subpath \"/usr/share\")\n")
	b.WriteString("  (subpath \"/Library\")\n")
	b.WriteString("  (subpath \"/System\")\n")
	b.WriteString("  (subpath \"/private/var\")\n")
	b.WriteString("  (subpath \"/usr/local\")\n")
	b.WriteString("  (subpath \"/opt/homebrew\")\n")
	b.WriteString(")\n")

	// Allow read/write to work directory
	if workDir != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* (subpath \"%s\"))\n", escapeSBPL(workDir)))
	}

	// Allow read/write to additional allowed paths
	for _, p := range opts.AllowedPaths {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* (subpath \"%s\"))\n", escapeSBPL(p)))
	}

	// Allow read-only paths
	for _, p := range opts.ReadOnlyPaths {
		b.WriteString(fmt.Sprintf("(allow file-read* (subpath \"%s\"))\n", escapeSBPL(p)))
	}
	// Config-level read-only dirs
	for _, p := range s.cfg.ReadonlyDirs {
		b.WriteString(fmt.Sprintf("(allow file-read* (subpath \"%s\"))\n", escapeSBPL(p)))
	}

	// Allow temp directory access
	b.WriteString("(allow file-read* file-write* (subpath \"/tmp\") (subpath \"/private/tmp\"))\n")

	// Allow basic process execution
	b.WriteString("(allow process-exec\n")
	b.WriteString("  (literal \"/bin/bash\")\n")
	b.WriteString("  (literal \"/bin/sh\")\n")
	b.WriteString("  (literal \"/usr/bin/env\")\n")
	b.WriteString("  (subpath \"/usr/bin\")\n")
	b.WriteString("  (subpath \"/usr/local/bin\")\n")
	b.WriteString("  (subpath \"/opt/homebrew/bin\")\n")
	b.WriteString(")\n")

	// Allow process forking and basic system calls
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")

	// Network access
	if opts.NetworkAllowed {
		b.WriteString("(allow network*)\n")
	} else if opts.ProxyPort > 0 {
		// Allow only localhost connections on the proxy port
		b.WriteString(fmt.Sprintf("(allow network* (remote ip \"localhost:%d\"))\n", opts.ProxyPort))
	}
	// Always allow Unix domain sockets for basic operations
	b.WriteString("(allow network-outbound (literal \"/var/run/syslog\"))\n")

	return b.String()
}

// escapeSBPL escapes special characters in SBPL string literals.
func escapeSBPL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
