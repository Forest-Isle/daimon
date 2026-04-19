package dashboard

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Forest-Isle/IronClaw/internal/cogmetrics"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

type ServerDeps struct {
	DB        *store.DB
	Hub       *Hub
	Tracker   *AgentStateTracker
	Collector *cogmetrics.Collector
	StaticFS  fs.FS
	Token     string
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("dashboard: failed to encode response", "err", err)
	}
}

func NewServerMux(deps ServerDeps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/agent/state", deps.authMiddleware(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, deps.Tracker.Snapshot())
	}))

	mux.HandleFunc("/api/sessions", deps.authMiddleware(func(w http.ResponseWriter, _ *http.Request) {
		if deps.DB == nil {
			http.Error(w, "database not available", http.StatusServiceUnavailable)
			return
		}
		rows, err := deps.DB.Query("SELECT id, channel, channel_id, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT 50")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = rows.Close() }()

		type sessionInfo struct {
			ID        string `json:"id"`
			Channel   string `json:"channel"`
			ChannelID string `json:"channel_id"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		}
		var sessions []sessionInfo
		for rows.Next() {
			var s sessionInfo
			if err := rows.Scan(&s.ID, &s.Channel, &s.ChannelID, &s.CreatedAt, &s.UpdatedAt); err != nil {
				continue
			}
			sessions = append(sessions, s)
		}
		writeJSON(w, sessions)
	}))

	mux.HandleFunc("/api/sessions/{id}/messages", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			http.Error(w, "database not available", http.StatusServiceUnavailable)
			return
		}
		sessionID := r.PathValue("id")
		rows, err := deps.DB.Query(
			"SELECT id, role, content, tool_name, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC",
			sessionID,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = rows.Close() }()

		type msg struct {
			ID        string  `json:"id"`
			Role      string  `json:"role"`
			Content   string  `json:"content"`
			ToolName  *string `json:"tool_name,omitempty"`
			CreatedAt string  `json:"created_at"`
		}
		var msgs []msg
		for rows.Next() {
			var m msg
			if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.ToolName, &m.CreatedAt); err != nil {
				continue
			}
			msgs = append(msgs, m)
		}
		writeJSON(w, msgs)
	}))

	mux.HandleFunc("/api/sessions/{id}/tools", deps.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if deps.DB == nil {
			http.Error(w, "database not available", http.StatusServiceUnavailable)
			return
		}
		sessionID := r.PathValue("id")
		rows, err := deps.DB.Query(
			"SELECT id, tool_name, input, output, status, duration_ms, created_at FROM tool_log WHERE session_id = ? ORDER BY created_at ASC",
			sessionID,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = rows.Close() }()

		type toolEntry struct {
			ID         string `json:"id"`
			ToolName   string `json:"tool_name"`
			Input      string `json:"input"`
			Output     string `json:"output"`
			Status     string `json:"status"`
			DurationMs int64  `json:"duration_ms"`
			CreatedAt  string `json:"created_at"`
		}
		var entries []toolEntry
		for rows.Next() {
			var e toolEntry
			if err := rows.Scan(&e.ID, &e.ToolName, &e.Input, &e.Output, &e.Status, &e.DurationMs, &e.CreatedAt); err != nil {
				continue
			}
			entries = append(entries, e)
		}
		writeJSON(w, entries)
	}))

	if deps.Hub != nil {
		mux.HandleFunc("/ws", deps.authMiddleware(deps.Hub.HandleWS))
	}

	if deps.Collector != nil {
		mux.HandleFunc("/api/metrics/health", deps.authMiddleware(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, deps.Collector.Snapshot())
		}))
	}

	mux.Handle("/", spaHandler{fs: deps.StaticFS})

	return mux
}

type spaHandler struct {
	fs fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	if _, err := fs.Stat(h.fs, path); err != nil {
		path = "index.html"
	}

	http.ServeFileFS(w, r, h.fs, path)
}

func (d ServerDeps) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	if d.Token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != d.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func StartServer(cfg config.DashboardConfig, deps ServerDeps) {
	deps.Token = cfg.Token
	handler := NewServerMux(deps)
	slog.Info("dashboard server starting", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		slog.Error("dashboard server error", "err", err)
	}
}
