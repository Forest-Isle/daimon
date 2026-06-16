package selfops

import "testing"

func TestSeverityString(t *testing.T) {
	if SeverityWarn.String() != "warn" {
		t.Fatalf("warn string = %q", SeverityWarn.String())
	}
	if SeverityCritical.String() != "critical" {
		t.Fatalf("critical string = %q", SeverityCritical.String())
	}
	if Severity(99).String() != "unknown" {
		t.Fatalf("unknown string = %q", Severity(99).String())
	}
}

func TestSignalsSalvagedRate(t *testing.T) {
	if got := (Signals{OutcomesTotal: 4, Salvaged: 1}).SalvagedRate(); got != 0.25 {
		t.Fatalf("salvaged rate = %v, want 0.25", got)
	}
	if got := (Signals{OutcomesTotal: 0, Salvaged: 1}).SalvagedRate(); got != 0 {
		t.Fatalf("zero-outcome salvaged rate = %v, want 0", got)
	}
}

func TestEvaluateBoundaries(t *testing.T) {
	th := Thresholds{
		MaxSalvagedRate:     0.5,
		MaxRoutingMisses:    2,
		MaxHoldsPending:     3,
		MinDiskFreePct:      10,
		MaxErrorClusterSize: 2,
	}
	cases := []struct {
		name string
		sig  Signals
		want []string
	}{
		{"empty", Signals{}, nil},
		{"disk_equal", Signals{DiskFreePct: 10}, nil},
		{"disk_below", Signals{DiskFreePct: 9.9}, []string{"disk space low"}},
		{"routing_equal", Signals{DiskFreePct: 100, RoutingMisses: 2}, nil},
		{"routing_over", Signals{DiskFreePct: 100, RoutingMisses: 3}, []string{"WakeUser routing misses"}},
		{"salvage_equal", Signals{DiskFreePct: 100, OutcomesTotal: 2, Salvaged: 1}, nil},
		{"salvage_over", Signals{DiskFreePct: 100, OutcomesTotal: 3, Salvaged: 2}, []string{"salvage rate elevated"}},
		{"salvage_no_outcomes", Signals{DiskFreePct: 100, OutcomesTotal: 0, Salvaged: 10}, nil},
		{"holds_equal", Signals{DiskFreePct: 100, HoldsPending: 3}, nil},
		{"holds_over", Signals{DiskFreePct: 100, HoldsPending: 4}, []string{"holds backlog"}},
		{"error_cluster_equal", Signals{DiskFreePct: 100, ErrorClusters: []ErrorCluster{{Key: "boom", Count: 2}}}, nil},
		{"error_cluster_over", Signals{DiskFreePct: 100, ErrorClusters: []ErrorCluster{{Key: "boom", Count: 3}}}, []string{"error cluster"}},
		{
			"fixed_order",
			Signals{DiskFreePct: 5, RoutingMisses: 3, OutcomesTotal: 3, Salvaged: 2, HoldsPending: 4, ErrorClusters: []ErrorCluster{{Key: "boom", Count: 3}}},
			[]string{"disk space low", "WakeUser routing misses", "salvage rate elevated", "holds backlog", "error cluster"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.sig, th)
			if len(got) != len(tc.want) {
				t.Fatalf("finding count = %d, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, want := range tc.want {
				if got[i].Title != want {
					t.Fatalf("finding[%d] title = %q, want %q", i, got[i].Title, want)
				}
			}
		})
	}
}

func TestEvaluateZeroThresholdsDisableChecks(t *testing.T) {
	sig := Signals{
		DiskFreePct:   0.1,
		RoutingMisses: 100,
		OutcomesTotal: 1,
		Salvaged:      1,
		HoldsPending:  100,
		ErrorClusters: []ErrorCluster{{Key: "boom", Count: 100}},
	}
	if got := Evaluate(sig, Thresholds{}); len(got) != 0 {
		t.Fatalf("zero thresholds must disable all checks, got %+v", got)
	}
}

func TestClusterErrorsCountsAndSorts(t *testing.T) {
	got := ClusterErrors([]string{"b", "a", "b", "c", "a", "b", "c", "a"})
	want := []ErrorCluster{
		{Key: "a", Count: 3},
		{Key: "b", Count: 3},
		{Key: "c", Count: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("cluster count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cluster[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEvaluateErrorClusterDetailIncludesMoreClusters(t *testing.T) {
	got := Evaluate(Signals{
		ErrorClusters: []ErrorCluster{
			{Key: "boom", Count: 3},
			{Key: "zap", Count: 1},
		},
	}, Thresholds{MaxErrorClusterSize: 2})
	if len(got) != 1 {
		t.Fatalf("finding count = %d, want 1: %+v", len(got), got)
	}
	if got[0].Detail != "\"boom\" ×3（+1 more clusters）" {
		t.Fatalf("detail = %q", got[0].Detail)
	}
}
