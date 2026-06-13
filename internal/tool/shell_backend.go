package tool

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os/exec"
)

type ShellRunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type ShellBackend interface {
	Available() bool
	Run(ctx context.Context, command, workDir string, streamCB StreamCallback) (ShellRunResult, error)
}

type HostShellBackend struct{}

func NewHostShellBackend() *HostShellBackend { return &HostShellBackend{} }

func (b *HostShellBackend) Available() bool {
	_, err := exec.LookPath("bash")
	return err == nil
}

func (b *HostShellBackend) Run(ctx context.Context, command, workDir string, streamCB StreamCallback) (ShellRunResult, error) {
	return runShellCommand(ctx, exec.CommandContext(ctx, "bash", "-c", command), workDir, streamCB)
}

// runShellCommand runs a prepared command and collects stdout/stderr/exit code,
// streaming stdout through streamCB when provided. Shared by the host and
// sandbox backends so they capture output identically.
func runShellCommand(ctx context.Context, cmd *exec.Cmd, workDir string, streamCB StreamCallback) (ShellRunResult, error) {
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stderr = &stderr

	var runErr error
	if streamCB != nil {
		stdoutPipe, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			return ShellRunResult{}, pipeErr
		}
		if startErr := cmd.Start(); startErr != nil {
			return ShellRunResult{}, startErr
		}
		_, _ = io.Copy(&streamingBufferWriter{buf: &stdout, cb: streamCB}, stdoutPipe)
		runErr = cmd.Wait()
	} else {
		cmd.Stdout = &stdout
		runErr = cmd.Run()
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			runErr = nil
		} else {
			exitCode = 1
		}
	}

	return ShellRunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, runErr
}

// ChannelRoutingBackend chooses between a host backend and a sandbox backend per
// call based on the trust boundary the command came from. Commands triggered by a
// remote, scheduled, internal, or background source — i.e. not the local
// interactive user — are forced into the sandbox, as is every command when the
// configured default backend is "seatbelt". If the sandbox is requested but
// unavailable (non-darwin, or sandbox-exec missing) it falls back to the host
// with a warning, so the agent keeps working rather than losing bash entirely.
type ChannelRoutingBackend struct {
	host           ShellBackend
	sandbox        ShellBackend
	defaultSandbox bool
}

// NewChannelRoutingBackend builds the routing backend. defaultSandbox forces the
// sandbox even for local interactive calls (config tools.exec.backend=seatbelt).
func NewChannelRoutingBackend(host, sandbox ShellBackend, defaultSandbox bool) *ChannelRoutingBackend {
	if host == nil {
		host = NewHostShellBackend()
	}
	return &ChannelRoutingBackend{host: host, sandbox: sandbox, defaultSandbox: defaultSandbox}
}

func (b *ChannelRoutingBackend) Available() bool { return b.host.Available() }

func (b *ChannelRoutingBackend) Run(ctx context.Context, command, workDir string, streamCB StreamCallback) (ShellRunResult, error) {
	if b.shouldSandbox(ctx) {
		if b.sandbox != nil && b.sandbox.Available() {
			return b.sandbox.Run(ctx, command, workDir, streamCB)
		}
		slog.Warn("bash: sandbox requested but unavailable; running on host",
			"channel_class", ChannelClassFromContext(ctx))
	}
	return b.host.Run(ctx, command, workDir, streamCB)
}

// shouldSandbox reports whether a call must be sandboxed: any non-local trust
// boundary, or the configured seatbelt default.
func (b *ChannelRoutingBackend) shouldSandbox(ctx context.Context) bool {
	if b.defaultSandbox {
		return true
	}
	switch ChannelClassFromContext(ctx) {
	case ToolChannelRemote, ToolChannelScheduled, ToolChannelInternal, ToolChannelBackground:
		return true
	default:
		return false
	}
}

type streamingBufferWriter struct {
	buf *bytes.Buffer
	cb  StreamCallback
}

func (w *streamingBufferWriter) Write(p []byte) (int, error) {
	if w.buf != nil {
		_, _ = w.buf.Write(p)
	}
	if w.cb != nil {
		w.cb(string(p))
	}
	return len(p), nil
}
