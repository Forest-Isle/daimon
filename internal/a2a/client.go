package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	maxRetries     = 2
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	agentCard  *AgentCard
}

// NewClient creates a new A2A client for the provided base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// SetTimeout updates the HTTP timeout used by the client.
func (c *Client) SetTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	c.httpClient.Timeout = timeout
}

// Discover fetches and caches the remote agent card.
func (c *Client) Discover(ctx context.Context) (*AgentCard, error) {
	if c.agentCard != nil {
		cardCopy := *c.agentCard
		return &cardCopy, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/.well-known/agent.json", nil)
	if err != nil {
		return nil, fmt.Errorf("a2a: discover: %w", err)
	}

	var card AgentCard
	if err := c.doJSON(req, &card); err != nil {
		return nil, fmt.Errorf("a2a: discover: %w", err)
	}

	c.agentCard = &card
	cardCopy := card
	return &cardCopy, nil
}

// SendTask creates a task using the legacy task submission endpoint.
func (c *Client) SendTask(ctx context.Context, input TaskInput) (*Task, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("a2a: send task: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tasks", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("a2a: send task: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var task Task
	if err := c.doJSON(req, &task); err != nil {
		return nil, fmt.Errorf("a2a: send task: %w", err)
	}
	return &task, nil
}

// StreamTask creates a task and streams task events from the SSE endpoint.
func (c *Client) StreamTask(ctx context.Context, input TaskInput) (<-chan TaskEvent, <-chan error, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, nil, fmt.Errorf("a2a: stream task: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/tasks/stream", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("a2a: stream task: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("a2a: stream task: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, fmt.Errorf("a2a: stream task: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	events := make(chan TaskEvent)
	errs := make(chan error, 1)
	go c.consumeSSE(resp.Body, events, errs)
	return events, errs, nil
}

// GetTask retrieves the latest state for a task.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/tasks/"+taskID, nil)
	if err != nil {
		return nil, fmt.Errorf("a2a: get task: %w", err)
	}

	var task Task
	if err := c.doJSON(req, &task); err != nil {
		return nil, fmt.Errorf("a2a: get task: %w", err)
	}
	return &task, nil
}

// ListTasks returns tasks, optionally filtered by status.
func (c *Client) ListTasks(ctx context.Context, status TaskState) ([]Task, error) {
	endpoint := c.baseURL + "/tasks"
	if status != "" {
		values := url.Values{}
		values.Set("status", string(status))
		endpoint += "?" + values.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("a2a: list tasks: %w", err)
	}

	var tasks []Task
	if err := c.doJSON(req, &tasks); err != nil {
		return nil, fmt.Errorf("a2a: list tasks: %w", err)
	}
	return tasks, nil
}

// SubscribePush registers a webhook push subscription.
func (c *Client) SubscribePush(ctx context.Context, sub PushSubscription) (*PushSubscription, error) {
	body, err := json.Marshal(sub)
	if err != nil {
		return nil, fmt.Errorf("a2a: subscribe push: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/push/subscribe", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("a2a: subscribe push: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var created PushSubscription
	if err := c.doJSON(req, &created); err != nil {
		return nil, fmt.Errorf("a2a: subscribe push: %w", err)
	}
	return &created, nil
}

// UnsubscribePush removes a webhook push subscription by ID.
func (c *Client) UnsubscribePush(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/push/subscribe/"+id, nil)
	if err != nil {
		return fmt.Errorf("a2a: unsubscribe push: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("a2a: unsubscribe push: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("a2a: unsubscribe push: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// CancelTask cancels a running task.
func (c *Client) CancelTask(ctx context.Context, taskID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/tasks/"+taskID, nil)
	if err != nil {
		return fmt.Errorf("a2a: cancel task: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("a2a: cancel task: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("a2a: cancel task: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) consumeSSE(body io.ReadCloser, events chan<- TaskEvent, errs chan<- error) {
	defer close(events)
	defer close(errs)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var event TaskEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			errs <- fmt.Errorf("decode sse event: %w", err)
			return
		}

		select {
		case events <- event:
		}
	}

	if err := scanner.Err(); err != nil {
		errs <- fmt.Errorf("read sse stream: %w", err)
	}
}

func (c *Client) doJSON(req *http.Request, dest any) error {
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		cloned := req.Clone(req.Context())
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("reset request body: %w", err)
			}
			cloned.Body = body
		}

		resp, err := c.httpClient.Do(cloned)
		if err == nil {
			if resp.StatusCode < http.StatusInternalServerError {
				return resp, nil
			}

			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		} else {
			lastErr = err
		}

		if attempt == maxRetries {
			break
		}

		delay := time.Duration(1<<attempt) * time.Second
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}

	return nil, lastErr
}
