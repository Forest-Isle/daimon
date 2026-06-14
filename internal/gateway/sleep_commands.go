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

	ctx, cancel := context.WithTimeout(ctx, sleepCycleTimeout)
	defer cancel()
	report := gw.sleep.Run(ctx)
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
