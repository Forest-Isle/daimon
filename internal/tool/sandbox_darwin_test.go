//go:build darwin

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeatbeltProfile verifies the generated SBPL has the expected shape: a
// permissive base, a blanket write deny re-opened only for the workdir and temp
// dirs, and a network deny.
func TestSeatbeltProfile(t *testing.T) {
	profile := seatbeltProfile("/Users/me/project")
	for _, want := range []string{
		"(version 1)",
		"(allow default)",
		"(deny file-write*)",
		`(subpath "/Users/me/project")`,
		`(subpath "/tmp")`,
		"(deny network*)",
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
}

// TestSeatbeltBackend_FencesWrites is the empirical proof that the sandbox holds:
// a write inside the working dir succeeds, a write to the home directory is
// denied by the kernel. Skips if sandbox-exec is unavailable or HOME sits inside
// a whitelisted temp root (which would make the deny case vacuous).
func TestSeatbeltBackend_FencesWrites(t *testing.T) {
	b := NewSeatbeltShellBackend()
	if !b.Available() {
		t.Skip("sandbox-exec unavailable")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	for _, root := range []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders"} {
		if strings.HasPrefix(home, root) {
			t.Skipf("HOME %q is inside whitelisted temp root; deny case would be vacuous", home)
		}
	}
	ctx := context.Background()
	work := t.TempDir()

	// 1. Write inside the working directory is allowed.
	res, err := b.Run(ctx, "echo hi > allowed.txt && echo WROTE", work, nil)
	if err != nil {
		t.Fatalf("sandboxed run error: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "WROTE") {
		t.Fatalf("in-workdir write should succeed: exit=%d stdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
	if _, statErr := os.Stat(filepath.Join(work, "allowed.txt")); statErr != nil {
		t.Fatalf("allowed.txt should exist: %v", statErr)
	}

	// 2. Write to the home directory is denied by the sandbox.
	target := filepath.Join(home, ".daimon_seatbelt_test_should_not_exist")
	_ = os.Remove(target)
	res, err = b.Run(ctx, "touch "+shellQuote(target), work, nil)
	if err != nil {
		t.Fatalf("sandboxed run error: %v", err)
	}
	if res.ExitCode == 0 {
		_ = os.Remove(target)
		t.Fatalf("write to HOME should be denied, but command succeeded")
	}
	if _, statErr := os.Stat(target); statErr == nil {
		_ = os.Remove(target)
		t.Fatalf("denied write must not create the file at %s", target)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
