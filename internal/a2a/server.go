package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	taskEventTypeStatus   = "status"
	taskEventTypeProgress = "progress"
	taskEventTypeOutput   = "output"
	taskEventTypeError    = "error"

	pushEventTaskStart    = "task.start"
	pushEventTaskComplete = "task.complete"
	pushEventTaskError    = "task.error"
)

// TaskHandler handles task execution for the A2A server.
type TaskHandler func(ctx context.Context, task *Task) (*TaskOutput, error)

// Server exposes an A2A-compatible HTTP API.
type Server struct {
	card       AgentCard
	handler    TaskHandler
	httpClient *http.Client
	tasks      map[string]*Task
	pushSubs   map[string]*PushSubscription
	eventChans map[string]chan TaskEvent
	mu         sync.RWMutex

	srv     *http.Server
	cancels map[string]context.CancelFunc
}

// NewServer creates a new A2A server with the provided discovery card and task handler.
func NewServer(card AgentCard, handler TaskHandler) *Server {
	return &Server{
		card:       card,
		handler:    handler,
		httpClient: http.DefaultClient,
		tasks:      make(map[string]*Task),
		pushSubs:   make(map[string]*PushSubscription),
		eventChans: make(map[string]chan TaskEvent),
		cancels:    make(map[string]context.CancelFunc),
	}
}

// ServeHTTP routes A2A protocol requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/agent.json":
		s.writeJSON(w, http.StatusOK, s.card)
	case r.URL.Path == "/tasks":
		s.handleTasks(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/tasks/stream":
		s.handleStreamTask(w, r)
	case strings.HasPrefix(r.URL.Path, "/tasks/"):
		s.handleTaskByID(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/push/subscribe":
		s.handlePushSubscribe(w, r)
	case strings.HasPrefix(r.URL.Path, "/push/subscribe/"):
		s.handlePushUnsubscribe(w, r)
	default:
		http.NotFound(w, r)
	}
}

// Start starts the HTTP server and blocks until it stops.
func (s *Server) Start(addr string) error {
	s.srv = &http.Server{
		Addr:    addr,
		Handler: s,
	}

	slog.Info("a2a server starting", "addr", addr)
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("a2a: start server: %w", err)
	}
	return nil
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("a2a: stop server: %w", err)
	}
	return nil
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateTask(w, r)
	case http.MethodGet:
		s.handleListTasks(w, r)
	default:
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	input, priority, ok := s.decodeTaskRequest(w, r)
	if !ok {
		return
	}

	task := s.newTask(input, priority)
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.tasks[task.ID] = cloneTask(task)
	s.cancels[task.ID] = cancel
	s.mu.Unlock()

	s.broadcastTaskEvent(task.ID, taskEventTypeStatus, string(TaskStateProcessing))
	go s.runTask(ctx, task.ID)

	s.writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	filter := TaskState(r.URL.Query().Get("status"))

	s.mu.RLock()
	tasks := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if filter != "" && task.State != filter {
			continue
		}
		tasks = append(tasks, *cloneTask(task))
	}
	s.mu.RUnlock()

	s.writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleStreamTask(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	input, priority, ok := s.decodeTaskRequest(w, r)
	if !ok {
		return
	}

	task := s.newTask(input, priority)
	ctx, cancel := context.WithCancel(r.Context())
	eventChan := make(chan TaskEvent, 16)

	s.mu.Lock()
	s.tasks[task.ID] = cloneTask(task)
	s.cancels[task.ID] = cancel
	s.eventChans[task.ID] = eventChan
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusAccepted)

	s.broadcastTaskEvent(task.ID, taskEventTypeStatus, string(TaskStateProcessing))
	go s.runTask(ctx, task.ID)

	for {
		select {
		case <-r.Context().Done():
			s.removeEventChan(task.ID, eventChan)
			return
		case event, ok := <-eventChan:
			if !ok {
				return
			}
			if err := s.writeSSE(w, event); err != nil {
				s.removeEventChan(task.ID, eventChan)
				return
			}
			flusher.Flush()
			if event.Type == taskEventTypeOutput || event.Type == taskEventTypeError {
				s.removeEventChan(task.ID, eventChan)
				return
			}
		}
	}
}

func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/tasks/")
	if taskID == "" || strings.Contains(taskID, "/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		task, ok := s.getTask(taskID)
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.writeJSON(w, http.StatusOK, task)
	case http.MethodDelete:
		if !s.cancelTask(taskID) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var sub PushSubscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(sub.CallbackURL) == "" {
		http.Error(w, "callback_url is required", http.StatusBadRequest)
		return
	}
	if len(sub.Events) == 0 {
		sub.Events = []string{pushEventTaskStart, pushEventTaskComplete, pushEventTaskError}
	}
	if sub.ID == "" {
		sub.ID = uuid.NewString()
	}

	s.mu.Lock()
	s.pushSubs[sub.ID] = clonePushSubscription(&sub)
	s.mu.Unlock()

	s.writeJSON(w, http.StatusCreated, sub)
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/push/subscribe/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	s.mu.Lock()
	_, ok := s.pushSubs[id]
	if ok {
		delete(s.pushSubs, id)
	}
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runTask(ctx context.Context, taskID string) {
	task, ok := s.getTask(taskID)
	if !ok {
		return
	}

	s.dispatchWebhook(pushEventTaskStart, TaskEvent{
		ID:        uuid.NewString(),
		TaskID:    taskID,
		Type:      taskEventTypeStatus,
		Data:      string(TaskStateProcessing),
		Timestamp: time.Now().UTC(),
	})
	s.broadcastTaskEvent(taskID, taskEventTypeProgress, "task accepted")

	output, err := s.handler(ctx, task)

	s.mu.Lock()
	current, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return
	}

	now := time.Now().UTC()
	current.UpdatedAt = now
	delete(s.cancels, taskID)

	var eventType string
	var eventData string
	var pushEvent string

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		current.State = TaskStateFailed
		current.Output = TaskOutput{Text: "task canceled"}
		eventType = taskEventTypeError
		eventData = current.Output.Text
		pushEvent = pushEventTaskError
	case err != nil:
		current.State = TaskStateFailed
		current.Output = TaskOutput{Text: err.Error()}
		eventType = taskEventTypeError
		eventData = err.Error()
		pushEvent = pushEventTaskError
	default:
		current.State = TaskStateCompleted
		if output != nil {
			current.Output = *output
		}
		eventType = taskEventTypeOutput
		eventData = current.Output.Text
		pushEvent = pushEventTaskComplete
	}

	finalTask := cloneTask(current)
	s.mu.Unlock()

	s.broadcastTaskEvent(taskID, taskEventTypeStatus, string(finalTask.State))
	s.broadcastTaskEvent(taskID, eventType, eventData)
	s.dispatchWebhook(pushEvent, TaskEvent{
		ID:        uuid.NewString(),
		TaskID:    taskID,
		Type:      eventType,
		Data:      eventData,
		Timestamp: now,
	})
	s.closeEventChan(taskID)
}

func (s *Server) getTask(taskID string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return nil, false
	}
	return cloneTask(task), true
}

func (s *Server) cancelTask(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return false
	}

	cancel, hasCancel := s.cancels[taskID]
	if hasCancel {
		cancel()
		delete(s.cancels, taskID)
	}

	task.State = TaskStateFailed
	task.Output = TaskOutput{Text: "task canceled"}
	task.UpdatedAt = time.Now().UTC()
	return true
}

func (s *Server) decodeTaskRequest(w http.ResponseWriter, r *http.Request) (TaskInput, TaskPriority, bool) {
	var payload struct {
		Message  string       `json:"message"`
		Context  string       `json:"context,omitempty"`
		Priority TaskPriority `json:"priority,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return TaskInput{}, "", false
	}

	priority := payload.Priority
	if priority == "" {
		priority = PriorityNormal
	}
	return TaskInput{Message: payload.Message, Context: payload.Context}, priority, true
}

func (s *Server) newTask(input TaskInput, priority TaskPriority) *Task {
	now := time.Now().UTC()
	return &Task{
		ID:        uuid.NewString(),
		State:     TaskStateProcessing,
		Priority:  priority,
		Input:     input,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *Server) broadcastTaskEvent(taskID, eventType, data string) {
	event := TaskEvent{
		ID:        uuid.NewString(),
		TaskID:    taskID,
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UTC(),
	}

	s.mu.RLock()
	eventChan := s.eventChans[taskID]
	s.mu.RUnlock()
	if eventChan != nil {
		select {
		case eventChan <- event:
		default:
		}
	}
}

func (s *Server) dispatchWebhook(eventName string, event TaskEvent) {
	subs := s.listPushSubscribers(eventName)
	for _, sub := range subs {
		go s.sendWebhook(sub, event)
	}
}

func (s *Server) listPushSubscribers(eventName string) []*PushSubscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs := make([]*PushSubscription, 0, len(s.pushSubs))
	for _, sub := range s.pushSubs {
		if includesEvent(sub.Events, eventName) {
			subs = append(subs, clonePushSubscription(sub))
		}
	}
	return subs
}

func (s *Server) sendWebhook(sub *PushSubscription, event TaskEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		return
	}

	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(http.MethodPost, sub.CallbackURL, strings.NewReader(string(body)))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, doErr := s.httpClient.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
					return
				}
			}
		}

		if attempt == 2 {
			return
		}

		time.Sleep(time.Duration(1<<attempt) * 200 * time.Millisecond)
	}
}

func (s *Server) closeEventChan(taskID string) {
	s.mu.Lock()
	eventChan := s.eventChans[taskID]
	delete(s.eventChans, taskID)
	s.mu.Unlock()
	if eventChan != nil {
		close(eventChan)
	}
}

func (s *Server) removeEventChan(taskID string, target chan TaskEvent) {
	s.mu.Lock()
	eventChan := s.eventChans[taskID]
	if eventChan == target {
		delete(s.eventChans, taskID)
	}
	s.mu.Unlock()
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeSSE(w http.ResponseWriter, event TaskEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", body)
	return err
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}
	cloned := *task
	if len(task.Output.Artifacts) > 0 {
		cloned.Output.Artifacts = append([]Artifact(nil), task.Output.Artifacts...)
	}
	return &cloned
}

func clonePushSubscription(sub *PushSubscription) *PushSubscription {
	if sub == nil {
		return nil
	}
	cloned := *sub
	if len(sub.Events) > 0 {
		cloned.Events = append([]string(nil), sub.Events...)
	}
	return &cloned
}

func includesEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}
