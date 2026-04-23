package sandbox

import (
	"runtime"
	"testing"
)

func TestNewAutoSandbox_ReturnsNonNilOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping macOS-specific test")
	}
	cfg := Config{WorkDir: "/tmp/test"}
	sb := NewAutoSandbox(cfg)
	// On macOS, should return seatbelt if sandbox-exec is present
	if sb == nil {
		t.Log("no sandbox backend available (Docker not running and sandbox-exec not found)")
		return
	}
	if !sb.Available() {
		t.Error("auto-selected sandbox reports unavailable")
	}
}

func TestDockerSandbox_ImplementsInterface(t *testing.T) {
	cfg := DockerSessionConfig{
		Image: "ubuntu:latest",
	}
	ds := NewDockerSandbox(cfg)
	var _ Sandbox = ds
	if ds.Name() != "docker" {
		t.Errorf("expected name 'docker', got %q", ds.Name())
	}
}

func TestExecResult_ZeroValue(t *testing.T) {
	r := ExecResult{}
	if r.Stdout != "" || r.Stderr != "" || r.ExitCode != 0 {
		t.Error("zero value should have empty strings and exit code 0")
	}
}

func TestExecOptions_Defaults(t *testing.T) {
	o := ExecOptions{}
	if o.Timeout != 0 || o.NetworkAllowed || o.ProxyPort != 0 {
		t.Error("default ExecOptions should have zero values")
	}
}
