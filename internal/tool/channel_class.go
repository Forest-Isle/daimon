package tool

import "context"

// ToolChannelClass describes the trust boundary a tool call came from.
type ToolChannelClass string

const (
	ToolChannelLocal      ToolChannelClass = "local"
	ToolChannelRemote     ToolChannelClass = "remote"
	ToolChannelScheduled  ToolChannelClass = "scheduled"
	ToolChannelBackground ToolChannelClass = "background"
	// ToolChannelInternal is an autonomous episode the agent runs for itself (a
	// timer/heartbeat/mail event), with no human channel to approve anything. Its
	// profile allows read-only tools but gates every write/destructive/network
	// call to approval — which, lacking a channel, is denied.
	ToolChannelInternal ToolChannelClass = "internal"
)

type channelClassCtxKey struct{}

func WithChannelClass(ctx context.Context, class ToolChannelClass) context.Context {
	if class == "" {
		class = ToolChannelLocal
	}
	return context.WithValue(ctx, channelClassCtxKey{}, class)
}

func ChannelClassFromContext(ctx context.Context) ToolChannelClass {
	class, _ := ctx.Value(channelClassCtxKey{}).(ToolChannelClass)
	if class == "" {
		return ToolChannelLocal
	}
	return class
}

// ChannelClassFromContextOK reports the channel class and whether it was
// explicitly stamped. Security gates that must fail closed (e.g. the action
// value gate) use ok to refuse the permissive default: an unstamped context is
// treated as untrusted rather than silently interactive.
func ChannelClassFromContextOK(ctx context.Context) (ToolChannelClass, bool) {
	class, ok := ctx.Value(channelClassCtxKey{}).(ToolChannelClass)
	if !ok || class == "" {
		return ToolChannelLocal, false
	}
	return class, true
}

func ChannelClassForName(name string) ToolChannelClass {
	switch name {
	case "tui":
		return ToolChannelLocal
	case "scheduler":
		return ToolChannelScheduled
	case "subagent":
		return ToolChannelBackground
	case "internal":
		return ToolChannelInternal
	case "":
		return ToolChannelLocal
	default:
		return ToolChannelRemote
	}
}
