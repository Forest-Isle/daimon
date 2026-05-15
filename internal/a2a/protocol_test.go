package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentCardJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentCard{
		Name:        "ironclaw",
		Description: "A2A agent",
		URL:         "http://localhost:8080",
		Version:     "1.0",
		Capabilities: Capabilities{
			Streaming:         true,
			PushNotifications: false,
		},
		Skills: []AgentSkill{
			{
				Name:        "chat",
				Description: "General chat",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded AgentCard
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != original.Name || decoded.URL != original.URL || decoded.Version != original.Version {
		t.Fatalf("round-trip mismatch: %#v != %#v", decoded, original)
	}
	if len(decoded.Skills) != 1 || decoded.Skills[0].Name != "chat" {
		t.Fatalf("unexpected skills after round-trip: %#v", decoded.Skills)
	}
}

func TestClientDiscoverCachesAgentCard(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	card := AgentCard{
		Name:        "remote-agent",
		Description: "Remote A2A endpoint",
		URL:         "http://example.test",
		Version:     "1.0",
		Capabilities: Capabilities{
			Streaming: true,
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent.json" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})

	client := newInMemoryClient("http://a2a.test", handler)
	ctx := context.Background()

	discovered, err := client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if discovered.Name != card.Name {
		t.Fatalf("unexpected agent name: %s", discovered.Name)
	}

	discoveredAgain, err := client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover cached: %v", err)
	}
	if discoveredAgain.Name != card.Name {
		t.Fatalf("unexpected cached agent name: %s", discoveredAgain.Name)
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one discover request, got %d", requests.Load())
	}
}

func TestClientSendTaskFlow(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{
		Name:    "ironclaw",
		URL:     "http://127.0.0.1",
		Version: "1.0",
	}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		time.Sleep(50 * time.Millisecond)
		return &TaskOutput{Text: "processed: " + task.Input.Message}, nil
	})

	client := newInMemoryClient("http://a2a.test", server)
	client.SetTimeout(5 * time.Second)

	task, err := client.SendTask(context.Background(), TaskInput{Message: "hello"})
	if err != nil {
		t.Fatalf("send task: %v", err)
	}
	if task.State != TaskStateProcessing {
		t.Fatalf("expected processing state, got %s", task.State)
	}

	var final *Task
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		final, err = client.GetTask(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if final.State == TaskStateCompleted {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if final == nil || final.State != TaskStateCompleted {
		t.Fatalf("expected completed task, got %#v", final)
	}
	if final.Output.Text != "processed: hello" {
		t.Fatalf("unexpected output: %#v", final.Output)
	}
}

func TestServerHandlerIntegrationAndCancel(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	server := NewServer(AgentCard{
		Name:    "ironclaw",
		URL:     "http://127.0.0.1",
		Version: "1.0",
	}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	client := newInMemoryClient("http://a2a.test", server)
	task, err := client.SendTask(context.Background(), TaskInput{Message: "cancel me"})
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	<-started

	if err := client.CancelTask(context.Background(), task.ID); err != nil {
		t.Fatalf("cancel task: %v", err)
	}

	canceled, err := client.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get canceled task: %v", err)
	}
	if canceled.State != TaskStateFailed {
		t.Fatalf("expected failed state after cancel, got %s", canceled.State)
	}
	if canceled.Output.Text != "task canceled" {
		t.Fatalf("unexpected cancel output: %#v", canceled.Output)
	}
}

func TestClientErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()

		client := NewClient("http://127.0.0.1:1")
		client.SetTimeout(100 * time.Millisecond)

		_, err := client.Discover(context.Background())
		if err == nil {
			t.Fatal("expected discover error")
		}
		if !strings.Contains(err.Error(), "a2a: discover:") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("server 500 retries", func(t *testing.T) {
		t.Parallel()

		var attempts atomic.Int32
		client := newInMemoryClient("http://a2a.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		client.SetTimeout(5 * time.Second)

		_, err := client.SendTask(context.Background(), TaskInput{Message: "hello"})
		if err == nil {
			t.Fatal("expected send task error")
		}
		if !strings.Contains(err.Error(), "a2a: send task: unexpected status 500") {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempts.Load() != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts.Load())
		}
	})
}

func TestServerStopWithoutStart(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{Name: "ironclaw", Version: "1.0"}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		return &TaskOutput{Text: "ok"}, nil
	})
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("stop without start: %v", err)
	}
}

func TestServerHandlerFailure(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{
		Name:    "ironclaw",
		URL:     "http://127.0.0.1",
		Version: "1.0",
	}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		return nil, errors.New("handler failed")
	})

	client := newInMemoryClient("http://a2a.test", server)
	task, err := client.SendTask(context.Background(), TaskInput{Message: "fail"})
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	var final *Task
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		final, err = client.GetTask(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if final.State == TaskStateFailed {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if final == nil || final.State != TaskStateFailed {
		t.Fatalf("expected failed task, got %#v", final)
	}
	if final.Output.Text != "handler failed" {
		t.Fatalf("unexpected failure output: %#v", final.Output)
	}
}

func newInMemoryClient(baseURL string, handler http.Handler) *Client {
	client := NewClient(baseURL)
	client.httpClient = &http.Client{
		Timeout: defaultTimeout,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			return recorder.Result(), nil
		}),
	}
	return client
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL = cloneURL(req.URL)
	return fn(cloned)
}

func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	clone := *u
	return &clone
}
