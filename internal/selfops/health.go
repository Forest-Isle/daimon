package selfops

import "fmt"

type Severity int

const (
	SeverityWarn Severity = iota
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityWarn:
		return "warn"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

type Finding struct {
	Severity Severity
	Title    string
	Detail   string
}

type Signals struct {
	OutcomesTotal int
	Salvaged      int
	RoutingMisses int
	HoldsPending  int
	DiskFreePct   float64
}

func (s Signals) SalvagedRate() float64 {
	if s.OutcomesTotal <= 0 {
		return 0
	}
	return float64(s.Salvaged) / float64(s.OutcomesTotal)
}

// Thresholds holds watchdog thresholds. A zero threshold disables that check.
type Thresholds struct {
	MaxSalvagedRate  float64
	MaxRoutingMisses int
	MaxHoldsPending  int
	MinDiskFreePct   float64
}

func Evaluate(sig Signals, th Thresholds) []Finding {
	var out []Finding
	if th.MinDiskFreePct > 0 && sig.DiskFreePct > 0 && sig.DiskFreePct < th.MinDiskFreePct {
		out = append(out, Finding{
			Severity: SeverityCritical,
			Title:    "disk space low",
			Detail:   fmt.Sprintf("disk free %.1f%% is below %.1f%%", sig.DiskFreePct, th.MinDiskFreePct),
		})
	}
	if th.MaxRoutingMisses > 0 && sig.RoutingMisses > th.MaxRoutingMisses {
		out = append(out, Finding{
			Severity: SeverityCritical,
			Title:    "WakeUser routing misses",
			Detail:   fmt.Sprintf("wake_user routing misses %d exceeds %d", sig.RoutingMisses, th.MaxRoutingMisses),
		})
	}
	if th.MaxSalvagedRate > 0 && sig.OutcomesTotal > 0 && sig.SalvagedRate() > th.MaxSalvagedRate {
		out = append(out, Finding{
			Severity: SeverityWarn,
			Title:    "salvage rate elevated",
			Detail:   fmt.Sprintf("salvage rate %.2f exceeds %.2f (%d/%d)", sig.SalvagedRate(), th.MaxSalvagedRate, sig.Salvaged, sig.OutcomesTotal),
		})
	}
	if th.MaxHoldsPending > 0 && sig.HoldsPending > th.MaxHoldsPending {
		out = append(out, Finding{
			Severity: SeverityWarn,
			Title:    "holds backlog",
			Detail:   fmt.Sprintf("pending holds %d exceeds %d", sig.HoldsPending, th.MaxHoldsPending),
		})
	}
	return out
}
