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

	"github.com/google/uuid"
)

type TaskHandler func(ctx context.Context, task *Task) (*TaskOutput, error)

type Server struct {
	card    AgentCard
	handler TaskHandler
	tasks   map[string]*Task
	mu      sync.RWMutex

	srv     *http.Server
	cancels map[string]context.CancelFunc
}

func NewServer(card AgentCard, handler TaskHandler) *Server {
	return &Server{
		card:    card,
		handler: handler,
		tasks:   make(map[string]*Task),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/agent.json":
		s.writeJSON(w, http.StatusOK, s.card)
	case r.URL.Path == "/tasks":
		s.handleTasks(w, r)
	case strings.HasPrefix(r.URL.Path, "/tasks/"):
		s.handleTaskByID(w, r)
	default:
		http.NotFound(w, r)
	}
}

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
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	var input TaskInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	task := &Task{
		ID:    uuid.NewString(),
		State: TaskStateProcessing,
		Input: input,
	}

	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.tasks[task.ID] = cloneTask(task)
	s.cancels[task.ID] = cancel
	s.mu.Unlock()

	go s.runTask(ctx, task.ID)

	s.writeJSON(w, http.StatusAccepted, task)
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

func (s *Server) runTask(ctx context.Context, taskID string) {
	task, ok := s.getTask(taskID)
	if !ok {
		return
	}

	output, err := s.handler(ctx, task)

	s.mu.Lock()
	defer s.mu.Unlock()
	defer delete(s.cancels, taskID)

	current, ok := s.tasks[taskID]
	if !ok {
		return
	}

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		current.State = TaskStateFailed
		current.Output = TaskOutput{Text: "task canceled"}
	case err != nil:
		current.State = TaskStateFailed
		current.Output = TaskOutput{Text: err.Error()}
	default:
		current.State = TaskStateCompleted
		if output != nil {
			current.Output = *output
		}
	}
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
	return true
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
