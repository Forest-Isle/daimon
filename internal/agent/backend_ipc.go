package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// SubprocessRequest is the JSON payload sent from the parent process to a
// subprocess (or Docker container) via stdin.
type SubprocessRequest struct {
	AgentID      string            `json:"agent_id"`
	Task         string            `json:"task"`
	TaskContext  string            `json:"task_context,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
	Model        string            `json:"model,omitempty"`
	MaxTokens    int               `json:"max_tokens,omitempty"`
	MaxIter      int               `json:"max_iterations"`
	AllowedTools []string          `json:"allowed_tools,omitempty"`
	ConfigPath   string            `json:"config_path"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`
	Timeout      string            `json:"timeout,omitempty"` // Go duration string, e.g. "120s"
}

// SubprocessResponse is the JSON payload written to stdout by a subprocess
// when execution completes.
type SubprocessResponse struct {
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"` // Go duration string
}

// WriteRequest serializes a SubprocessRequest to the given writer as a single
// JSON line terminated by a newline.
func WriteRequest(w io.Writer, req *SubprocessRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ReadRequest deserializes a SubprocessRequest from the given reader.
func ReadRequest(r io.Reader) (*SubprocessRequest, error) {
	dec := json.NewDecoder(r)
	var req SubprocessRequest
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	return &req, nil
}

// WriteResponse serializes a SubprocessResponse to the given writer.
func WriteResponse(w io.Writer, resp *SubprocessResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ReadResponse deserializes a SubprocessResponse from the given reader.
func ReadResponse(r io.Reader) (*SubprocessResponse, error) {
	dec := json.NewDecoder(r)
	var resp SubprocessResponse
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

// ToAgentResult converts a SubprocessResponse into an AgentResult.
func (resp *SubprocessResponse) ToAgentResult(agentName string) *AgentResult {
	dur, _ := time.ParseDuration(resp.Duration)
	result := &AgentResult{
		AgentName: agentName,
		Output:    resp.Output,
		Duration:  dur,
	}
	if resp.Error != "" {
		result.Error = fmt.Errorf("%s", resp.Error)
	}
	return result
}
