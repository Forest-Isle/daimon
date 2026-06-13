//go:build !darwin

package tool

import "context"

// unavailableSandboxBackend is the non-darwin placeholder: there is no
// sandbox-exec, so it reports itself unavailable and the routing backend falls
// back to the host (with a warning). A future increment can add a Linux backend
// (e.g. bubblewrap/landlock) here.
type unavailableSandboxBackend struct{}

// NewSeatbeltShellBackend returns an unavailable backend on non-darwin platforms.
func NewSeatbeltShellBackend() ShellBackend { return &unavailableSandboxBackend{} }

func (b *unavailableSandboxBackend) Available() bool { return false }

func (b *unavailableSandboxBackend) Run(_ context.Context, _ string, _ string, _ StreamCallback) (ShellRunResult, error) {
	return ShellRunResult{}, nil
}
