package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/economy"
	"github.com/Forest-Isle/daimon/internal/store"
	"github.com/Forest-Isle/daimon/internal/world"
)

func TestThrottleGateSetOverrideAndReversibleRefresh(t *testing.T) {
	g := &throttleGate{throttled: map[string]bool{}, overrides: map[string]bool{}}
	if g.ShouldSkip("internal.heartbeat") {
		t.Fatal("empty gate must not skip")
	}
	g.set([]string{"b", "a"})
	if !g.ShouldSkip("a") || !g.ShouldSkip("b") {
		t.Fatal("flagged classes must skip")
	}
	g.overrideOff("a")
	if g.ShouldSkip("a") {
		t.Fatal("override off must suppress skipping")
	}
	g.overrideOn("a")
	if !g.ShouldSkip("a") {
		t.Fatal("override on must restore skipping while class is flagged")
	}
	g.set([]string{"b"})
	if g.ShouldSkip("a") || !g.ShouldSkip("b") {
		t.Fatal("set must rebuild throttled classes so recovered classes leave")
	}
	throttled, overrides := g.snapshot()
	if strings.Join(throttled, ",") != "b" || len(overrides) != 0 {
		t.Fatalf("snapshot = throttled %v overrides %v", throttled, overrides)
	}
}

func TestRefreshThrottleDisabledClearsGateAndJobNoops(t *testing.T) {
	gw := newThrottleTestGateway(t)
	gw.throttle.set([]string{"internal.heartbeat"})
	gw.Config().Economy.Throttle.Enforce = false

	summary, err := (throttleEvalJob{refresh: gw.refreshThrottle}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary != "throttle enforcement disabled" {
		t.Fatalf("summary = %q", summary)
	}
	if gw.throttle.ShouldSkip("internal.heartbeat") {
		t.Fatal("disabled enforcement must clear the gate")
	}
}

func TestRefreshThrottleFlagsNotifiesAndSkipsHighRisk(t *testing.T) {
	ctx := context.Background()
	gw := newThrottleTestGateway(t)
	notify := &throttleNotifyChannel{name: "notify"}
	gw.channels.channels[notify.name] = notify
	cfg := gw.Config()
	cfg.Economy.Throttle.Enforce = true
	cfg.Economy.Throttle.MinCleanRate = 0.75
	cfg.Economy.Throttle.MinEpisodes = 1
	cfg.Economy.Throttle.PerClassBudgetUSD = 0
	cfg.Economy.Prices = map[string]config.ModelPrice{
		"priced": {OutputPerMTok: 1},
	}
	cfg.Agent.Heart.HighRiskKinds = []string{"custom.risk."}

	seedThrottleCost(t, gw, "low1", "internal.low", "priced", 1000)
	seedThrottleCost(t, gw, "low2", "internal.low", "priced", 1000)
	seedThrottleOutcome(t, gw, "low1", "episode stream error: no")
	seedThrottleOutcome(t, gw, "low2", "ok")
	seedThrottleCost(t, gw, "pay1", "payment.charge", "priced", 1000)
	seedThrottleOutcome(t, gw, "pay1", "episode stream error: no")
	seedThrottleCost(t, gw, "risk1", "custom.risk.delete", "priced", 1000)
	seedThrottleOutcome(t, gw, "risk1", "episode stream error: no")

	summary, err := gw.refreshThrottle(ctx)
	if err != nil {
		t.Fatalf("refreshThrottle: %v", err)
	}
	if summary != "throttle evaluated" {
		t.Fatalf("summary = %q", summary)
	}
	if !gw.throttle.ShouldSkip("internal.low") {
		t.Fatal("low-value non-high-risk class must be throttled")
	}
	if gw.throttle.ShouldSkip("payment.charge") || gw.throttle.ShouldSkip("custom.risk.delete") {
		t.Fatal("high-risk classes must be excluded from throttle enforcement")
	}
	if len(notify.messages) != 1 || !strings.Contains(notify.messages[0], "/throttle off internal.low") {
		t.Fatalf("notifications = %v", notify.messages)
	}
	if _, err := gw.refreshThrottle(ctx); err != nil {
		t.Fatalf("second refreshThrottle: %v", err)
	}
	if len(notify.messages) != 1 {
		t.Fatalf("second refresh must not re-notify already-throttled class: %v", notify.messages)
	}
}

func TestHandleThrottleParsesListOffOn(t *testing.T) {
	gw := newThrottleTestGateway(t)
	gw.Config().Economy.Throttle.Enforce = true
	gw.throttle.set([]string{"b", "a"})

	resp, err := gw.handleThrottle(context.Background(), nil, channel.InboundMessage{Text: "/throttle"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(resp, "Throttle enforcement: on") || !strings.Contains(resp, "Throttled: a, b") {
		t.Fatalf("list response = %q", resp)
	}
	resp, err = gw.handleThrottle(context.Background(), nil, channel.InboundMessage{Text: "/throttle off a"})
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if !strings.Contains(resp, "override set") || gw.throttle.ShouldSkip("a") {
		t.Fatalf("off response = %q skip=%v", resp, gw.throttle.ShouldSkip("a"))
	}
	resp, err = gw.handleThrottle(context.Background(), nil, channel.InboundMessage{Text: "/throttle on a"})
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	if !strings.Contains(resp, "override removed") || !gw.throttle.ShouldSkip("a") {
		t.Fatalf("on response = %q skip=%v", resp, gw.throttle.ShouldSkip("a"))
	}
	resp, err = gw.handleThrottle(context.Background(), nil, channel.InboundMessage{Text: "/throttle nope"})
	if err != nil {
		t.Fatalf("bad usage: %v", err)
	}
	if !strings.Contains(resp, "Usage:") {
		t.Fatalf("bad usage response = %q", resp)
	}
}

func newThrottleTestGateway(t *testing.T) *Gateway {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "throttle.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{}
	return &Gateway{
		db:       db,
		config:   InitConfig(cfg, ""),
		channels: &ChannelSubsystem{channels: map[string]channel.Channel{}},
		throttle: &throttleGate{throttled: map[string]bool{}, overrides: map[string]bool{}},
	}
}

func seedThrottleCost(t *testing.T, gw *Gateway, episodeID, class, model string, output int) {
	t.Helper()
	if err := economy.NewStore(gw.db.DB).Record(context.Background(), economy.Entry{
		EpisodeID:     episodeID,
		ActivityClass: class,
		Model:         model,
		OutputTokens:  output,
		OccurredAt:    2_000_000_000,
	}); err != nil {
		t.Fatalf("Record %s: %v", episodeID, err)
	}
}

func seedThrottleOutcome(t *testing.T, gw *Gateway, episodeID, summary string) {
	t.Helper()
	if err := world.NewStore(gw.db.DB).AppendJournal(context.Background(), world.JournalEntry{
		ID:        "journal_outcome_" + episodeID,
		EpisodeID: episodeID,
		Kind:      "outcome",
		Summary:   summary,
	}); err != nil {
		t.Fatalf("AppendJournal %s: %v", episodeID, err)
	}
}

type throttleNotifyChannel struct {
	name     string
	messages []string
}

func (c *throttleNotifyChannel) Name() string { return c.name }
func (c *throttleNotifyChannel) Start(context.Context, channel.InboundHandler) error {
	return nil
}
func (c *throttleNotifyChannel) Send(context.Context, channel.OutboundMessage) error {
	return nil
}
func (c *throttleNotifyChannel) SendStreaming(context.Context, channel.MessageTarget) (channel.StreamUpdater, error) {
	return nil, nil
}
func (c *throttleNotifyChannel) Stop(context.Context) error { return nil }
func (c *throttleNotifyChannel) SendNotification(_ context.Context, _ channel.MessageTarget, text string) error {
	c.messages = append(c.messages, text)
	return nil
}
