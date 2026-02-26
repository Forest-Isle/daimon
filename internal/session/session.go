package session

import (
	"sync"
	"time"
)

// Message represents a single message in a session's history.
type Message struct {
	ID        string
	Role      string // user, assistant, system, tool_use, tool_result
	Content   string
	ToolName  string
	ToolInput string
	CreatedAt time.Time
}

// Session holds the state for a single conversation.
type Session struct {
	ID        string
	Channel   string
	ChannelID string
	Messages  []Message
	CreatedAt time.Time
	UpdatedAt time.Time
	Metadata  map[string]string

	mu sync.Mutex
}

// AddMessage appends a message to the session history.
func (s *Session) AddMessage(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

// History returns a copy of the message history.
func (s *Session) History() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.Messages))
	copy(out, s.Messages)
	return out
}

// TrimHistory keeps only the last n messages.
func (s *Session) TrimHistory(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Messages) > n {
		s.Messages = s.Messages[len(s.Messages)-n:]
	}
}
