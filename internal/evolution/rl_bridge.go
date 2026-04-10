package evolution

import (
	"log/slog"
	"time"
)

// RLExperience is a self-contained RL experience representation that avoids
// importing the rl package (which would create a config→evolution→rl→config
// import cycle). The gateway layer converts these to rl.Experience.
type RLExperience struct {
	ComplexitySimple   float64
	ComplexityModerate float64
	ComplexityComplex  float64
	ToolCount          float64
	SubTaskCount       float64
	ReplanCount        float64
	SuccessCount       float64
	FailureCount       float64
	Progress           float64
	PlanConfidence     float64
	ReflectionConf     float64
	Reward             float64
}

// ConvertTrajectories converts trajectory records into evolution-layer RL
// experiences. The caller (gateway) maps these to rl.Experience.
func ConvertTrajectories(records []TrajectoryRecord) []RLExperience {
	if len(records) == 0 {
		return nil
	}

	results := make([]RLExperience, 0, len(records))
	for _, rec := range records {
		exp := trajectoryToExperience(rec)
		results = append(results, exp)
	}
	return results
}

func trajectoryToExperience(rec TrajectoryRecord) RLExperience {
	exp := RLExperience{
		Reward: computeTrajectoryReward(rec),
	}

	switch rec.Complexity {
	case "simple":
		exp.ComplexitySimple = 1.0
	case "moderate":
		exp.ComplexityModerate = 1.0
	case "complex":
		exp.ComplexityComplex = 1.0
	}

	exp.ToolCount = normalizeFloat(float64(len(rec.Tools)), 20)
	exp.SubTaskCount = normalizeFloat(float64(len(rec.Tools)), 10)
	exp.ReplanCount = normalizeFloat(float64(rec.ReplanCount), 5)

	successCount := 0
	failCount := 0
	for _, t := range rec.Tools {
		if t.Succeeded {
			successCount++
		} else {
			failCount++
		}
	}
	exp.SuccessCount = normalizeFloat(float64(successCount), 10)
	exp.FailureCount = normalizeFloat(float64(failCount), 10)

	if rec.Reflection.Succeeded {
		exp.Progress = 1.0
	}
	exp.PlanConfidence = rec.Reflection.Confidence
	exp.ReflectionConf = rec.Reflection.Confidence

	return exp
}

func computeTrajectoryReward(rec TrajectoryRecord) float64 {
	reward := 0.0

	if rec.Reflection.Succeeded {
		reward += 0.5
	}

	if rec.DurationMs > 0 && rec.DurationMs < 60000 {
		reward += 0.2
	}
	if rec.ReplanCount == 0 {
		reward += 0.1
	}

	reward += rec.UserFeedback * 0.2

	return clampReward(reward, -1.0, 1.0)
}

func normalizeFloat(v, maxVal float64) float64 {
	if maxVal <= 0 {
		return 0
	}
	r := v / maxVal
	if r > 1.0 {
		return 1.0
	}
	return r
}

func clampReward(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ConvertFromDir reads trajectories and converts them. Convenience wrapper.
func ConvertFromDir(dir string, since time.Time) ([]RLExperience, error) {
	records, err := ReadTrajectories(dir, since, time.Now())
	if err != nil {
		return nil, err
	}
	exps := ConvertTrajectories(records)
	slog.Info("rl_bridge: converted trajectories", "records", len(records), "experiences", len(exps))
	return exps, nil
}
