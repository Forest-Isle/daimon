package agent

import (
	"github.com/Forest-Isle/IronClaw/internal/session"
)

const maxContextMessages = 50

// safeTrimHistory trims history to at most maxLen messages while ensuring no
// orphaned tool_result remains — a tool_result whose tool_use was trimmed away
// causes the provider to return HTTP 400. It first advances the window to begin
// on a user message (API requirement), then drops any tool_result anywhere in
// the window whose referenced tool_use is no longer present.
func safeTrimHistory(history []session.Message, maxLen int) []session.Message {
	if len(history) <= maxLen {
		return history
	}

	start := len(history) - maxLen

	// Ensure the window begins on a user message, dropping any leading
	// assistant/tool_use/tool_result fragments.
	for start < len(history) && history[start].Role != "user" {
		start++
	}
	window := history[start:]

	// Collect tool_use IDs present in the window.
	toolUseIDs := make(map[string]bool)
	for _, m := range window {
		if m.Role == "tool_use" {
			toolUseIDs[m.ID] = true
		}
	}

	// Drop any tool_result — at any position — whose tool_use is not in the
	// window. ToolName stores the referenced tool_use ID for tool_result rows.
	trimmed := make([]session.Message, 0, len(window))
	for _, m := range window {
		if m.Role == "tool_result" && !toolUseIDs[m.ToolName] {
			continue
		}
		trimmed = append(trimmed, m)
	}

	return trimmed
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
				Role:      "assistant",
				Content:   m.Content,
				Thinking:  m.Thinking,
				Signature: m.Signature,
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
