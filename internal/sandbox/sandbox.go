package sandbox

import (
	"context"
	"runtime"
	"time"
)

// Sandbox is the unified interface for OS-level command sandboxing.
// Implementations include Docker, macOS Seatbelt, and Linux Bubblewrap.
type Sandbox interface {
	// Exec runs a command inside the sandbox and returns the result.
	Exec(ctx context.Context, command string, workDir string, opts ExecOptions) (*ExecResult, error)
	// Available reports whether this sandbox backend is usable on the current system.
	Available() bool
	// Name returns a human-readable identifier for this sandbox type.
	Name() string
}

// ExecResult holds the output of a sandboxed command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// ExecOptions configures per-invocation sandbox behavior.
type ExecOptions struct {
	Timeout        time.Duration
	AllowedPaths   []string
	ReadOnlyPaths  []string
	NetworkAllowed bool
	ProxyPort      int
}

// Config holds common sandbox configuration shared by all backends.
type Config struct {
	WorkDir      string
	AllowedDirs  []string
	ReadonlyDirs []string
	NetworkMode  string
	MemoryLimit  string
	CPULimit     string
	Image        string        // Docker image (only used by Docker backend)
	IdleTimeout  time.Duration // Docker session idle timeout
}

// NewAutoSandbox returns the best available sandbox for the current OS.
// Priority: darwin → Seatbelt, linux → Bubblewrap, fallback → Docker.
// Returns nil if no sandbox backend is available.
func NewAutoSandbox(cfg Config) Sandbox {
	switch runtime.GOOS {
	case "darwin":
		sb := NewSeatbelt(cfg)
		if sb.Available() {
			return sb
		}
	case "linux":
		bw := NewBubblewrap(cfg)
		if bw.Available() {
			return bw
		}
	}
	// Fallback to Docker — independent of any caller context.
	if ProbeDocker(context.Background()) {
		dockerCfg := DockerSessionConfig{
			Image:        cfg.Image,
			NetworkMode:  cfg.NetworkMode,
			MemoryLimit:  cfg.MemoryLimit,
			CPULimit:     cfg.CPULimit,
			AllowedDirs:  cfg.AllowedDirs,
			ReadonlyDirs: cfg.ReadonlyDirs,
			IdleTimeout:  cfg.IdleTimeout,
		}
		return NewDockerSandbox(dockerCfg)
	}
	return nil
}

// DockerSandbox wraps DockerSessionManager to implement the Sandbox interface.
type DockerSandbox struct {
	mgr       *DockerSessionManager
	sessionID string
}

// NewDockerSandbox creates a Docker-based sandbox implementing the Sandbox interface.
func NewDockerSandbox(cfg DockerSessionConfig) *DockerSandbox {
	mgr := NewDockerSessionManager(cfg, true)
	return &DockerSandbox{mgr: mgr, sessionID: "default"}
}

func (d *DockerSandbox) Name() string    { return "docker" }
func (d *DockerSandbox) Available() bool { return d.mgr.Available() }

func (d *DockerSandbox) Exec(ctx context.Context, command string, workDir string, opts ExecOptions) (*ExecResult, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	session, err := d.mgr.GetOrCreate(ctx, d.sessionID)
	if err != nil {
		return nil, err
	}
	stdout, stderr, exitCode, duration, err := session.Exec(ctx, command)
	if err != nil {
		return nil, err
	}
	return &ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}
