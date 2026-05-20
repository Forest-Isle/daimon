package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Memory is a minimal pluggable memory contract. Implementations may
// persist to disk, SQLite, etc. The runner only needs Append/Snapshot.
type Memory interface {
	Append(ctx context.Context, m Message) error
	Snapshot(ctx context.Context) ([]Message, error)
}

// InMemory is a goroutine-safe slice-backed Memory.
type InMemory struct {
	mu  sync.Mutex
	log []Message
}

func NewInMemory(seed ...Message) *InMemory {
	cp := make([]Message, len(seed))
	copy(cp, seed)
	return &InMemory{log: cp}
}

func (m *InMemory) Append(_ context.Context, msg Message) error {
	m.mu.Lock()
	m.log = append(m.log, msg)
	m.mu.Unlock()
	return nil
}

func (m *InMemory) Snapshot(_ context.Context) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.log))
	copy(out, m.log)
	return out, nil
}

// Config configures Agent behaviour. Zero values produce sensible defaults
// that work for one-shot completions.
type Config struct {
	Model           string        // model id passed to provider
	System          string        // system prompt
	MaxTurns        int           // hard cap on PROVIDER → TOOL → PROVIDER cycles (default 8)
	MaxTokens       int           // per-call max tokens (default 4096)
	ParallelTools   int           // max read-only tools to dispatch in parallel (default 4)
	ToolTimeout     time.Duration // per-tool timeout (default 60s)
	ToolMiddleware  []ToolMiddleware
	Sink            EventSink
	Gate            Gate
	Approver        Approver
}

// withDefaults fills zero fields with sane defaults.
func (c Config) withDefaults() Config {
	if c.MaxTurns == 0 {
		c.MaxTurns = 8
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 4096
	}
	if c.ParallelTools == 0 {
		c.ParallelTools = 4
	}
	if c.ToolTimeout == 0 {
		c.ToolTimeout = 60 * time.Second
	}
	if c.Sink == nil {
		c.Sink = NullSink
	}
	if c.Gate == nil {
		c.Gate = AllowAllGate{}
	}
	if c.Approver == nil {
		c.Approver = AutoApprover{}
	}
	return c
}

// Agent is the entire agentic loop. It is a small struct, not a god object:
// composing additional behaviour means adding ToolMiddleware or wrapping
// the Provider, never mutating Agent itself.
type Agent struct {
	cfg      Config
	provider Provider
	tools    *ToolRegistry
	memory   Memory
	handler  ToolHandler
}

// New constructs an Agent. Memory may be nil — in that case an InMemory is
// allocated on first use.
func New(provider Provider, tools *ToolRegistry, mem Memory, cfg Config) *Agent {
	if provider == nil {
		panic("core.New: nil provider")
	}
	if tools == nil {
		tools = NewToolRegistry()
	}
	if mem == nil {
		mem = NewInMemory()
	}
	cfg = cfg.withDefaults()

	// Compose: gate → tracer → user middleware → base.
	mws := make([]ToolMiddleware, 0, len(cfg.ToolMiddleware)+2)
	mws = append(mws, GateMiddleware(cfg.Gate, cfg.Approver))
	mws = append(mws, TraceToolMiddleware(cfg.Sink))
	mws = append(mws, TimeoutToolMiddleware(cfg.ToolTimeout))
	mws = append(mws, cfg.ToolMiddleware...)

	return &Agent{
		cfg:      cfg,
		provider: provider,
		tools:    tools,
		memory:   mem,
		handler:  chainTool(baseToolHandler(tools), mws...),
	}
}

// Run drives one user prompt to completion, persisting all messages
// (including tool calls and tool results) to memory and emitting events
// on the sink. It returns the final assistant text or the stop reason.
func (a *Agent) Run(ctx context.Context, prompt string) (string, StopReason, error) {
	if err := a.memory.Append(ctx, Message{Role: RoleUser, Content: prompt}); err != nil {
		return "", StopError, fmt.Errorf("memory.Append user: %w", err)
	}

	a.cfg.Sink.Emit(Event{Kind: EventStart, Time: time.Now(), Payload: map[string]any{"prompt": prompt}})
	a.cfg.Sink.Emit(Event{Kind: EventMessage, Time: time.Now(), Payload: Message{Role: RoleUser, Content: prompt}})

	var (
		lastText string
		stop     StopReason
	)
	for turn := 1; turn <= a.cfg.MaxTurns; turn++ {
		history, err := a.memory.Snapshot(ctx)
		if err != nil {
			return "", StopError, fmt.Errorf("memory.Snapshot: %w", err)
		}

		req := LLMRequest{
			Model:     a.cfg.Model,
			System:    a.cfg.System,
			Messages:  history,
			Tools:     a.tools.Schemas(),
			MaxTokens: a.cfg.MaxTokens,
		}

		a.cfg.Sink.Emit(Event{Kind: EventLLMRequest, Time: time.Now(), Turn: turn, Payload: req})

		resp, err := a.provider.Complete(ctx, req)
		if err != nil {
			a.cfg.Sink.Emit(Event{Kind: EventError, Time: time.Now(), Turn: turn, Payload: err.Error()})
			return "", StopError, fmt.Errorf("provider.Complete: %w", err)
		}

		a.cfg.Sink.Emit(Event{Kind: EventLLMResponse, Time: time.Now(), Turn: turn, Payload: resp})

		// Persist assistant message (text + any tool_calls in one turn).
		am := Message{Role: RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls}
		if err := a.memory.Append(ctx, am); err != nil {
			return "", StopError, fmt.Errorf("memory.Append assistant: %w", err)
		}
		a.cfg.Sink.Emit(Event{Kind: EventMessage, Time: time.Now(), Turn: turn, Payload: am})

		if resp.Text != "" {
			lastText = resp.Text
		}
		stop = resp.StopReason

		// No tool calls → assistant turn complete.
		if len(resp.ToolCalls) == 0 || stop == StopEndTurn {
			a.cfg.Sink.Emit(Event{Kind: EventFinish, Time: time.Now(), Turn: turn, Payload: resp})
			return lastText, stop, nil
		}

		// Run tool calls. Read-only tools dispatched in parallel batch;
		// any non-read-only tool forces sequential execution.
		results, err := a.runToolBatch(ctx, turn, resp.ToolCalls)
		if err != nil {
			return "", StopError, err
		}
		for _, r := range results {
			tm := Message{Role: RoleTool, ToolUseID: r.UseID, Content: toolResultPayload(r)}
			if err := a.memory.Append(ctx, tm); err != nil {
				return "", StopError, fmt.Errorf("memory.Append tool: %w", err)
			}
			a.cfg.Sink.Emit(Event{Kind: EventMessage, Time: time.Now(), Turn: turn, Payload: tm})
		}
	}

	a.cfg.Sink.Emit(Event{Kind: EventFinish, Time: time.Now(), Payload: "max_turns"})
	return lastText, StopMaxTurns, nil
}

// toolResultPayload renders a ToolResult as the Content string LLMs see.
// Errors are surfaced explicitly so the model can react.
func toolResultPayload(r ToolResult) string {
	if r.Error != "" {
		return "ERROR: " + r.Error
	}
	return r.Output
}

// runToolBatch executes a slice of tool calls in parallel where possible
// (all read-only and within ParallelTools concurrency budget) and serially
// otherwise. The returned slice preserves call order so message threading
// is deterministic.
func (a *Agent) runToolBatch(ctx context.Context, turn int, calls []ToolCall) ([]ToolResult, error) {
	results := make([]ToolResult, len(calls))

	// Determine if every call is read-only — only then can we parallelise.
	allReadOnly := true
	for _, c := range calls {
		t, ok := a.tools.Lookup(c.Name)
		if !ok || !t.ReadOnly() {
			allReadOnly = false
			break
		}
	}

	if !allReadOnly || a.cfg.ParallelTools <= 1 || len(calls) == 1 {
		for i, c := range calls {
			res := a.dispatch(ctx, turn, c)
			results[i] = res
		}
		return results, nil
	}

	sem := make(chan struct{}, a.cfg.ParallelTools)
	var wg sync.WaitGroup
	for i, c := range calls {
		i, c := i, c
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = a.dispatch(ctx, turn, c)
		}()
	}
	wg.Wait()
	return results, nil
}

func (a *Agent) dispatch(ctx context.Context, turn int, call ToolCall) ToolResult {
	a.cfg.Sink.Emit(Event{Kind: EventToolRequest, Time: time.Now(), Turn: turn, Payload: call})
	res, err := a.handler(ctx, call)
	if err != nil {
		// Convert errors to a tool-result the model can read instead of
		// blowing up the loop. This is a deliberate design choice: errors
		// are evidence the model should observe and recover from.
		res = ToolResult{UseID: call.ID, Error: err.Error()}
	}
	a.cfg.Sink.Emit(Event{Kind: EventToolResult, Time: time.Now(), Turn: turn, Payload: res})
	return res
}
