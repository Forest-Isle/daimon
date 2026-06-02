package evolution

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sort"
	"time"
)

// StrategyGene encodes a complete cognitive strategy as a genome.
type StrategyGene struct {
	ID         string `json:"id"`
	Generation int    `json:"generation"`

	// Tunable parameters
	MCTSSearchDepth    int     `json:"mcts_search_depth"`    // [10, 100]
	MCTSExplorationC   float64 `json:"mcts_exploration_c"`   // [0.5, 2.0]
	TreeExpansionWidth int     `json:"tree_expansion_width"` // [3, 10]
	PlannerTemperature float64 `json:"planner_temperature"`  // [0.1, 2.0]
	MaxParallelTools   int     `json:"max_parallel_tools"`   // [1, 10]
	ReplanThreshold    float64 `json:"replan_threshold"`     // [0.3, 0.9]
	ContextBudgetPct   float64 `json:"context_budget_pct"`   // [0.5, 0.95]

	// Fitness
	Fitness *GeneFitness `json:"fitness,omitempty"`
}

// GeneFitness measures how well a strategy gene performs.
type GeneFitness struct {
	SuccessRate      float64   `json:"success_rate"`
	AvgConfidence    float64   `json:"avg_confidence"`
	UserSatisfaction float64   `json:"user_satisfaction"`
	CompositeScore   float64   `json:"composite_score"`
	EpisodesTested   int       `json:"episodes_tested"`
	EvaluatedAt      time.Time `json:"evaluated_at"`
}

// GeneticOptimizer evolves strategy genes using a genetic algorithm.
type GeneticOptimizer struct {
	population     []*StrategyGene
	generation     int
	populationSize int
	eliteCount     int
	mutationRate   float64
	crossoverRate  float64
	bestEver       *StrategyGene
}

// NewGeneticOptimizer creates a new genetic optimizer with default settings.
func NewGeneticOptimizer() *GeneticOptimizer {
	g := &GeneticOptimizer{
		populationSize: 20,
		eliteCount:     3,
		mutationRate:   0.15,
		crossoverRate:  0.7,
	}
	g.initializePopulation()
	return g
}

func (g *GeneticOptimizer) initializePopulation() {
	g.population = make([]*StrategyGene, g.populationSize)
	for i := 0; i < g.populationSize; i++ {
		g.population[i] = g.randomGene(0)
	}
}

func (g *GeneticOptimizer) randomGene(gen int) *StrategyGene {
	return &StrategyGene{
		ID:                 randomID("gene"),
		Generation:         gen,
		MCTSSearchDepth:    randInt(10, 100),
		MCTSExplorationC:   randFloat(0.5, 2.0),
		TreeExpansionWidth: randInt(3, 10),
		PlannerTemperature: randFloat(0.1, 2.0),
		MaxParallelTools:   randInt(1, 10),
		ReplanThreshold:    randFloat(0.3, 0.9),
		ContextBudgetPct:   randFloat(0.5, 0.95),
	}
}

// Evolve runs one generation of evolution.
func (g *GeneticOptimizer) Evolve() {
	g.generation++

	// Sort by fitness (best first)
	sort.Slice(g.population, func(i, j int) bool {
		fi := safeFitness(g.population[i])
		fj := safeFitness(g.population[j])
		return fi.CompositeScore > fj.CompositeScore
	})

	// Track best ever
	if g.bestEver == nil ||
		safeFitness(g.population[0]).CompositeScore > safeFitness(g.bestEver).CompositeScore {
		best := *g.population[0]
		g.bestEver = &best
	}

	// Elitism: keep top N
	newPop := make([]*StrategyGene, 0, g.populationSize)
	for i := 0; i < g.eliteCount; i++ {
		clone := *g.population[i]
		newPop = append(newPop, &clone)
	}

	// Fill rest via crossover + mutation
	for len(newPop) < g.populationSize {
		parent1 := g.tournamentSelect()
		parent2 := g.tournamentSelect()

		var child *StrategyGene
		if rand.Float64() < g.crossoverRate && parent1 != nil && parent2 != nil {
			child = g.crossover(parent1, parent2)
		} else {
			clone := *parent1
			child = &clone
		}

		child = g.mutate(child)
		child.ID = randomID("gene")
		child.Generation = g.generation
		child.Fitness = nil // reset fitness for evaluation
		newPop = append(newPop, child)
	}

	g.population = newPop

	slog.Info("genetic: generation complete",
		"gen", g.generation,
		"best_score", fmtScore(safeFitness(g.population[0]).CompositeScore),
		"pop_size", len(g.population),
	)
}

func fmtScore(s float64) string { return fmt.Sprintf("%.4f", s) }

// tournamentSelect picks the best of k randomly chosen genes.
func (g *GeneticOptimizer) tournamentSelect() *StrategyGene {
	k := 3
	best := g.population[rand.Intn(len(g.population))]
	for i := 1; i < k; i++ {
		contender := g.population[rand.Intn(len(g.population))]
		if safeFitness(contender).CompositeScore > safeFitness(best).CompositeScore {
			best = contender
		}
	}
	return best
}

// crossover performs uniform crossover between two parents.
func (g *GeneticOptimizer) crossover(a, b *StrategyGene) *StrategyGene {
	child := &StrategyGene{}
	// Uniform crossover: each gene randomly from parent A or B
	if rand.Float64() < 0.5 {
		child.MCTSSearchDepth = a.MCTSSearchDepth
	} else {
		child.MCTSSearchDepth = b.MCTSSearchDepth
	}
	if rand.Float64() < 0.5 {
		child.MCTSExplorationC = a.MCTSExplorationC
	} else {
		child.MCTSExplorationC = b.MCTSExplorationC
	}
	if rand.Float64() < 0.5 {
		child.TreeExpansionWidth = a.TreeExpansionWidth
	} else {
		child.TreeExpansionWidth = b.TreeExpansionWidth
	}
	if rand.Float64() < 0.5 {
		child.PlannerTemperature = a.PlannerTemperature
	} else {
		child.PlannerTemperature = b.PlannerTemperature
	}
	if rand.Float64() < 0.5 {
		child.MaxParallelTools = a.MaxParallelTools
	} else {
		child.MaxParallelTools = b.MaxParallelTools
	}
	if rand.Float64() < 0.5 {
		child.ReplanThreshold = a.ReplanThreshold
	} else {
		child.ReplanThreshold = b.ReplanThreshold
	}
	if rand.Float64() < 0.5 {
		child.ContextBudgetPct = a.ContextBudgetPct
	} else {
		child.ContextBudgetPct = b.ContextBudgetPct
	}
	return child
}

// mutate applies random perturbations to a gene.
func (g *GeneticOptimizer) mutate(gene *StrategyGene) *StrategyGene {
	if rand.Float64() < g.mutationRate {
		gene.MCTSSearchDepth = clampInt(gene.MCTSSearchDepth+randInt(-15, 15), 10, 100)
	}
	if rand.Float64() < g.mutationRate {
		gene.MCTSExplorationC = clampFloat(gene.MCTSExplorationC+(rand.Float64()-0.5)*0.4, 0.5, 2.0)
	}
	if rand.Float64() < g.mutationRate {
		gene.TreeExpansionWidth = clampInt(gene.TreeExpansionWidth+randInt(-3, 3), 3, 10)
	}
	if rand.Float64() < g.mutationRate {
		gene.PlannerTemperature = clampFloat(gene.PlannerTemperature+(rand.Float64()-0.5)*0.3, 0.1, 2.0)
	}
	if rand.Float64() < g.mutationRate {
		gene.MaxParallelTools = clampInt(gene.MaxParallelTools+randInt(-2, 2), 1, 10)
	}
	if rand.Float64() < g.mutationRate {
		gene.ReplanThreshold = clampFloat(gene.ReplanThreshold+(rand.Float64()-0.5)*0.15, 0.3, 0.9)
	}
	if rand.Float64() < g.mutationRate {
		gene.ContextBudgetPct = clampFloat(gene.ContextBudgetPct+(rand.Float64()-0.5)*0.1, 0.5, 0.95)
	}
	return gene
}

// Evaluate assigns a fitness score based on real episode results.
func (g *GeneticOptimizer) Evaluate(gene *StrategyGene, episodes []episodeRecord) {
	if len(episodes) == 0 {
		gene.Fitness = &GeneFitness{CompositeScore: 0.5}
		return
	}

	var successes int
	var totalConf float64

	for _, ep := range episodes {
		if ep.Succeeded {
			successes++
		}
		totalConf += ep.TotalReward
	}

	n := float64(len(episodes))
	sr := float64(successes) / n
	avgConf := totalConf / n

	// Composite: success 60% + confidence 30% + consistency 10%
	composite := sr*0.6 + avgConf*0.3 + 0.1

	gene.Fitness = &GeneFitness{
		SuccessRate:      sr,
		AvgConfidence:    avgConf,
		UserSatisfaction: avgConf, // proxy
		CompositeScore:   composite,
		EpisodesTested:   len(episodes),
		EvaluatedAt:      time.Now(),
	}
}

// ApplyToStrategy converts a gene into a live Strategy for the optimizer.
func (g *GeneticOptimizer) ApplyToStrategy(gene *StrategyGene, s *Strategy) {
	s.ReplanThreshold.Value = gene.ReplanThreshold
	s.Version++
	s.UpdatedAt = time.Now()
}

// BestGene returns the best gene found so far.
func (g *GeneticOptimizer) BestGene() *StrategyGene {
	if g.bestEver != nil {
		return g.bestEver
	}
	if len(g.population) == 0 {
		return nil
	}
	return g.population[0]
}

// Population returns the current generation.
func (g *GeneticOptimizer) Population() []*StrategyGene { return g.population }

// Generation returns the current generation number.
func (g *GeneticOptimizer) Generation() int { return g.generation }

// --- helpers ---

func safeFitness(g *StrategyGene) *GeneFitness {
	if g == nil {
		return &GeneFitness{CompositeScore: 0}
	}
	if g.Fitness == nil {
		return &GeneFitness{CompositeScore: 0.5}
	}
	return g.Fitness
}

func randInt(min, max int) int {
	return min + rand.Intn(max-min+1)
}

func randFloat(min, max float64) float64 {
	return min + rand.Float64()*(max-min)
}

func clampInt(v, min, max int) int {
	switch {
	case v < min:
		return min
	case v > max:
		return max
	default:
		return v
	}
}

func clampFloat(v, min, max float64) float64 {
	return math.Max(min, math.Min(max, v))
}

func randomID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()+int64(rand.Intn(10000)))
}
