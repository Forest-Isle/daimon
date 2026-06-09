package gateway

import (
	"context"
	"log/slog"
	"sync"
	"github.com/Forest-Isle/IronClaw/internal/config"
)

type ConfigSubsystem struct {
	cfg     *config.Config
	mu      sync.RWMutex
	watcher *config.ConfigWatcher
}

func (cs *ConfigSubsystem) Name() string                { return "config" }
func (cs *ConfigSubsystem) Start(_ context.Context) error { return nil }
func (cs *ConfigSubsystem) Stop(_ context.Context) error {
	if cs.watcher != nil { cs.watcher.Stop() }
	return nil
}

func InitConfig(cfg *config.Config, cfgPath string) *ConfigSubsystem {
	cs := &ConfigSubsystem{cfg: cfg}
	if cfgPath != "" {
		w, err := config.NewConfigWatcher(cfgPath)
		if err != nil { slog.Warn("config: hot-reload unavailable", "err", err) } else { cs.watcher = w }
	}
	return cs
}

func (cs *ConfigSubsystem) Config() *config.Config {
	cs.mu.RLock(); defer cs.mu.RUnlock()
	return cs.cfg
}

func (cs *ConfigSubsystem) OnReload(fn func(*config.Config)) {
	if cs.watcher != nil {
		cs.watcher.OnReload(func(newCfg *config.Config) {
			cs.mu.Lock(); cs.cfg = newCfg; cs.mu.Unlock()
			fn(newCfg)
		})
	}
}
