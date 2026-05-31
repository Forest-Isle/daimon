# Archived Code

## core/
A clean, composable agentic runtime built as an alternative to internal/agent/.
Archived 2026-05-31 during the cognitive rewrite (single-loop + self-correction).
Key design ideas preserved for reference:
- Middleware chain for tool execution (ToolMiddleware)
- Event bus for observability (EventSink)
- Provider-agnostic interfaces (Provider, Tool, Memory)

See docs/superpowers/specs/2026-05-31-ironclaw-refactor-design.md for context.
