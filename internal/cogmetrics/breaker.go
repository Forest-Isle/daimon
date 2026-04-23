package cogmetrics

// BreakerAction defines what happens when a health rule is violated.
type BreakerAction int

const (
	ActionNone               BreakerAction = iota
	ActionTriggerCompression               // Force context compression
	ActionDegradeToSimple                  // Switch from cognitive to simple mode
	ActionPauseAndAskUser                  // Stop and request user intervention
	ActionSwitchModel                      // Try a different/smaller model
	ActionDegradeToSyncWrite               // Switch async writes to sync
	ActionDisableEvolution                 // Turn off evolution hooks
)

func (a BreakerAction) String() string {
	switch a {
	case ActionTriggerCompression:
		return "trigger_compression"
	case ActionDegradeToSimple:
		return "degrade_to_simple"
	case ActionPauseAndAskUser:
		return "pause_and_ask_user"
	case ActionSwitchModel:
		return "switch_model"
	case ActionDegradeToSyncWrite:
		return "degrade_to_sync_write"
	case ActionDisableEvolution:
		return "disable_evolution"
	default:
		return "none"
	}
}

// HealthRule defines a threshold and action for a specific metric.
type HealthRule struct {
	Metric     string        // metric name to monitor
	Threshold  float64       // threshold value
	Action     BreakerAction // what to do when exceeded
	Severity   float64       // how much to reduce health score (0.0-1.0)
	UseAverage bool          // use average instead of last value
	MinSamples int           // minimum samples before rule is active
}

// Exceeds returns true if the value violates the threshold.
// For most metrics, exceeding means value > threshold.
// For confidence metrics, "exceeds" means value is BELOW threshold.
func (r HealthRule) Exceeds(value float64) bool {
	switch r.Metric {
	case "reflect_confidence":
		return value < r.Threshold // low confidence is bad
	default:
		return value > r.Threshold // high value is bad
	}
}

// DefaultHealthRules returns the standard set of health rules.
func DefaultHealthRules() []HealthRule {
	return []HealthRule{
		{
			Metric:    "context_utilization",
			Threshold: 0.85,
			Action:    ActionTriggerCompression,
			Severity:  0.15,
		},
		{
			Metric:    "consecutive_replans",
			Threshold: 3,
			Action:    ActionDegradeToSimple,
			Severity:  0.25,
		},
		{
			Metric:     "tool_failure_rate",
			Threshold:  0.5,
			Action:     ActionPauseAndAskUser,
			Severity:   0.3,
			UseAverage: true,
			MinSamples: 5,
		},
		{
			Metric:     "reflect_confidence",
			Threshold:  0.3,
			Action:     ActionSwitchModel,
			Severity:   0.2,
			UseAverage: true,
			MinSamples: 3,
		},
		{
			Metric:    "memory_write_latency_ms",
			Threshold: 30000, // 30 seconds
			Action:    ActionDegradeToSyncWrite,
			Severity:  0.1,
		},
		{
			Metric:     "evolution_hook_timeout_rate",
			Threshold:  0.2,
			Action:     ActionDisableEvolution,
			Severity:   0.1,
			UseAverage: true,
			MinSamples: 10,
		},
	}
}

// Breaker is a circuit breaker that monitors health and triggers actions.
type Breaker struct {
	checker   *HealthChecker
	callbacks map[BreakerAction]func()
}

// NewBreaker creates a circuit breaker with the given health checker.
func NewBreaker(checker *HealthChecker) *Breaker {
	return &Breaker{
		checker:   checker,
		callbacks: make(map[BreakerAction]func()),
	}
}

// OnAction registers a callback for a specific breaker action.
func (b *Breaker) OnAction(action BreakerAction, fn func()) {
	b.callbacks[action] = fn
}

// Evaluate checks health and triggers callbacks for any violations.
// Returns the list of actions triggered.
func (b *Breaker) Evaluate() []BreakerAction {
	status := b.checker.Check()
	var triggered []BreakerAction

	seen := make(map[BreakerAction]bool)
	for _, v := range status.Violations {
		action := actionFromString(v.Action)
		if action == ActionNone || seen[action] {
			continue
		}
		seen[action] = true

		if fn, ok := b.callbacks[action]; ok {
			fn()
		}
		triggered = append(triggered, action)
	}
	return triggered
}

// HealthScore returns the current health score (0.0-1.0).
func (b *Breaker) HealthScore() float64 {
	return b.checker.Check().Score
}

func actionFromString(s string) BreakerAction {
	switch s {
	case "trigger_compression":
		return ActionTriggerCompression
	case "degrade_to_simple":
		return ActionDegradeToSimple
	case "pause_and_ask_user":
		return ActionPauseAndAskUser
	case "switch_model":
		return ActionSwitchModel
	case "degrade_to_sync_write":
		return ActionDegradeToSyncWrite
	case "disable_evolution":
		return ActionDisableEvolution
	default:
		return ActionNone
	}
}
