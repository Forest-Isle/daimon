package browser_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// CDPClient communicates with Chrome/Chromium via the DevTools Protocol.
type CDPClient struct {
	ws       *websocket.Conn
	msgID    atomic.Int64
	pending  map[int64]chan *cdpResponse
	mu       sync.Mutex
	done     chan struct{}
}

type cdpRequest struct {
	ID     int64                  `json:"id"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewCDPClient connects to a Chrome DevTools Protocol endpoint.
// debugURL should be like "http://localhost:9222".
func NewCDPClient(debugURL string) (*CDPClient, error) {
	// Fetch WebSocket URL from HTTP endpoint
	wsURL, err := fetchWSUrl(debugURL)
	if err != nil {
		return nil, fmt.Errorf("fetch ws url: %w", err)
	}

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial ws: %w", err)
	}

	c := &CDPClient{
		ws:      conn,
		pending: make(map[int64]chan *cdpResponse),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func fetchWSUrl(debugURL string) (string, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(strings.TrimRight(debugURL, "/") + "/json/version")
	if err != nil {
		// Try alternative: fetch list and use first page
		resp2, err2 := httpClient.Get(strings.TrimRight(debugURL, "/") + "/json")
		if err2 != nil {
			return "", fmt.Errorf("http get: %w (and %w)", err, err2)
		}
		defer resp2.Body.Close()
		var targets []struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		}
		if err := json.NewDecoder(resp2.Body).Decode(&targets); err != nil {
			return "", fmt.Errorf("decode targets: %w", err)
		}
		if len(targets) == 0 || targets[0].WebSocketDebuggerURL == "" {
			return "", fmt.Errorf("no debuggable targets found")
		}
		return targets[0].WebSocketDebuggerURL, nil
	}
	defer resp.Body.Close()
	var info struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("decode version: %w", err)
	}
	if info.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("no webSocketDebuggerUrl in response")
	}
	return info.WebSocketDebuggerURL, nil
}

func (c *CDPClient) readLoop() {
	defer close(c.done)
	for {
		_, message, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var resp cdpResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue // event, not a response
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- &resp
		}
	}
}

// SendCommand sends a CDP command and waits for the response.
func (c *CDPClient) SendCommand(ctx context.Context, method string, params map[string]interface{}) (map[string]interface{}, error) {
	id := c.msgID.Add(1)
	req := cdpRequest{ID: id, Method: method, Params: params}

	ch := make(chan *cdpResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	data, _ := json.Marshal(req)
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("cdp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, fmt.Errorf("decode result: %w", err)
		}
		return result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Evaluate executes JavaScript in the page and returns the result as string.
func (c *CDPClient) Evaluate(ctx context.Context, expression string) (string, error) {
	result, err := c.SendCommand(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	r := result["result"].(map[string]interface{})
	if r["type"] == "string" {
		if v, ok := r["value"].(string); ok {
			return v, nil
		}
	}
	// For non-string results, return JSON representation
	if v, ok := r["value"]; ok {
		if b, err := json.Marshal(v); err == nil {
			return string(b), nil
		}
	}
	return "", nil
}

// Close closes the WebSocket connection.
func (c *CDPClient) Close() error {
	return c.ws.Close()
}

