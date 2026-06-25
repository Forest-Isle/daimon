package mcp

import (
	"testing"

	"github.com/Forest-Isle/daimon/internal/tool"
)

// Regression: WatchDir → SyncServers → StopServer may receive a nil *tool.Registry.
// StopServer must tolerate it instead of nil-dereferencing at UnregisterByPrefix,
// which previously crashed the (recover-less) WatchDir goroutine and took down the
// whole process on any hot-remove — in both default and deferred modes.
func TestStopServerToleratesNilRegistry(t *testing.T) {
	assertNoPanic(t, "default_mode", func() {
		NewManager().StopServer("ghost", nil)
	})

	assertNoPanic(t, "deferred_mode", func() {
		m := NewManager()
		m.SetDeferredCatalog(tool.NewDeferredCatalog())
		m.StopServer("ghost", nil)
	})
}

func assertNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("unexpected panic: %v", r)
			}
		}()
		fn()
	})
}
