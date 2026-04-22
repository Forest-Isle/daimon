package eval

// Dimension categorizes evaluation tasks into capability areas.
type Dimension string

const (
	DimTaskExecution Dimension = "task_execution"
	DimPlanning      Dimension = "planning"
	DimErrorRecovery Dimension = "error_recovery"
	DimToolSelection Dimension = "tool_selection"
	DimConversation  Dimension = "conversation"
	DimMemory        Dimension = "memory"
	DimKnowledge     Dimension = "knowledge"
	DimMultiAgent    Dimension = "multi_agent"

	// Self-learning dimensions — assess the agent's ability to improve over time.
	DimSkillLearning       Dimension = "skill_learning"
	DimPreferenceAdherence Dimension = "preference_adherence"
	DimMemoryRetention     Dimension = "memory_retention"
)

// AllDimensions returns the full list of recognized dimensions.
func AllDimensions() []Dimension {
	return []Dimension{
		DimTaskExecution, DimPlanning, DimErrorRecovery, DimToolSelection,
		DimConversation, DimMemory, DimKnowledge, DimMultiAgent,
		DimSkillLearning, DimPreferenceAdherence, DimMemoryRetention,
	}
}

// DefaultDimension returns DimTaskExecution when dim is empty, otherwise dim.
func DefaultDimension(dim Dimension) Dimension {
	if dim == "" {
		return DimTaskExecution
	}
	return dim
}

// VerifyMethod determines how a task's output is verified.
type VerifyMethod string

const (
	VerifyDeterministic VerifyMethod = "deterministic"
	VerifyLLMJudge      VerifyMethod = "llm_judge"
	VerifyHybrid        VerifyMethod = "hybrid"
)

// DimensionScore aggregates metrics for a single evaluation dimension.
type DimensionScore struct {
	Dimension   Dimension         `json:"dimension"`
	TaskCount   int               `json:"task_count"`
	SuccessRate float64           `json:"success_rate"`
	AvgScore    float64           `json:"avg_score"`
	AvgReplan   float64           `json:"avg_replan"`
	TopFailures []FailureCategory `json:"top_failures,omitempty"`
}

// DimensionReport provides a full dimension-level analysis of evaluation results.
type DimensionReport struct {
	Dimensions          []DimensionScore        `json:"dimensions"`
	Weakest             []DimensionScore        `json:"weakest"`
	Strongest           []DimensionScore        `json:"strongest"`
	FailureDistribution map[FailureCategory]int `json:"failure_distribution"`
}

// AggregateDimensions builds a DimensionReport from evaluation results.
func AggregateDimensions(results []EvalResult) *DimensionReport {
	type dimAccum struct {
		totalScore  float64
		totalReplan float64
		successCnt  int
		count       int
		failures    map[FailureCategory]int
	}

	accums := make(map[Dimension]*dimAccum)
	globalFailures := make(map[FailureCategory]int)

	for _, r := range results {
		dim := DefaultDimension(r.Dimension)
		acc, ok := accums[dim]
		if !ok {
			acc = &dimAccum{failures: make(map[FailureCategory]int)}
			accums[dim] = acc
		}
		acc.count++
		acc.totalScore += r.FinalScore
		acc.totalReplan += float64(r.ReplanCount)
		if r.Success {
			acc.successCnt++
		}
		if r.FailureCategory != "" {
			cat := FailureCategory(r.FailureCategory)
			acc.failures[cat]++
			globalFailures[cat]++
		}
	}

	report := &DimensionReport{
		FailureDistribution: globalFailures,
	}

	for dim, acc := range accums {
		ds := DimensionScore{
			Dimension:   dim,
			TaskCount:   acc.count,
			SuccessRate: float64(acc.successCnt) / float64(acc.count),
			AvgScore:    acc.totalScore / float64(acc.count),
			AvgReplan:   acc.totalReplan / float64(acc.count),
		}

		type kv struct {
			cat   FailureCategory
			count int
		}
		var sorted []kv
		for cat, cnt := range acc.failures {
			sorted = append(sorted, kv{cat, cnt})
		}
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].count > sorted[i].count {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		for i, kv := range sorted {
			if i >= 3 {
				break
			}
			ds.TopFailures = append(ds.TopFailures, kv.cat)
		}

		report.Dimensions = append(report.Dimensions, ds)
	}

	dims := report.Dimensions
	for i := 0; i < len(dims); i++ {
		for j := i + 1; j < len(dims); j++ {
			if dims[j].AvgScore < dims[i].AvgScore {
				dims[i], dims[j] = dims[j], dims[i]
			}
		}
	}

	for _, ds := range dims {
		if ds.AvgScore < 0.7 && len(report.Weakest) < 3 {
			report.Weakest = append(report.Weakest, ds)
		}
	}

	for i := len(dims) - 1; i >= 0; i-- {
		if dims[i].AvgScore >= 0.8 && len(report.Strongest) < 3 {
			report.Strongest = append(report.Strongest, dims[i])
		}
	}

	return report
}
