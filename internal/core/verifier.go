package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Verifier inspects tool results and flags anomalies. A failed verification
// surfaces as an enriched error in the tool result so the model can
// self-correct on the next turn without hard-stopping the loop.
//
// Verifiers are tool-specific: BashVerifier checks exit codes, FileVerifier
// checks file existence/size, HTTPVerifier checks status codes. Unknown
// tools pass through unverified.
type Verifier interface {
	// Name returns the verifier identifier for logging/events.
	Name() string
	// Tools returns the tool names this verifier applies to.
	Tools() []string
	// Verify inspects a tool result and returns a failure reason or "".
	Verify(ctx context.Context, call ToolCall, result ToolResult) string
}

// VerifierMiddleware chains a set of Verifiers into a ToolMiddleware.
// When a tool's result fails verification, the failure reason is appended
// to the result Error so the model sees "VERIFY FAIL: <reason>" and can
// self-correct.
func VerifierMiddleware(verifiers ...Verifier) ToolMiddleware {
	// Build a reverse index: tool name → []Verifier
	idx := make(map[string][]Verifier)
	for _, v := range verifiers {
		for _, t := range v.Tools() {
			idx[t] = append(idx[t], v)
		}
	}
	return func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			res, err := next(ctx, call)
			if err != nil {
				return res, err
			}
			vs, ok := idx[call.Name]
			if !ok {
				return res, nil
			}
			for _, v := range vs {
				if reason := v.Verify(ctx, call, res); reason != "" {
					if res.Error != "" {
						res.Error += "; VERIFY FAIL [" + v.Name() + "]: " + reason
					} else {
						res.Error = "VERIFY FAIL [" + v.Name() + "]: " + reason
					}
					break // first failure stops verification
				}
			}
			return res, nil
		}
	}
}

// BashVerifier checks that bash tool results have exit_code=0.
type BashVerifier struct{}

func (BashVerifier) Name() string               { return "bash" }
func (BashVerifier) Tools() []string             { return []string{"bash"} }
func (BashVerifier) Verify(_ context.Context, _ ToolCall, r ToolResult) string {
	if r.Metadata == nil {
		return "" // no metadata, can't verify
	}
	code, ok := r.Metadata["exit_code"]
	if !ok {
		return ""
	}
	// exit_code is float64 from JSON unmarshalling.
	var ec int
	switch v := code.(type) {
	case float64:
		ec = int(v)
	case int:
		ec = v
	case json.Number:
		ec64, _ := v.Int64()
		ec = int(ec64)
	default:
		return ""
	}
	if ec != 0 {
		return fmt.Sprintf("exit_code=%d (expected 0)", ec)
	}
	return ""
}

// HTTPVerifier checks HTTP status codes in tool results.
type HTTPVerifier struct{}

func (HTTPVerifier) Name() string   { return "http" }
func (HTTPVerifier) Tools() []string { return []string{"http"} }
func (HTTPVerifier) Verify(_ context.Context, _ ToolCall, r ToolResult) string {
	if r.Metadata == nil {
		return ""
	}
	status, ok := r.Metadata["status_code"]
	if !ok {
		return ""
	}
	var sc int
	switch v := status.(type) {
	case float64:
		sc = int(v)
	case int:
		sc = v
	case json.Number:
		sc64, _ := v.Int64()
		sc = int(sc64)
	default:
		return ""
	}
	if sc >= 400 {
		return fmt.Sprintf("HTTP %d", sc)
	}
	return ""
}

// FileReadVerifier checks that file_read returns non-empty output or at least
// doesn't report an error we can detect.
type FileReadVerifier struct{}

func (FileReadVerifier) Name() string   { return "file_read" }
func (FileReadVerifier) Tools() []string { return []string{"file_read"} }
func (FileReadVerifier) Verify(_ context.Context, _ ToolCall, r ToolResult) string {
	if r.Error != "" {
		return r.Error
	}
	// File not found or permission denied often appears in output.
	if strings.Contains(r.Output, "no such file") || strings.Contains(r.Output, "permission denied") {
		return "file_access_error"
	}
	return ""
}
