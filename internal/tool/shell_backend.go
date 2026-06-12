package tool

import (
	"bytes"
	"context"
	"io"
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
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
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
