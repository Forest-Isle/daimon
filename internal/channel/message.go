package channel

// InboundMessage is a normalized message from any channel.
type InboundMessage struct {
	Channel   string // e.g. "telegram"
	ChannelID string // unique user/chat identifier within the channel
	UserID    string
	UserName  string
	Text      string
	// MessageID is the channel-native identifier for this inbound (telegram
	// update_id, a TUI line counter). It is the dedup key for the event heart so a
	// redelivered message is recorded once. Empty when the channel has no stable id.
	MessageID string
	// CallbackData is set for inline keyboard callbacks (e.g. tool approval).
	CallbackData string
	// ReplyToMsgID is the platform message ID being replied to, if any.
	ReplyToMsgID string
}

// OutboundMessage is a message to send to a channel.
type OutboundMessage struct {
	Channel   string
	ChannelID string
	Text      string
	// ParseMode: "Markdown", "HTML", or empty for plain text.
	ParseMode string
	// ReplyMarkup is channel-specific inline keyboard data.
	ReplyMarkup any
}

// MessageTarget identifies where to send a streaming message.
type MessageTarget struct {
	Channel   string
	ChannelID string
}
