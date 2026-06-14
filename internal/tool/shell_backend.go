package tool

import (
	"bytes"
	"context"
	"fmt"
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
// configured default backend is "seatbelt". If the sandbox is required but
// unavailable (non-darwin, or sandbox-exec missing), the behavior splits by
// origin: a non-local trust boundary fails closed (the command is refused, never
// run on the host — running an untrusted-origin command unsandboxed would be a
// privilege escalation), while the local seatbelt opt-in degrades to the host
// with a warning (the interactive user is the origin, so keeping bash working is
// the safer tradeoff).
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
	nonLocal := b.nonLocalOrigin(ctx)
	if nonLocal || b.defaultSandbox {
		if b.sandbox != nil && b.sandbox.Available() {
			return b.sandbox.Run(ctx, command, workDir, streamCB)
		}
		// Sandbox required but unavailable. A non-local trust boundary did not
		// originate with the interactive user, so a host fallback would run an
		// untrusted-origin command unsandboxed — fail closed instead. Only the
		// local seatbelt opt-in (defaultSandbox with a local origin) may degrade
		// to the host.
		if nonLocal {
			return ShellRunResult{}, fmt.Errorf("bash: sandbox required for %s-origin command but unavailable; refusing host fallback",
				ChannelClassFromContext(ctx))
		}
		slog.Warn("bash: sandbox requested but unavailable; running on host",
			"channel_class", ChannelClassFromContext(ctx))
	}
	return b.host.Run(ctx, command, workDir, streamCB)
}

// nonLocalOrigin reports whether the call came from a trust boundary other than
// the local interactive user (remote/scheduled/internal/background). Such calls
// must be sandboxed and may never fall back to the host.
func (b *ChannelRoutingBackend) nonLocalOrigin(ctx context.Context) bool {
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
