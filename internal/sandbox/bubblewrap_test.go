package sandbox

import (
	"runtime"
	"testing"
)

func TestBubblewrap_Available(t *testing.T) {
	bw := NewBubblewrap(Config{})
	if runtime.GOOS != "linux" {
		if bw.Available() {
			t.Error("bubblewrap should not be available on non-linux")
		}
	}
}

func TestBubblewrap_Name(t *testing.T) {
	bw := NewBubblewrap(Config{})
	if bw.Name() != "bubblewrap" {
		t.Errorf("expected name 'bubblewrap', got %q", bw.Name())
	}
}

func TestBubblewrap_BuildArgs(t *testing.T) {
	bw := &Bubblewrap{
		cfg: Config{
			ReadonlyDirs: []string{"/opt/libs"},
		},
	}

	args := bw.buildArgs("/home/user/project", ExecOptions{
		AllowedPaths:   []string{"/tmp/extra"},
		ReadOnlyPaths:  []string{"/etc/config"},
		NetworkAllowed: false,
	})

	// Verify key arguments are present
	argStr := ""
	for _, a := range args {
		argStr += a + " "
	}

	assertContainsAll(t, args,
		"--ro-bind", "/", "/",
		"--bind", "/home/user/project",
		"--tmpfs", "/tmp",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-net",
		"--die-with-parent",
		"--chdir", "/home/user/project",
	)

	// Check extra paths
	assertContainsAll(t, args, "--bind", "/tmp/extra")
	assertContainsAll(t, args, "--ro-bind", "/etc/config")
	assertContainsAll(t, args, "--ro-bind", "/opt/libs")
}

func TestBubblewrap_BuildArgs_NetworkAllowed(t *testing.T) {
	bw := &Bubblewrap{cfg: Config{}}
	args := bw.buildArgs("/tmp", ExecOptions{NetworkAllowed: true})

	for _, a := range args {
		if a == "--unshare-net" {
			t.Error("should not unshare network when NetworkAllowed is true")
		}
	}
}

func TestBubblewrap_Exec_Unavailable(t *testing.T) {
	bw := &Bubblewrap{available: false}
	_, err := bw.Exec(nil, "echo hi", "/tmp", ExecOptions{})
	if err == nil {
		t.Error("expected error when bubblewrap is unavailable")
	}
}

func assertContainsAll(t *testing.T, args []string, targets ...string) {
	t.Helper()
	for _, target := range targets {
		found := false
		for _, a := range args {
			if a == target {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected args to contain %q", target)
		}
	}
}
