package agent

import "testing"

func TestAgentSpec_Validate_FailureStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy FailureStrategy
		wantErr  bool
	}{
		{"empty defaults to best_effort", "", false},
		{"best_effort valid", StrategyBestEffort, false},
		{"fail_fast valid", StrategyFailFast, false},
		{"invalid strategy", FailureStrategy("invalid"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &AgentSpec{
				Name:            "test",
				Description:     "test agent",
				FailureStrategy: tt.strategy,
			}
			err := spec.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.strategy == "" {
				if spec.FailureStrategy != StrategyBestEffort {
					t.Errorf("expected default FailureStrategy = best_effort, got %q", spec.FailureStrategy)
				}
			}
		})
	}
}
