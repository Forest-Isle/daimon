package channel

import "context"

// InboundHandler is called by a channel adapter when a message arrives.
type InboundHandler func(ctx context.Context, msg InboundMessage)

// Channel adapts an external messaging platform (Telegram, Discord, etc.).
type Channel interface {
	Name() string
	Start(ctx context.Context, handler InboundHandler) error
	Send(ctx context.Context, msg OutboundMessage) error
	SendStreaming(ctx context.Context, target MessageTarget) (StreamUpdater, error)
	Stop(ctx context.Context) error
}

// StreamUpdater allows incremental updates to a streaming message.
type StreamUpdater interface {
	Update(text string) error
	Finish(text string) error
}
