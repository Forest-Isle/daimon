package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func NewClient(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

func (c *Client) SetTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	c.httpClient.Timeout = timeout
}

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
