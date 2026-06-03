package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/tool"
)

// AgentMessage is a message exchanged between sub-agents on a shared bus.
type AgentMessage struct {
	From    string `json:"from"`    // agent name
	To      string `json:"to"`      // agent name, or "*" for broadcast
	Type    string `json:"type"`    // "data", "signal", "error", "done"
	Payload string `json:"payload"` // arbitrary content
}

// MessageBus enables sub-agents spawned within the same parallel group to
// exchange messages. Agents with the same SharedChannelID share a bus
// and can publish/receive messages asynchronously.
type MessageBus struct {
	mu       sync.RWMutex
	channels map[string]*messageChannel
}

type messageChannel struct {
	mu    sync.Mutex
	msgs  []AgentMessage
	sigCh chan struct{} // signaled when a new message arrives
	subs  map[string]int // agent name → last read index
}

// NewMessageBus creates a new inter-agent message bus.
func NewMessageBus() *MessageBus {
	return &MessageBus{channels: make(map[string]*messageChannel)}
}

// GetOrCreate returns the channel for a sharedChannelID, creating it if needed.
func (b *MessageBus) GetOrCreate(channelID string) *messageChannel {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.channels[channelID]
	if !ok {
		ch = &messageChannel{
			sigCh: make(chan struct{}, 1),
			subs:  make(map[string]int),
		}
		b.channels[channelID] = ch
	}
	return ch
}

// Publish sends a message on the given channel. The message is appended
// and all waiting subscribers are signaled.
func (b *MessageBus) Publish(channelID string, msg AgentMessage) {
	ch := b.GetOrCreate(channelID)
	ch.mu.Lock()
	ch.msgs = append(ch.msgs, msg)
	// Non-blocking signal
	select {
	case ch.sigCh <- struct{}{}:
	default:
	}
	ch.mu.Unlock()
}

// Receive blocks until a new message arrives for the given agent on the
// channel, then returns all unread messages. If timeout is reached, returns
// whatever is available (possibly empty).
func (b *MessageBus) Receive(channelID, agentName string, timeout time.Duration) []AgentMessage {
	ch := b.GetOrCreate(channelID)
	deadline := time.After(timeout)

	// Check if there are already unread messages
	ch.mu.Lock()
	lastIdx := ch.subs[agentName]
	if lastIdx < len(ch.msgs) {
		unread := make([]AgentMessage, len(ch.msgs)-lastIdx)
		copy(unread, ch.msgs[lastIdx:])
		ch.subs[agentName] = len(ch.msgs)
		ch.mu.Unlock()
		return unread
	}
	ch.mu.Unlock()

	// Wait for new message or timeout
	select {
	case <-ch.sigCh:
		ch.mu.Lock()
		defer ch.mu.Unlock()
		idx := ch.subs[agentName]
		if idx >= len(ch.msgs) {
			return nil
		}
		unread := make([]AgentMessage, len(ch.msgs)-idx)
		copy(unread, ch.msgs[idx:])
		ch.subs[agentName] = len(ch.msgs)
		return unread
	case <-deadline:
		ch.mu.Lock()
		defer ch.mu.Unlock()
		idx := ch.subs[agentName]
		if idx >= len(ch.msgs) {
			return nil
		}
		unread := make([]AgentMessage, len(ch.msgs)-idx)
		copy(unread, ch.msgs[idx:])
		ch.subs[agentName] = len(ch.msgs)
		return unread
	}
}

// Remove cleans up a channel when no longer needed.
func (b *MessageBus) Remove(channelID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.channels, channelID)
}

// AgentMessageTool exposes the message bus to sub-agents as a tool.
type AgentMessageTool struct {
	bus       *MessageBus
	channelID string
	agentName string
}

func NewAgentMessageTool(bus *MessageBus, channelID, agentName string) *AgentMessageTool {
	return &AgentMessageTool{bus: bus, channelID: channelID, agentName: agentName}
}

func (t *AgentMessageTool) Name() string        { return "agent_message" }
func (t *AgentMessageTool) Description() string { return "Send messages to or receive messages from other agents in the same team." }
func (t *AgentMessageTool) RequiresApproval() bool { return false }
func (t *AgentMessageTool) IsReadOnly() bool        { return true }

func (t *AgentMessageTool) Capabilities() tool.ToolCapabilities {
	return tool.ToolCapabilities{IsReadOnly: true, IsDestructive: false}
}

func (t *AgentMessageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op":      map[string]any{"type": "string", "enum": []string{"send", "receive"}},
			"to":      map[string]any{"type": "string", "description": "Target agent name, or * for broadcast. Only for send."},
			"type":    map[string]any{"type": "string", "description": "Message type: data, signal, error, done"},
			"payload": map[string]any{"type": "string", "description": "Message content"},
		},
		"required": []string{"op"},
	}
}

func (t *AgentMessageTool) Execute(ctx context.Context, input []byte) (tool.Result, error) {
	var in struct {
		Op      string `json:"op"`
		To      string `json:"to"`
		Type    string `json:"type"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Result{Error: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	switch in.Op {
	case "send":
		if in.To == "" {
			in.To = "*"
		}
		msg := AgentMessage{
			From:    t.agentName,
			To:      in.To,
			Type:    in.Type,
			Payload: in.Payload,
		}
		t.bus.Publish(t.channelID, msg)
		return tool.Result{Output: fmt.Sprintf("Message sent to %s", in.To)}, nil
	case "receive":
		msgs := t.bus.Receive(t.channelID, t.agentName, 5*time.Second)
		if len(msgs) == 0 {
			return tool.Result{Output: "(no messages)"}, nil
		}
		b, _ := json.Marshal(msgs)
		return tool.Result{Output: string(b)}, nil
	default:
		return tool.Result{Error: fmt.Sprintf("unknown op: %s (use send or receive)", in.Op)}, nil
	}
}
