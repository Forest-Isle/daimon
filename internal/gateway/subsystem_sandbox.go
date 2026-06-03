package gateway

import (
	"context"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/sandbox"
	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// SandboxSubsystem manages Docker session isolation, file guards, network
// policies, and the tool interceptor chain.
type SandboxSubsystem struct {
	dockerSessionMgr *sandbox.DockerSessionManager
	interceptorChain *tool.InterceptorChain
	trustTracker     *tool.TrustTracker
	httpTool         *tool.HTTPTool // stored for redirect-check injection after network policy init
}

func (ss *SandboxSubsystem) Name() string { return "sandbox" }

// Start is a no-op — sandbox components are initialized during New().
func (ss *SandboxSubsystem) Start(_ context.Context) error { return nil }

// Stop cleans up all Docker sandbox sessions.
func (ss *SandboxSubsystem) Stop(_ context.Context) error {
	if ss.dockerSessionMgr != nil {
		ss.dockerSessionMgr.CleanupAll()
		slog.Debug("sandbox: docker sessions cleaned up")
	}
	return nil
}

// DockerSessionManager returns the Docker session manager, or nil.
func (ss *SandboxSubsystem) DockerSessionManager() *sandbox.DockerSessionManager {
	return ss.dockerSessionMgr
}

// InterceptorChain returns the tool interceptor chain, or nil.
func (ss *SandboxSubsystem) InterceptorChain() *tool.InterceptorChain {
	return ss.interceptorChain
}

// TrustTracker returns the trust tracker, or nil.
func (ss *SandboxSubsystem) TrustTracker() *tool.TrustTracker {
	return ss.trustTracker
}
