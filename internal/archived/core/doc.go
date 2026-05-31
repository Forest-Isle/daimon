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
// The package is organized as flat files within this package rather than
// sub-packages:
//
//	agent.go         Core Agent type, Config, Memory interface, tool dispatch.
//	stream_agent.go  StreamAgent wrapping Agent with streaming LLM support.
//	provider.go      Provider, Stream, LLMRequest, LLMResponse, LLMChunk.
//	tool.go          Tool interface, ToolRegistry, ToolSchema, ToolResult.
//	event.go         Event types and EventSink interface.
//	middleware.go    ToolMiddleware chain, GateMiddleware, TraceMiddleware.
//	adapter/         Legacy adapters bridging agent.Provider and tool.Tool.
//
// The top-level Agent.Run() function is the entire core loop:
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
