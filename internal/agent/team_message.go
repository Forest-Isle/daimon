package agent

import (
	"fmt"
	"sync"
	"time"
)

// MessageType defines the kind of team message.
type MessageType string

const (
	MsgDirect           MessageType = "message"
	MsgBroadcast        MessageType = "broadcast"
	MsgShutdownRequest  MessageType = "shutdown_request"
	MsgShutdownResponse MessageType = "shutdown_response"
	MsgPlanApproval     MessageType = "plan_approval"
)

// TeamMessage is the communication unit between team members.
type TeamMessage struct {
	Type      MessageType `json:"type"`
	From      string      `json:"from"`
	To        string      `json:"to,omitempty"`
	Content   string      `json:"content"`
	Summary   string      `json:"summary,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
	Approve   *bool       `json:"approve,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// MessageRouter delivers messages between team members.
type MessageRouter struct {
	team    *Team
	inboxes map[string]chan TeamMessage
	mu      sync.RWMutex
}

// NewMessageRouter creates a router for the given team.
func NewMessageRouter(team *Team) *MessageRouter {
	return &MessageRouter{
		team:    team,
		inboxes: make(map[string]chan TeamMessage),
	}
}

// Register creates an inbox for a team member.
func (r *MessageRouter) Register(name string) <-chan TeamMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan TeamMessage, 50)
	r.inboxes[name] = ch
	return ch
}

// Send delivers a message to a specific member or broadcasts to all.
func (r *MessageRouter) Send(msg TeamMessage) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	if msg.Type == MsgBroadcast {
		for name, inbox := range r.inboxes {
			if name != msg.From {
				select {
				case inbox <- msg:
				default:
					// inbox full, drop message
				}
			}
		}
		return nil
	}

	inbox, ok := r.inboxes[msg.To]
	if !ok {
		return fmt.Errorf("unknown recipient: %s", msg.To)
	}
	select {
	case inbox <- msg:
		return nil
	default:
		return fmt.Errorf("inbox full for %s", msg.To)
	}
}

// Unregister removes a member's inbox.
func (r *MessageRouter) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.inboxes[name]; ok {
		close(ch)
		delete(r.inboxes, name)
	}
}
