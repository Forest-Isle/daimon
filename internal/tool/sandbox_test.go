package tool

import (
	"context"
	"testing"
)

// TestChannelRoutingBackend verifies the per-call host/sandbox decision: local
// interactive calls stay on the host, every non-local trust boundary is forced
// into the sandbox, the seatbelt default forces even local calls, a non-local
// call whose sandbox is unavailable fails closed (never falls back to the host),
// and the local seatbelt opt-in degrades to the host when the sandbox is gone.
func TestChannelRoutingBackend(t *testing.T) {
	cases := []struct {
		name           string
		class          ToolChannelClass
		defaultSandbox bool
		sandboxAvail   bool
		want           string // "HOST" or "SANDBOX"
		wantErr        bool   // fail closed: refuse rather than run on host
	}{
		{"local stays host", ToolChannelLocal, false, true, "HOST", false},
		{"remote forced sandbox", ToolChannelRemote, false, true, "SANDBOX", false},
		{"scheduled forced sandbox", ToolChannelScheduled, false, true, "SANDBOX", false},
		{"internal forced sandbox", ToolChannelInternal, false, true, "SANDBOX", false},
		{"background forced sandbox", ToolChannelBackground, false, true, "SANDBOX", false},
		{"seatbelt default forces local", ToolChannelLocal, true, true, "SANDBOX", false},
		{"non-local sandbox unavailable fails closed", ToolChannelRemote, false, false, "", true},
		{"seatbelt default but unavailable falls back", ToolChannelLocal, true, false, "HOST", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &fakeShellBackend{available: true, result: ShellRunResult{Stdout: "HOST"}}
			sandbox := &fakeShellBackend{available: tc.sandboxAvail, result: ShellRunResult{Stdout: "SANDBOX"}}
			b := NewChannelRoutingBackend(host, sandbox, tc.defaultSandbox)

			ctx := WithChannelClass(context.Background(), tc.class)
			res, err := b.Run(ctx, "echo hi", "/tmp", nil)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected fail-closed error, ran %q backend instead", res.Stdout)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if res.Stdout != tc.want {
				t.Fatalf("ran %q backend, want %q", res.Stdout, tc.want)
			}
		})
	}
}

// TestChannelRoutingBackend_DefaultClassIsLocal pins that a context without an
// explicit channel class is treated as local (host), not sandboxed.
func TestChannelRoutingBackend_DefaultClassIsLocal(t *testing.T) {
	host := &fakeShellBackend{available: true, result: ShellRunResult{Stdout: "HOST"}}
	sandbox := &fakeShellBackend{available: true, result: ShellRunResult{Stdout: "SANDBOX"}}
	b := NewChannelRoutingBackend(host, sandbox, false)

	res, err := b.Run(context.Background(), "echo hi", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "HOST" {
		t.Fatalf("no-class context ran %q, want HOST", res.Stdout)
	}
}
