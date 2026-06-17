package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/sleep"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

// errSummarizer fails if called; the empty-world digest path must not call it.
type errSummarizer struct{}

func (errSummarizer) Complete(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("summarizer must not be called for an empty world")
}

func TestHandleSleepRunsCycle(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "sleep_cmd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ws := world.NewStore(db.DB)

	gw := &Gateway{sleep: sleep.NewRunner(sleep.NewDigestJob(ws, errSummarizer{}))}
	resp, err := gw.handleSleep(context.Background(), nil, channel.InboundMessage{Text: "/sleep"})
	if err != nil {
		t.Fatalf("handleSleep: %v", err)
	}
	if !strings.Contains(resp, "Sleep cycle complete") || !strings.Contains(resp, "digest") {
		t.Fatalf("unexpected sleep report:\n%s", resp)
	}
}

func TestHandleSleepNilRunner(t *testing.T) {
	gw := &Gateway{} // no sleep runner
	resp, err := gw.handleSleep(context.Background(), nil, channel.InboundMessage{Text: "/sleep"})
	if err != nil {
		t.Fatalf("handleSleep: %v", err)
	}
	if !strings.Contains(resp, "not available") {
		t.Fatalf("expected unavailable message, got %q", resp)
	}
}

func TestHandleSleepAlreadyRunning(t *testing.T) {
	gw := &Gateway{sleep: sleep.NewRunner()}
	gw.sleepRunning.Store(true)

	resp, err := gw.handleSleep(context.Background(), nil, channel.InboundMessage{Text: "/sleep"})
	if err != nil {
		t.Fatalf("handleSleep: %v", err)
	}
	if resp != "A sleep cycle is already running; try again shortly." {
		t.Fatalf("unexpected response: %q", resp)
	}
}

type signalSleepJob struct {
	started chan struct{}
	release chan struct{}
}

func (j *signalSleepJob) Name() string { return "signal" }

func (j *signalSleepJob) Run(ctx context.Context) (string, error) {
	close(j.started)
	if j.release == nil {
		return "done", nil
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-j.release:
		return "done", nil
	}
}

func newSleepTestGateway(idleMinutes int, j sleep.Job) *Gateway {
	return &Gateway{
		config: &ConfigSubsystem{cfg: &config.Config{
			Agent: config.AgentConfig{
				Heart: config.HeartConfig{SleepIdleMinutes: idleMinutes},
			},
		}},
		sleep: sleep.NewRunner(j),
	}
}

func assertSleepNotStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
		t.Fatal("sleep job started unexpectedly")
	case <-time.After(50 * time.Millisecond):
	}
}

func assertSleepStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sleep job did not start")
	}
}

func TestTriggerAutonomousSleepSkipsWhenAlreadyRunning(t *testing.T) {
	job := &signalSleepJob{started: make(chan struct{})}
	gw := newSleepTestGateway(0, job)
	gw.sleepRunning.Store(true)

	gw.triggerAutonomousSleep(context.Background())

	assertSleepNotStarted(t, job.started)
}

func TestTriggerAutonomousSleepIdleGate(t *testing.T) {
	t.Run("recent activity skips", func(t *testing.T) {
		job := &signalSleepJob{started: make(chan struct{})}
		gw := newSleepTestGateway(10, job)
		gw.lastEventAt.Store(time.Now().Unix())

		gw.triggerAutonomousSleep(context.Background())

		assertSleepNotStarted(t, job.started)
	})

	t.Run("old activity proceeds", func(t *testing.T) {
		job := &signalSleepJob{started: make(chan struct{}), release: make(chan struct{})}
		gw := newSleepTestGateway(10, job)
		gw.lastEventAt.Store(time.Now().Add(-11 * time.Minute).Unix())

		gw.triggerAutonomousSleep(context.Background())
		assertSleepStarted(t, job.started)
		close(job.release)
	})

	t.Run("zero activity proceeds", func(t *testing.T) {
		job := &signalSleepJob{started: make(chan struct{}), release: make(chan struct{})}
		gw := newSleepTestGateway(10, job)

		gw.triggerAutonomousSleep(context.Background())
		assertSleepStarted(t, job.started)
		close(job.release)
	})
}
