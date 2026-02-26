package agent

import (
	"github.com/punkopunko/ironclaw/internal/session"
)

const maxContextMessages = 50

// safeTrimHistory trims history to at most maxLen messages while ensuring
// tool_use/tool_result pairs are never split. It finds a safe cut point
// where no orphaned tool_result references a tool_use that was trimmed away.
func safeTrimHistory(history []session.Message, maxLen int) []session.Message {
	if len(history) <= maxLen {
		return history
	}

	start := len(history) - maxLen

	// Collect all tool_use IDs present from start onward
	toolUseIDs := make(map[string]bool)
	for _, m := range history[start:] {
		if m.Role == "tool_use" {
			toolUseIDs[m.ID] = true
		}
	}

	// Move start forward to skip any orphaned tool_result messages
	// whose corresponding tool_use was trimmed away.
	for start < len(history) {
		m := history[start]
		if m.Role == "tool_result" && !toolUseIDs[m.ToolName] {
			start++
			continue
		}
		// Also skip standalone tool_use at the very beginning if its
		// tool_result is not in the window (partial pair).
		if m.Role == "tool_use" {
			break
		}
		break
	}

	// Ensure the first message has role "user" (API requirement).
	// Skip any leading assistant/tool_use/tool_result messages.
	for start < len(history) && history[start].Role != "user" {
		start++
	}

	return history[start:]
}

// BuildMessages converts session history into CompletionMessages for the LLM.
// It merges tool_use messages into the preceding assistant message and
// tool_result messages into user messages, matching the Anthropic API format.
func BuildMessages(sess *session.Session) []CompletionMessage {
	history := sess.History()

	// Trim to fit context window, respecting tool_use/tool_result pairing
	history = safeTrimHistory(history, maxContextMessages)

	var msgs []CompletionMessage

	for i := 0; i < len(history); i++ {
		m := history[i]

		switch m.Role {
		case "user":
			msgs = append(msgs, CompletionMessage{
				Role:    "user",
				Content: m.Content,
			})

		case "assistant":
			cm := CompletionMessage{
				Role:    "assistant",
				Content: m.Content,
			}
			// Collect any following tool_use messages into this assistant message
			for i+1 < len(history) && history[i+1].Role == "tool_use" {
				i++
				tu := history[i]
				cm.ToolBlocks = append(cm.ToolBlocks, ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.ToolName,
					Input: tu.ToolInput,
				})
			}
			msgs = append(msgs, cm)

		case "tool_use":
			// Standalone tool_use (no preceding assistant message) — wrap in assistant
			cm := CompletionMessage{
				Role: "assistant",
				ToolBlocks: []ToolUseBlock{{
					ID:    m.ID,
					Name:  m.ToolName,
					Input: m.ToolInput,
				}},
			}
			// Collect any more consecutive tool_use messages
			for i+1 < len(history) && history[i+1].Role == "tool_use" {
				i++
				tu := history[i]
				cm.ToolBlocks = append(cm.ToolBlocks, ToolUseBlock{
					ID:    tu.ID,
					Name:  tu.ToolName,
					Input: tu.ToolInput,
				})
			}
			msgs = append(msgs, cm)

		case "tool_result":
			// ToolName stores the tool_use ID for tool_result messages
			msgs = append(msgs, CompletionMessage{
				Role:      "user",
				Content:   m.Content,
				ToolUseID: m.ToolName,
			})
		}
	}

	return msgs
}
