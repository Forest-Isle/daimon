package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Forest-Isle/daimon/internal/attention"
	"github.com/Forest-Isle/daimon/internal/channel"
	"github.com/Forest-Isle/daimon/internal/config"
	"github.com/Forest-Isle/daimon/internal/economy"
	"github.com/Forest-Isle/daimon/internal/world"
)

const throttleWindow = 30 * 24 * time.Hour

type throttleGate struct {
	mu        sync.RWMutex
	throttled map[string]bool
	overrides map[string]bool
}

func (g *throttleGate) ShouldSkip(class string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.throttled[class] && !g.overrides[class]
}

func (g *throttleGate) set(classes []string) {
	if g == nil {
		return
	}
	next := make(map[string]bool, len(classes))
	for _, class := range classes {
		next[class] = true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.throttled = next
	if g.overrides == nil {
		g.overrides = map[string]bool{}
	}
}

func (g *throttleGate) overrideOff(class string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.overrides == nil {
		g.overrides = map[string]bool{}
	}
	g.overrides[class] = true
}

func (g *throttleGate) overrideOn(class string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.overrides, class)
}

func (g *throttleGate) snapshot() (throttled, overrides []string) {
	if g == nil {
		return nil, nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for class := range g.throttled {
		throttled = append(throttled, class)
	}
	for class := range g.overrides {
		overrides = append(overrides, class)
	}
	sort.Strings(throttled)
	sort.Strings(overrides)
	return throttled, overrides
}

type throttleClassCostRow struct {
	class       string
	totals      economy.Totals
	usd         float64
	anyUnpriced bool
}

func (gw *Gateway) gatherClassValues(ctx context.Context, since int64) ([]economy.ClassValue, error) {
	if gw == nil || gw.db == nil || gw.db.DB == nil {
		return nil, fmt.Errorf("gateway database unavailable")
	}
	st := economy.NewStore(gw.db.DB)
	classRows, err := st.ByClassModelSince(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("aggregate costs by class: %w", err)
	}
	folded := foldThrottleClassCosts(classRows, gatewayPricesFromConfig(gw.Config().Economy))

	episodeCosts, err := st.EpisodeClassCostSince(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("episode costs for ROI: %w", err)
	}
	ids := make([]string, 0, len(episodeCosts))
	for _, e := range episodeCosts {
		if e.EpisodeID != "" {
			ids = append(ids, e.EpisodeID)
		}
	}
	quality, err := world.NewStore(gw.db.DB).OutcomeQualityForEpisodes(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("outcome quality for ROI: %w", err)
	}

	cleanByClass := map[string]int{}
	for _, e := range episodeCosts {
		if q, ok := quality[e.EpisodeID]; ok && q == world.OutcomeClean {
			cleanByClass[e.Class]++
		}
	}
	values := make([]economy.ClassValue, 0, len(folded))
	for _, c := range folded {
		values = append(values, economy.ClassValue{
			Class:    c.class,
			Episodes: c.totals.Episodes,
			Clean:    cleanByClass[c.class],
			USD:      c.usd,
			Priced:   !c.anyUnpriced,
		})
	}
	return values, nil
}

func foldThrottleClassCosts(rows []economy.ClassModelTotals, prices economy.Prices) []throttleClassCostRow {
	byClass := map[string]*throttleClassCostRow{}
	order := []string{}
	for _, r := range rows {
		c, ok := byClass[r.Class]
		if !ok {
			c = &throttleClassCostRow{class: r.Class}
			byClass[r.Class] = c
			order = append(order, r.Class)
		}
		c.totals.Episodes += r.Episodes
		c.totals.InputTokens += r.InputTokens
		c.totals.OutputTokens += r.OutputTokens
		c.totals.CacheReadTokens += r.CacheReadTokens
		c.totals.CacheCreationTokens += r.CacheCreationTokens
		if usd, priced := prices.CostUSD(r.Model, r.Totals); priced {
			c.usd += usd
		} else {
			c.anyUnpriced = true
		}
	}
	out := make([]throttleClassCostRow, 0, len(order))
	for _, class := range order {
		out = append(out, *byClass[class])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].totals.OutputTokens != out[j].totals.OutputTokens {
			return out[i].totals.OutputTokens > out[j].totals.OutputTokens
		}
		return out[i].class < out[j].class
	})
	return out
}

func (gw *Gateway) refreshThrottle(ctx context.Context) (string, error) {
	if gw == nil || gw.throttle == nil {
		return "throttle unavailable", nil
	}
	cfg := gw.Config()
	if cfg == nil || !cfg.Economy.Throttle.Enforce {
		gw.throttle.set(nil)
		return "throttle enforcement disabled", nil
	}

	since := time.Now().Add(-throttleWindow).Unix()
	values, err := gw.gatherClassValues(ctx, since)
	if err != nil {
		return "", err
	}
	advice := throttlePolicyFromGatewayConfig(cfg.Economy).Evaluate(values)
	oldThrottled, _ := gw.throttle.snapshot()
	wasThrottled := make(map[string]bool, len(oldThrottled))
	for _, class := range oldThrottled {
		wasThrottled[class] = true
	}

	var flagged []string
	for _, a := range advice {
		if isThrottleHighRiskKind(a.Class, cfg.Agent.Heart.HighRiskKinds) {
			continue
		}
		flagged = append(flagged, a.Class)
	}
	for _, class := range flagged {
		if !wasThrottled[class] {
			gw.notifyThrottle(ctx, class)
		}
	}
	gw.throttle.set(flagged)
	return "throttle evaluated", nil
}

func (gw *Gateway) notifyThrottle(ctx context.Context, class string) {
	notifier, target := gw.primaryNotifier()
	if notifier == nil {
		slog.Warn("throttle: class auto-throttled but no notification channel", "class", class)
		return
	}
	text := fmt.Sprintf("⚠️ throttle: 类 %s 自动节流(ROI 为负/超预算); 回复 /throttle off %s 可否决", class, class)
	if err := notifier.SendNotification(ctx, target, text); err != nil {
		slog.Warn("throttle: notify failed", "class", class, "err", err)
	}
}

func throttlePolicyFromGatewayConfig(cfg config.EconomyConfig) economy.ThrottlePolicy {
	return economy.ThrottlePolicy{
		PerClassBudgetUSD: cfg.Throttle.PerClassBudgetUSD,
		MinCleanRate:      cfg.Throttle.MinCleanRate,
		MinEpisodes:       cfg.Throttle.MinEpisodes,
	}
}

func gatewayPricesFromConfig(cfg config.EconomyConfig) economy.Prices {
	prices := economy.Prices{}
	for model, mp := range cfg.Prices {
		prices[model] = economy.Price{
			InputPerMTok:         mp.InputPerMTok,
			OutputPerMTok:        mp.OutputPerMTok,
			CacheReadPerMTok:     mp.CacheReadPerMTok,
			CacheCreationPerMTok: mp.CacheCreationPerMTok,
		}
	}
	return prices
}

func isThrottleHighRiskKind(kind string, configured []string) bool {
	for _, p := range append(attention.DefaultHighRiskKinds(), configured...) {
		if p == "" {
			continue
		}
		if kind == p || strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

type throttleEvalJob struct {
	refresh func(context.Context) (string, error)
}

func (j throttleEvalJob) Name() string { return "throttle-eval" }

func (j throttleEvalJob) Run(ctx context.Context) (string, error) {
	if j.refresh == nil {
		return "throttle unavailable", nil
	}
	return j.refresh(ctx)
}

const throttleUsage = `**Throttle Commands**
- /throttle or /throttle list — show throttled classes and user overrides
- /throttle off <class> — override throttling for a class
- /throttle on <class> — remove an override so policy can throttle the class again`

func (gw *Gateway) handleThrottle(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	_ = ctx
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(msg.Text, "/throttle")))
	if len(args) == 0 || args[0] == "list" {
		if gw.throttle == nil {
			return "Throttle is not available.", nil
		}
		throttled, overrides := gw.throttle.snapshot()
		return fmt.Sprintf("Throttle enforcement: %s\nThrottled: %s\nOverrides: %s",
			onOff(gw.Config().Economy.Throttle.Enforce),
			formatClassList(throttled),
			formatClassList(overrides)), nil
	}
	if args[0] == "help" {
		return throttleUsage, nil
	}
	if len(args) != 2 || (args[0] != "off" && args[0] != "on") {
		return "Usage: /throttle [list|off <class>|on <class>]", nil
	}
	if gw.throttle == nil {
		return "Throttle is not available.", nil
	}
	class := strings.TrimSpace(args[1])
	if class == "" {
		return "Usage: /throttle [list|off <class>|on <class>]", nil
	}
	if args[0] == "off" {
		gw.throttle.overrideOff(class)
		return "Throttle override set for " + class + ".", nil
	}
	gw.throttle.overrideOn(class)
	return "Throttle override removed for " + class + ".", nil
}

func formatClassList(classes []string) string {
	if len(classes) == 0 {
		return "(none)"
	}
	return strings.Join(classes, ", ")
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
