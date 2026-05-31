package tui

import (
	"fmt"
	"strings"
)

// renderStatsPanel renders the detailed stats panel.
func (m Model) renderStatsPanel() string {
	var b strings.Builder

	// Model & Session section
	b.WriteString(statsHeaderStyle.Render("Model & Session"))
	b.WriteString("\n")
	streaming := "idle"
	if m.streamingID != "" {
		streaming = "active"
	}
	model := m.metrics.model
	if model == "" {
		model = "—"
	}
	provider := m.metrics.provider
	if provider == "" {
		provider = "—"
	}
	_, _ = fmt.Fprintf(&b, "  %s %s    %s %s\n",
		statsLabelStyle.Render("Model:"), statsValueStyle.Render(model),
		statsLabelStyle.Render("Provider:"), statsValueStyle.Render(provider))
	_, _ = fmt.Fprintf(&b, "  %s %s    %s %s    %s %s    %s %s\n",
		statsLabelStyle.Render("Mode:"), statsValueStyle.Render(m.agentMode),
		statsLabelStyle.Render("Ver:"), statsValueStyle.Render(m.version),
		statsLabelStyle.Render("Msgs:"), statsValueStyle.Render(fmt.Sprintf("%d", len(m.messages))),
		statsLabelStyle.Render("Stream:"), statsValueStyle.Render(streaming))

	// Token Usage section
	b.WriteString("\n")
	b.WriteString(statsHeaderStyle.Render("Token Usage"))
	b.WriteString("\n")
	if m.metrics.inputTokens > 0 || m.metrics.outputTokens > 0 {
		total := m.metrics.inputTokens + m.metrics.outputTokens
		_, _ = fmt.Fprintf(&b, "  %s %s    %s %s    %s %s\n",
			statsLabelStyle.Render("Input:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.inputTokens)),
			statsLabelStyle.Render("Output:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.outputTokens)),
			statsLabelStyle.Render("Total:"),
			statsValueStyle.Render(formatTokenCount(total)))
	} else {
		b.WriteString(statsLabelStyle.Render("  No token data yet"))
		b.WriteString("\n")
	}
	if m.metrics.cacheCreate > 0 || m.metrics.cacheRead > 0 {
		_, _ = fmt.Fprintf(&b, "  %s %s    %s %s\n",
			statsLabelStyle.Render("Cache Write:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.cacheCreate)),
			statsLabelStyle.Render("Cache Read:"),
			statsValueStyle.Render(formatTokenCount(m.metrics.cacheRead)))
	}

	// Tool History section
	b.WriteString("\n")
	b.WriteString(statsHeaderStyle.Render("Tool History"))
	b.WriteString("\n")

	if len(m.toolHistory) == 0 {
		b.WriteString(statsLabelStyle.Render("  No tools executed yet"))
	} else {
		start := 0
		if len(m.toolHistory) > 8 {
			start = len(m.toolHistory) - 8
		}
		for _, entry := range m.toolHistory[start:] {
			icon := statusToolOKStyle.Render("✓")
			if !entry.succeeded {
				icon = statusToolFailStyle.Render("✗")
			}
			name := statsValueStyle.Render(fmt.Sprintf("%-16s", entry.name))
			dur := statsLabelStyle.Render(fmt.Sprintf("%5dms", entry.durationMs))
			_, _ = fmt.Fprintf(&b, "  %s %s %s\n", icon, name, dur)
		}
	}

	// Context section
	b.WriteString("\n")
	b.WriteString(statsHeaderStyle.Render("Context"))
	b.WriteString("\n")

	pct := m.metrics.utilization
	barWidth := 30
	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	barStyle := statsBarFilledStyle
	if pct >= 0.9 {
		barStyle = statsBarCritStyle
	} else if pct >= 0.7 {
		barStyle = statsBarWarnStyle
	}
	bar := barStyle.Render(strings.Repeat("█", filled)) +
		statsBarEmptyStyle.Render(strings.Repeat("░", barWidth-filled))
	_, _ = fmt.Fprintf(&b, "  %s %s %s\n",
		statsLabelStyle.Render("Utilization:"),
		bar,
		statsValueStyle.Render(fmt.Sprintf("%d%%", int(pct*100))))

	if m.metrics.maxIter > 0 {
		_, _ = fmt.Fprintf(&b, "  %s %s    %s %s\n",
			statsLabelStyle.Render("Iteration:"),
			statsValueStyle.Render(fmt.Sprintf("%d/%d", m.metrics.iteration+1, m.metrics.maxIter)),
			statsLabelStyle.Render("Tools:"),
			statsValueStyle.Render(fmt.Sprintf("%d", m.toolCount)))
	}

	if m.compressionCount > 0 {
		compVal := fmt.Sprintf("%d", m.compressionCount)
		if m.lastCompressFrom > 0 {
			compVal += fmt.Sprintf(" (last: %d%%→%d%%)", int(m.lastCompressFrom*100), int(m.lastCompressTo*100))
		}
		_, _ = fmt.Fprintf(&b, "  %s %s\n",
			statsLabelStyle.Render("Compressions:"),
			statsValueStyle.Render(compVal))
	}

	return statsPanelStyle.Width(m.width - 2).Render(b.String())
}
