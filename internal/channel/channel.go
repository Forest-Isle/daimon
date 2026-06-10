package channel

import "context"

// InboundHandler is called by a channel adapter when a message arrives.
type InboundHandler func(ctx context.Context, msg InboundMessage)

// Channel adapts an external messaging platform (Telegram, TUI, etc.).
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

// ApprovalSender is an optional interface for channels that support interactive
// tool-execution approval. The call blocks until the user responds or a timeout
// is reached. Channels that do not implement this interface will auto-approve.
type ApprovalSender interface {
	SendApprovalRequest(ctx context.Context, target MessageTarget, toolName string, input string) (bool, error)
}

// NotificationSender is an optional interface for channels that support
// lightweight status notifications (e.g., memory operation summaries).
// Channels that do not implement this interface simply skip notifications.
type NotificationSender interface {
	SendNotification(ctx context.Context, target MessageTarget, text string) error
}

// FeedbackSender is an optional interface for channels that support
// collecting user satisfaction feedback (e.g., 👍/👎 after a response).
// The call blocks until the user responds or a timeout is reached.
// Returns a value in [-1, 1]: -1 (negative), 0 (neutral/timeout), 1 (positive).
// Channels that do not implement this interface yield 0 (neutral).
type FeedbackSender interface {
	SendFeedbackRequest(ctx context.Context, target MessageTarget) (float64, error)
}

// ToolStreamWriter is an optional interface for channels that support
// real-time streaming of tool execution output. When a tool produces
// output incrementally (e.g., long-running bash commands), the runtime
// sends lines/chunks via this writer while the tool is still running.
// Channels that implement this interface give users live feedback;
// channels that don't simply buffer output until completion.
type ToolStreamWriter interface {
	WriteToolStream(ctx context.Context, target MessageTarget, toolName string, chunk string) error
	FlushToolStream(ctx context.Context, target MessageTarget, toolName string) error
}
