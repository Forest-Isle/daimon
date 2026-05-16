package a2a

import (
	"bufio"
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
		Tools: []AgentTool{
			{
				Name:        "search",
				Description: "Search knowledge",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		Memory:        true,
		KnowledgeBase: true,
		CodeIntel:     true,
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
	if len(decoded.Tools) != 1 || decoded.Tools[0].Name != "search" {
		t.Fatalf("unexpected tools after round-trip: %#v", decoded.Tools)
	}
	if !decoded.Memory || !decoded.KnowledgeBase || !decoded.CodeIntel {
		t.Fatalf("expected capability flags to round-trip: %#v", decoded)
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
	if final.Priority != PriorityNormal {
		t.Fatalf("expected default priority, got %s", final.Priority)
	}
	if final.CreatedAt.IsZero() || final.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps to be set: %#v", final)
	}
}

func TestClientStreamTask(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{
		Name:    "ironclaw",
		URL:     "http://127.0.0.1",
		Version: "2.0",
	}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		return &TaskOutput{Text: "streamed: " + task.Input.Message}, nil
	})

	client := newInMemoryClient("http://a2a.test", server)
	events, errs, err := client.StreamTask(context.Background(), TaskInput{Message: "hello"})
	if err != nil {
		t.Fatalf("stream task: %v", err)
	}

	var got []TaskEvent
	for event := range events {
		got = append(got, event)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	}

	if len(got) < 3 {
		t.Fatalf("expected multiple task events, got %#v", got)
	}
	if got[0].Type != "status" || got[len(got)-1].Type != "output" {
		t.Fatalf("unexpected event sequence: %#v", got)
	}
	if got[len(got)-1].Data != "streamed: hello" {
		t.Fatalf("unexpected final event: %#v", got[len(got)-1])
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

func TestClientListTasksAndStatusFilter(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{Name: "ironclaw", Version: "2.0"}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		if task.Input.Message == "slow" {
			time.Sleep(150 * time.Millisecond)
		}
		return &TaskOutput{Text: task.Input.Message}, nil
	})

	client := newInMemoryClient("http://a2a.test", server)
	if _, err := client.SendTask(context.Background(), TaskInput{Message: "slow"}); err != nil {
		t.Fatalf("send slow task: %v", err)
	}
	if _, err := client.SendTask(context.Background(), TaskInput{Message: "fast"}); err != nil {
		t.Fatalf("send fast task: %v", err)
	}

	pending, err := client.ListTasks(context.Background(), TaskStateProcessing)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(pending) == 0 {
		t.Fatalf("expected processing tasks, got %#v", pending)
	}

	all, err := client.ListTasks(context.Background(), "")
	if err != nil {
		t.Fatalf("list all tasks: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected all tasks, got %#v", all)
	}
}

func TestClientPushSubscriptionLifecycle(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{Name: "ironclaw", Version: "2.0"}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		return &TaskOutput{Text: "ok"}, nil
	})
	client := newInMemoryClient("http://a2a.test", server)

	sub, err := client.SubscribePush(context.Background(), PushSubscription{
		CallbackURL: "http://callback.test/hook",
		Events:      []string{"task.complete"},
	})
	if err != nil {
		t.Fatalf("subscribe push: %v", err)
	}
	if sub.ID == "" {
		t.Fatalf("expected subscription ID: %#v", sub)
	}

	if err := client.UnsubscribePush(context.Background(), sub.ID); err != nil {
		t.Fatalf("unsubscribe push: %v", err)
	}
}

func TestServerSSEFormat(t *testing.T) {
	t.Parallel()

	server := NewServer(AgentCard{Name: "ironclaw", Version: "2.0"}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		return &TaskOutput{Text: "ok"}, nil
	})

	body := strings.NewReader(`{"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/stream", body)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, req)
	resp := recorder.Result()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("unexpected content type: %s", got)
	}

	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		t.Fatal("expected SSE payload")
	}
	line := scanner.Text()
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("unexpected SSE line: %q", line)
	}
}

func TestServerWebhookPush(t *testing.T) {
	t.Parallel()

	received := make(chan TaskEvent, 4)
	server := NewServer(AgentCard{Name: "ironclaw", Version: "2.0"}, func(ctx context.Context, task *Task) (*TaskOutput, error) {
		return &TaskOutput{Text: "done"}, nil
	})
	server.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var event TaskEvent
			if err := json.NewDecoder(req.Body).Decode(&event); err != nil {
				t.Fatalf("decode webhook event: %v", err)
			}
			received <- event
			recorder := httptest.NewRecorder()
			recorder.WriteHeader(http.StatusOK)
			return recorder.Result(), nil
		}),
	}
	client := newInMemoryClient("http://a2a.test", server)

	_, err := client.SubscribePush(context.Background(), PushSubscription{
		CallbackURL: "http://callback.test/hook",
		Events:      []string{"task.complete"},
	})
	if err != nil {
		t.Fatalf("subscribe push: %v", err)
	}
	if _, err := client.SendTask(context.Background(), TaskInput{Message: "hello"}); err != nil {
		t.Fatalf("send task: %v", err)
	}

	select {
	case event := <-received:
		if event.Type != "output" {
			t.Fatalf("unexpected webhook event: %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected webhook event")
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
