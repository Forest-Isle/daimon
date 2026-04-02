package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/punkopunko/ironclaw/internal/session"
)

const forkDirectiveTag = "fork-directive"

// BuildForkMessages creates the message list for a fork agent.
// It copies the parent's message history and appends a fork directive
// as a new user message. The parent slice is never mutated.
func BuildForkMessages(parentMessages []session.Message, directive string) []session.Message {
	msgs := make([]session.Message, len(parentMessages), len(parentMessages)+1)
	copy(msgs, parentMessages)

	forkMsg := session.Message{
		ID:        fmt.Sprintf("fork_%d", time.Now().UnixNano()),
		Role:      "user",
		Content:   fmt.Sprintf("<%s>\n%s\n</%s>", forkDirectiveTag, directive, forkDirectiveTag),
		CreatedAt: time.Now(),
	}
	msgs = append(msgs, forkMsg)
	return msgs
}

// IsForkDirective returns true if the message is a fork directive.
func IsForkDirective(msg session.Message) bool {
	return msg.Role == "user" && strings.Contains(msg.Content, "<"+forkDirectiveTag+">")
}

// CheckForkDepth returns an error if the sub-agent context has reached MaxForkDepth.
func CheckForkDepth(sc *SubagentContext) error {
	if sc == nil {
		return nil
	}
	if sc.Depth >= MaxForkDepth {
		return fmt.Errorf("fork depth %d exceeds maximum %d", sc.Depth, MaxForkDepth)
	}
	return nil
}
