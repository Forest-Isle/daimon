package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
	"github.com/Forest-Isle/IronClaw/internal/config"
	"github.com/Forest-Isle/IronClaw/internal/store"
)

type HealthSubsystem struct {
	registry *healthRegistry
	srv      *http.Server
}

func (hs *HealthSubsystem) Name() string                { return "health" }
func (hs *HealthSubsystem) Start(_ context.Context) error { return nil }
func (hs *HealthSubsystem) Stop(_ context.Context) error {
	if hs.srv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.srv.Shutdown(shutCtx)
	}
	return nil
}

func InitHealth(cfg *config.Config, db *store.DB) *HealthSubsystem {
	hs := &HealthSubsystem{registry: newHealthRegistry()}
	hs.registry.Register("database", CheckerFunc(func(ctx context.Context) error {
		return db.PingContext(ctx)
	}))
	return hs
}

func (hs *HealthSubsystem) StartServer(cfg *config.Config) {
	if hs.registry == nil { return }
	port := cfg.Health.Port
	if port <= 0 { port = 9090 }
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", hs.handleLiveness)
	mux.HandleFunc("/readiness", hs.handleReadiness)
	mux.HandleFunc("/health", hs.handleHealth)
	hs.srv = &http.Server{
		Addr: fmt.Sprintf(":%d", port), Handler: mux,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second,
	}
	go func() {
		slog.Info("health check server starting", "addr", hs.srv.Addr)
		if err := hs.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health check server error", "err", err)
		}
	}()
}

func (hs *HealthSubsystem) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (hs *HealthSubsystem) handleReadiness(w http.ResponseWriter, r *http.Request) {
	report := hs.registry.Check(r.Context())
	sc := http.StatusOK
	if report.Status != HealthOK { sc = http.StatusServiceUnavailable }
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(sc)
	_ = json.NewEncoder(w).Encode(report)
}

func (hs *HealthSubsystem) handleHealth(w http.ResponseWriter, r *http.Request) {
	report := hs.registry.Check(r.Context())
	sc := http.StatusOK
	if report.Status != HealthOK { sc = http.StatusServiceUnavailable }
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(sc)
	_ = json.NewEncoder(w).Encode(report)
}
