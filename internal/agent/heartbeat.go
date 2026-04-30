package agent

import (
	"context"
	"sync"
	"time"
)

const defaultHeartbeatInterval = 5 * time.Minute

type HeartbeatConfig struct {
	Interval time.Duration
	Enabled  bool
}

type HeartbeatTick struct {
	TriggerTime time.Time
	Reason      string
}

type HeartbeatScheduler struct {
	cfg       HeartbeatConfig
	triggerCh chan HeartbeatTick
	stopCh    chan struct{}
	mu        sync.Mutex
	running   bool

	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewHeartbeatScheduler(cfg HeartbeatConfig) *HeartbeatScheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHeartbeatInterval
	}

	return &HeartbeatScheduler{
		cfg:       cfg,
		triggerCh: make(chan HeartbeatTick, 1),
		stopCh:    make(chan struct{}),
	}
}

func (h *HeartbeatScheduler) Start(ctx context.Context) {
	if h == nil || !h.cfg.Enabled {
		return
	}

	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.wg.Add(1)
	h.mu.Unlock()

	go func() {
		defer h.wg.Done()
		defer func() {
			h.mu.Lock()
			h.running = false
			h.mu.Unlock()
		}()

		ticker := time.NewTicker(h.cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-h.stopCh:
				return
			case t := <-ticker.C:
				h.sendTick(HeartbeatTick{
					TriggerTime: t,
					Reason:      "scheduled",
				})
			}
		}
	}()
}

func (h *HeartbeatScheduler) Stop() {
	if h == nil {
		return
	}

	h.stopOnce.Do(func() {
		close(h.stopCh)
	})
	h.wg.Wait()
}

func (h *HeartbeatScheduler) Ticks() <-chan HeartbeatTick {
	if h == nil {
		return nil
	}

	return h.triggerCh
}

func (h *HeartbeatScheduler) TriggerNow(reason string) {
	if h == nil {
		return
	}

	if reason == "" {
		reason = "external"
	}

	h.sendTick(HeartbeatTick{
		TriggerTime: time.Now(),
		Reason:      reason,
	})
}

func (h *HeartbeatScheduler) sendTick(tick HeartbeatTick) {
	select {
	case h.triggerCh <- tick:
	default:
	}
}
