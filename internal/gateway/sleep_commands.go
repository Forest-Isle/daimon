package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Forest-Isle/daimon/internal/channel"
)

// sleepCycleTimeout bounds an on-demand cycle so a stuck LLM call cannot block
// the inbound goroutine indefinitely.
const sleepCycleTimeout = 2 * time.Minute

// proposalDeliveryTimeout bounds proposal delivery after a sleep cycle. Its own
// deadline (not the sleep-cycle ctx) so a long cycle never leaves freshly-queued
// proposals undelivered.
const proposalDeliveryTimeout = 30 * time.Second

const sleepUsage = `**Sleep Commands**
- /sleep — run a consolidation cycle now (regenerate the self-digest, …)

Sleep jobs run offline maintenance over the world model.`

// handleSleep triggers a consolidation cycle on demand and reports each job's
// outcome. The cycle runs synchronously here (the user asked for it); the heart
// can schedule it autonomously in a later increment.
func (gw *Gateway) handleSleep(ctx context.Context, _ channel.Channel, msg channel.InboundMessage) (string, error) {
	args := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/sleep"))
	if args == "help" {
		return sleepUsage, nil
	}
	if gw.sleep == nil {
		return "Sleep is not available (no consolidation jobs registered).", nil
	}

	cycleCtx, cancel := context.WithTimeout(ctx, sleepCycleTimeout)
	defer cancel()
	report := gw.sleep.Run(cycleCtx)

	// A sleep cycle is what fills the proposals queue (the anticipation job runs
	// in it), so push any freshly-queued proposals to the user now (§4.9). Delivery
	// gets its own deadline from the original ctx, not the (possibly exhausted)
	// sleep-cycle ctx, so a long cycle does not starve delivery of its db writes.
	deliverCtx, deliverCancel := context.WithTimeout(ctx, proposalDeliveryTimeout)
	defer deliverCancel()
	gw.deliverProposals(deliverCtx)

	if len(report.Results) == 0 {
		return "Sleep ran no jobs.", nil
	}

	var b strings.Builder
	b.WriteString("**Sleep cycle complete**\n")
	for _, r := range report.Results {
		if r.Err != nil {
			fmt.Fprintf(&b, "- %s: failed — %s\n", r.Name, r.Err)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", r.Name, r.Summary)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
