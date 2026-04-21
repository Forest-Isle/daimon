package feature

import "context"

// Phase controls when a feature is initialized during gateway startup.
type Phase int

const (
	PhaseConstruct  Phase = iota // initialized during object construction
	PhaseStart                   // initialized when the gateway starts
	PhaseBackground              // initialized in background after startup
)

func (p Phase) String() string {
	switch p {
	case PhaseConstruct:
		return "construct"
	case PhaseStart:
		return "start"
	case PhaseBackground:
		return "background"
	default:
		return "unknown"
	}
}

// DetectResult is returned by a feature's AutoDetect function.
type DetectResult struct {
	Available bool
	Reason    string
}

// Feature defines a registrable capability with lifecycle hooks.
type Feature struct {
	Name          string
	Description   string
	Default       bool
	Phase         Phase
	Dependencies  []string
	HotReloadable bool
	AutoDetect    func(ctx context.Context) DetectResult
	OnEnable      func(ctx context.Context) error
	OnDisable     func(ctx context.Context) error
}

// FeatureInfo is a read-only snapshot of a feature's current state.
type FeatureInfo struct {
	Name          string
	Description   string
	Enabled       bool
	Reason        string
	Phase         Phase
	Dependencies  []string
	HotReloadable bool
}
