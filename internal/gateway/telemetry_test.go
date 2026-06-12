package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
	"github.com/stretchr/testify/require"
)

func TestGatewayTelemetryWritesLocalTrace(t *testing.T) {
	cfg := testConfig(t)
	tracePath := filepath.Join(t.TempDir(), "events.jsonl")
	cfg.Telemetry.Enabled = true
	cfg.Telemetry.TracePath = tracePath

	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.telemetry.Stop(nil) }()
	defer func() { _ = gw.db.Close() }()
	require.NotNil(t, gw.telemetry)
	require.NotNil(t, gw.telemetry.Exporter)

	gw.agent.EventBus().Publish(agent.SessionStarted{SessionID: "sess_trace", Channel: "tui"})
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(tracePath)
		return err == nil && strings.Contains(string(data), "session.started") && strings.Contains(string(data), "sess_trace")
	}, 2*time.Second, 20*time.Millisecond)
}

func TestGatewayPublishesTaskTransitionEvent(t *testing.T) {
	cfg := testConfig(t)
	gw, err := New(cfg)
	require.NoError(t, err)
	defer func() { _ = gw.db.Close() }()

	events := make(chan agent.Event, 4)
	gw.agent.EventBus().Subscribe(func(event agent.Event) {
		events <- event
	})
	gw.publishTaskTransition("task_1", "scheduled", "running", "succeeded", "done")

	require.Eventually(t, func() bool {
		select {
		case event := <-events:
			transition, ok := event.(agent.TaskTransitioned)
			return ok && transition.TaskID == "task_1" && transition.ToState == "succeeded"
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)
}
