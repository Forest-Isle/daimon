package cogmetrics

import (
	"context"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/evolution"
)

// Collector implements evolution.Hook and accumulates cognitive health
// metrics from the event stream. All methods are safe for concurrent use.
type Collector struct {
	mu sync.RWMutex

	assertionPassRate RollingAvg
	replanRate        RollingAvg
	replanSuccess     RollingAvg // success rate when replans occurred
	noReplanSuccess   RollingAvg // success rate when no replans occurred
	avgConfidence     RollingAvg
	toolReliability   map[string]*RollingAvg
	complexitySuccess map[string]*RollingAvg
	strategyVersion   int
	totalEpisodes     int64
	totalReflections  int64

	startedAt time.Time
}

var _ evolution.Hook = (*Collector)(nil)

const defaultWindowSize = 100

// NewCollector creates a metrics collector with rolling window averages.
func NewCollector() *Collector {
	return &Collector{
		assertionPassRate: NewRollingAvg(defaultWindowSize),
		replanRate:        NewRollingAvg(defaultWindowSize),
		replanSuccess:     NewRollingAvg(defaultWindowSize),
		noReplanSuccess:   NewRollingAvg(defaultWindowSize),
		avgConfidence:     NewRollingAvg(defaultWindowSize),
		toolReliability:   make(map[string]*RollingAvg),
		complexitySuccess: make(map[string]*RollingAvg),
		startedAt:         time.Now(),
	}
}

func (c *Collector) Name() string { return "cogmetrics_collector" }

func (c *Collector) OnReflectionComplete(_ context.Context, event evolution.ReflectionEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalReflections++
	c.avgConfidence.Add(event.Confidence)

	comp := event.Complexity
	if comp != "" {
		ra, ok := c.complexitySuccess[comp]
		if !ok {
			r := NewRollingAvg(defaultWindowSize)
			ra = &r
			c.complexitySuccess[comp] = ra
		}
		if event.Succeeded {
			ra.Add(1.0)
		} else {
			ra.Add(0.0)
		}
	}
}

func (c *Collector) OnEpisodeComplete(_ context.Context, event evolution.EpisodeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalEpisodes++

	hasReplan := event.ReplanCount > 0
	if hasReplan {
		c.replanRate.Add(1.0)
	} else {
		c.replanRate.Add(0.0)
	}

	succeeded := 0.0
	if event.Succeeded {
		succeeded = 1.0
	}

	if hasReplan {
		c.replanSuccess.Add(succeeded)
	} else {
		c.noReplanSuccess.Add(succeeded)
	}
}

func (c *Collector) OnToolExecuted(_ context.Context, event evolution.ToolExecEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ra, ok := c.toolReliability[event.ToolName]
	if !ok {
		r := NewRollingAvg(defaultWindowSize)
		ra = &r
		c.toolReliability[event.ToolName] = ra
	}

	if event.Succeeded {
		ra.Add(1.0)
	} else {
		ra.Add(0.0)
	}
}

// RecordAssertionRate records the pass rate for a batch of assertions.
func (c *Collector) RecordAssertionRate(passRate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.assertionPassRate.Add(passRate)
}

// SetStrategyVersion updates the tracked strategy version.
func (c *Collector) SetStrategyVersion(v int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.strategyVersion = v
}
