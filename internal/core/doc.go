// Package core is a clean, composable agentic runtime.
//
// Design goals:
//   - Tiny surface: one Agent type, one Runner, one event stream.
//   - No god-objects: every concern is a Middleware or a small interface.
//   - Streaming-first: every step emits typed events on a single bus.
//   - Provider-agnostic: wraps the legacy internal/agent.Provider and
//     internal/tool.Registry via thin adapters so existing LLM/tool
//     implementations are reused without modification.
//
// Sub-packages:
//
//	core/llm      Clean LLM Provider interface + legacy adapter.
//	core/tools    Clean Tool interface + Registry + legacy adapter.
//	core/events   Typed event bus.
//	core/memory   Minimal pluggable memory store.
//	core/policy   Tool-call permission decisions.
//	core/middleware Middleware contracts for tool execution and LLM calls.
//	core/builtin  Stock middleware: permissions, tracing, result cache.
//	core/runner   Wires an Agent, Provider, Tools, Memory, and Middleware
//	              into a single Run(ctx, prompt) → <-chan Event entry point.
//
// The top-level Agent.Step() function is the entire core loop:
//
//	for !done {
//	    response := provider.Complete(messages)
//	    if response has tool_calls:
//	        run each tool through middleware chain
//	        append results to messages
//	    else:
//	        done = true
//	}
//
// Everything else — permissions, hooks, caching, telemetry — plugs in as
// middleware. Adding capability never requires editing Agent.
package core
