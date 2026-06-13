//go:build darwin

package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// SeatbeltShellBackend runs a command under macOS's sandbox-exec with a
// dynamically generated SBPL profile that denies network access and confines
// file writes to the working directory and temp dirs. Reads and process
// execution are unrestricted (allow default) so ordinary commands still run; the
// fence is on writes and network, which is what stops a remote-triggered command
// from damaging the user's files or exfiltrating data.
type SeatbeltShellBackend struct{}

// NewSeatbeltShellBackend returns the macOS seatbelt backend.
func NewSeatbeltShellBackend() ShellBackend { return &SeatbeltShellBackend{} }

func (b *SeatbeltShellBackend) Available() bool {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return false
	}
	if _, err := exec.LookPath("bash"); err != nil {
		return false
	}
	return true
}

func (b *SeatbeltShellBackend) Run(ctx context.Context, command, workDir string, streamCB StreamCallback) (ShellRunResult, error) {
	profile := seatbeltProfile(workDir)
	cmd := exec.CommandContext(ctx, "sandbox-exec", "-p", profile, "bash", "-c", command)
	return runShellCommand(ctx, cmd, workDir, streamCB)
}

// seatbeltWritableTempDirs are always-writable roots: the standard temp
// locations on macOS (TMPDIR resolves under /private/var/folders) plus the
// device sinks commands routinely write to.
var seatbeltWritableTempDirs = []string{
	"/tmp",
	"/private/tmp",
	"/private/var/folders",
	"/var/folders",
}

var seatbeltWritableDevices = []string{
	"/dev/null",
	"/dev/stdout",
	"/dev/stderr",
	"/dev/dtracehelper",
	"/dev/tty",
}

// seatbeltProfile builds the SBPL profile. Rule order matters: SBPL applies the
// last matching rule, so the broad (deny file-write*) after (allow default) is
// re-opened only for the whitelisted subpaths.
func seatbeltProfile(workDir string) string {
	var subpaths []string
	for _, p := range seatbeltWritableTempDirs {
		subpaths = append(subpaths, `(subpath "`+escapeSBPL(p)+`")`)
	}
	if abs := absWorkDir(workDir); abs != "" {
		subpaths = append(subpaths, `(subpath "`+escapeSBPL(abs)+`")`)
	}
	for _, d := range seatbeltWritableDevices {
		subpaths = append(subpaths, `(literal "`+escapeSBPL(d)+`")`)
	}

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")
	b.WriteString("(deny file-write*)\n")
	b.WriteString("(allow file-write* " + strings.Join(subpaths, " ") + ")\n")
	b.WriteString("(deny network*)\n")
	return b.String()
}

func absWorkDir(workDir string) string {
	if workDir == "" {
		return ""
	}
	if filepath.IsAbs(workDir) {
		return filepath.Clean(workDir)
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return ""
	}
	return abs
}

// escapeSBPL escapes a path for an SBPL double-quoted string literal.
func escapeSBPL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
