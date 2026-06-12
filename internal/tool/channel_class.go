package tool

import "context"

// ToolChannelClass describes the trust boundary a tool call came from.
type ToolChannelClass string

const (
	ToolChannelLocal      ToolChannelClass = "local"
	ToolChannelRemote     ToolChannelClass = "remote"
	ToolChannelScheduled  ToolChannelClass = "scheduled"
	ToolChannelBackground ToolChannelClass = "background"
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

func ChannelClassForName(name string) ToolChannelClass {
	switch name {
	case "tui":
		return ToolChannelLocal
	case "scheduler":
		return ToolChannelScheduled
	case "subagent":
		return ToolChannelBackground
	case "":
		return ToolChannelLocal
	default:
		return ToolChannelRemote
	}
}
