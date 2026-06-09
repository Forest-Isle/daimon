package feature

import "context"

// Feature defines a registrable capability.
type Feature struct {
	Name        string
	Description string
	Default     bool
	AutoDetect  func(ctx context.Context) bool // nil = always available
}

// Info is a read-only snapshot of a feature's current state.
type Info struct {
	Name        string
	Description string
	Enabled     bool
	Reason      string
}
