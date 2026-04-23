package sandbox

import (
	"runtime"
	"strings"
	"testing"
)

func TestSeatbelt_Available(t *testing.T) {
	sb := NewSeatbelt(Config{})
	if runtime.GOOS == "darwin" {
		// sandbox-exec should be present on macOS
		if !sb.Available() {
			t.Log("sandbox-exec not found, which is unusual on macOS")
		}
	} else {
		if sb.Available() {
			t.Error("seatbelt should not be available on non-darwin")
		}
	}
}

func TestSeatbelt_Name(t *testing.T) {
	sb := NewSeatbelt(Config{})
	if sb.Name() != "seatbelt" {
		t.Errorf("expected name 'seatbelt', got %q", sb.Name())
	}
}

func TestSeatbelt_GenerateProfile(t *testing.T) {
	sb := NewSeatbelt(Config{
		ReadonlyDirs: []string{"/opt/data"},
	})

	opts := ExecOptions{
		AllowedPaths:   []string{"/home/user/project"},
		ReadOnlyPaths:  []string{"/etc/config"},
		NetworkAllowed: false,
		ProxyPort:      8080,
	}

	profile := sb.generateProfile("/tmp/workdir", opts)

	// Check essential profile components
	if !strings.Contains(profile, "(version 1)") {
		t.Error("profile missing version")
	}
	if !strings.Contains(profile, "(deny default)") {
		t.Error("profile missing deny default")
	}
	if !strings.Contains(profile, "/tmp/workdir") {
		t.Error("profile missing work directory")
	}
	if !strings.Contains(profile, "/home/user/project") {
		t.Error("profile missing allowed path")
	}
	if !strings.Contains(profile, "/etc/config") {
		t.Error("profile missing read-only path")
	}
	if !strings.Contains(profile, "/opt/data") {
		t.Error("profile missing config read-only dir")
	}
	if !strings.Contains(profile, "8080") {
		t.Error("profile missing proxy port")
	}
	// Should not have unrestricted network
	if strings.Contains(profile, "(allow network*)") {
		t.Error("profile should not allow unrestricted network when NetworkAllowed is false")
	}
}

func TestSeatbelt_GenerateProfile_NetworkAllowed(t *testing.T) {
	sb := NewSeatbelt(Config{})
	opts := ExecOptions{NetworkAllowed: true}
	profile := sb.generateProfile("/tmp/work", opts)

	if !strings.Contains(profile, "(allow network*)") {
		t.Error("profile should allow network when NetworkAllowed is true")
	}
}

func TestEscapeSBPL(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`/simple/path`, `/simple/path`},
		{`/path with "quotes"`, `/path with \"quotes\"`},
		{`/path\with\backslash`, `/path\\with\\backslash`},
	}
	for _, tt := range tests {
		got := escapeSBPL(tt.input)
		if got != tt.want {
			t.Errorf("escapeSBPL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSeatbelt_Exec_Unavailable(t *testing.T) {
	sb := &Seatbelt{available: false}
	_, err := sb.Exec(nil, "echo hi", "/tmp", ExecOptions{})
	if err == nil {
		t.Error("expected error when seatbelt is unavailable")
	}
}
