package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

func startHTTPServer(addr string, db *store.DB, memStore memory.Store) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query("SELECT id, channel, channel_id, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT 50")
		if err != nil {
			http.Error(w, err.Error(), 500)
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

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessions)
	})

	// Memorywire protocol endpoint for vendor-neutral agent memory operations
	// (remember, recall, forget, merge, expire). Available via POST /memorywire
	// when the memory store is available.
	if memStore != nil {
		mwh := memory.NewMemorywireHandler(memStore)
		mux.Handle("/memorywire", mwh)
		slog.Info("http admin server: memorywire endpoint registered", "path", "/memorywire")
	}

	slog.Info("http admin server starting", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("http server error", "err", err)
	}
}
