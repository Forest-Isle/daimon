package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/gateway"
	"github.com/Forest-Isle/IronClaw/internal/userdir"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Sub-agent management commands",
	}
	cmd.AddCommand(newAgentRunCmd())
	return cmd
}

func newAgentRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run a sub-agent task from stdin (used by SubprocessBackend)",
		Long: `Reads a SubprocessRequest JSON from stdin, executes the agent task,
and writes a SubprocessResponse JSON to stdout. This command is designed
to be invoked by SubprocessBackend and should not be called manually.

All log output goes to stderr; only the JSON response is written to stdout.`,
		RunE: runAgentFromStdin,
	}
}

func runAgentFromStdin(cmd *cobra.Command, args []string) error {
	// Log to stderr only — stdout is reserved for JSON response.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	// Read request from stdin.
	req, err := agent.ReadRequest(os.Stdin)
	if err != nil {
		return fmt.Errorf("read request from stdin: %w", err)
	}

	slog.Info("agent run: starting", "agent_id", req.AgentID, "task_len", len(req.Task))

	// Load config.
	cfg, err := config.Load(req.ConfigPath)
	if err != nil {
		return writeErrorResponse(fmt.Sprintf("load config %s: %v", req.ConfigPath, err), 0)
	}
	if err := userdir.Apply(cfg); err != nil {
		return writeErrorResponse(fmt.Sprintf("apply user config: %v", err), 0)
	}

	// Apply overrides from request.
	if req.Model != "" {
		cfg.LLM.Model = req.Model
	}
	if req.MaxTokens > 0 {
		cfg.LLM.MaxTokens = req.MaxTokens
	}
	if req.SystemPrompt != "" {
		cfg.Agent.SystemPrompt = req.SystemPrompt
	}
	if req.MaxIter > 0 {
		cfg.Agent.MaxIterations = req.MaxIter
	}

	// Initialize headless gateway (DB → Tools → LLM → Runtime).
	hgw, err := gateway.NewHeadless(cfg)
	if err != nil {
		return writeErrorResponse(fmt.Sprintf("init headless gateway: %v", err), 0)
	}
	defer func() { _ = hgw.Close() }()

	// Apply tool filter.
	hgw.FilterTools(req.AllowedTools)

	// Set up timeout from request.
	timeout := 120 * time.Second
	if req.Timeout != "" {
		if parsed, err := time.ParseDuration(req.Timeout); err == nil && parsed > 0 {
			timeout = parsed
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Handle SIGTERM for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		slog.Warn("agent run: received signal, cancelling")
		cancel()
	}()

	// Build user message.
	userText := req.Task
	if req.TaskContext != "" {
		userText = fmt.Sprintf("Context from previous tasks:\n%s\n\nTask:\n%s", req.TaskContext, req.Task)
	}

	// Execute via captureChannel.
	start := time.Now()
	capture := newSubprocessCaptureChannel()
	msg := channel.InboundMessage{
		Channel:   "agent",
		ChannelID: fmt.Sprintf("agent_%s", req.AgentID),
		UserID:    "orchestrator",
		UserName:  "orchestrator",
		Text:      userText,
	}

	runtime := hgw.Runtime()
	execErr := runtime.HandleMessage(ctx, capture, msg)
	duration := time.Since(start)

	// Build and write response.
	resp := &agent.SubprocessResponse{
		Duration: duration.String(),
	}
	if execErr != nil {
		resp.Error = execErr.Error()
	} else {
		resp.Output = capture.Collect()
	}

	return agent.WriteResponse(os.Stdout, resp)
}

// writeErrorResponse writes a SubprocessResponse with an error to stdout
// and returns nil (the CLI should exit 0 so the parent can parse the response).
func writeErrorResponse(errMsg string, duration time.Duration) error {
	resp := &agent.SubprocessResponse{
		Error:    errMsg,
		Duration: duration.String(),
	}
	return agent.WriteResponse(os.Stdout, resp)
}

// subprocessCaptureChannel is a minimal channel.Channel implementation for
// capturing sub-agent output in the subprocess. It mirrors the captureChannel
// in agent_tool.go but lives in the main package.
type subprocessCaptureChannel struct {
	messages []string
}

func newSubprocessCaptureChannel() *subprocessCaptureChannel {
	return &subprocessCaptureChannel{}
}

func (c *subprocessCaptureChannel) Name() string { return "subprocess" }

func (c *subprocessCaptureChannel) Start(_ context.Context, _ channel.InboundHandler) error {
	return nil
}

func (c *subprocessCaptureChannel) Send(_ context.Context, msg channel.OutboundMessage) error {
	if msg.Text != "" {
		c.messages = append(c.messages, msg.Text)
	}
	return nil
}

func (c *subprocessCaptureChannel) SendStreaming(_ context.Context, _ channel.MessageTarget) (channel.StreamUpdater, error) {
	return &subprocessCaptureUpdater{ch: c}, nil
}

func (c *subprocessCaptureChannel) Stop(_ context.Context) error {
	return nil
}

func (c *subprocessCaptureChannel) Collect() string {
	if len(c.messages) == 0 {
		return ""
	}
	return c.messages[len(c.messages)-1]
}

type subprocessCaptureUpdater struct {
	ch *subprocessCaptureChannel
}

func (u *subprocessCaptureUpdater) Update(_ string) error { return nil }

func (u *subprocessCaptureUpdater) Finish(text string) error {
	if text != "" {
		u.ch.messages = append(u.ch.messages, text)
	}
	return nil
}
