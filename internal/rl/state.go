package rl

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// StateDim is the dimensionality of the RL state vector.
const StateDim = 21

// RLState represents the state observation for the RL system.
// It encodes the current cognitive context as a fixed-size vector.
type RLState struct {
	// Task features
	ComplexitySimple   float64 // one-hot: 1 if simple
	ComplexityModerate float64 // one-hot: 1 if moderate
	ComplexityComplex  float64 // one-hot: 1 if complex

	// Context features (normalized 0-1)
	MemoryCount    float64 // number of retrieved memories / 10
	KnowledgeCount float64 // number of knowledge snippets / 10
	GraphCount     float64 // number of graph relations / 10
	HistoryLength  float64 // conversation history length / 20
	ToolCount      float64 // number of available tools / 20

	// Plan features
	SubTaskCount   float64 // number of subtasks / 10
	PlanConfidence float64 // plan overall confidence (0-1)

	// Execution features
	SuccessCount  float64 // successful subtasks / 10
	FailureCount  float64 // failed subtasks / 10
	DeniedCount   float64 // denied subtasks / 10
	Progress      float64 // overall progress (0-1)
	ReplanCount   float64 // number of replans / 5

	// Reflection features
	ReflectionConfidence float64 // reflection confidence (0-1)

	// Binary features
	HasSkills      float64 // 1 if skills available
	HasAgents      float64 // 1 if agents available
	HasPersonality float64 // 1 if personality configured

	// Text features
	WordCount       float64 // user message word count / 100
	ErrorPatternCnt float64 // number of error patterns / 5
}

// ToVector converts the state to a float64 slice for neural network input.
func (s *RLState) ToVector() []float64 {
	return []float64{
		s.ComplexitySimple,
		s.ComplexityModerate,
		s.ComplexityComplex,
		s.MemoryCount,
		s.KnowledgeCount,
		s.GraphCount,
		s.HistoryLength,
		s.ToolCount,
		s.SubTaskCount,
		s.PlanConfidence,
		s.SuccessCount,
		s.FailureCount,
		s.DeniedCount,
		s.Progress,
		s.ReplanCount,
		s.ReflectionConfidence,
		s.HasSkills,
		s.HasAgents,
		s.HasPersonality,
		s.WordCount,
		s.ErrorPatternCnt,
	}
}

// FromVector populates the state from a float64 slice.
func (s *RLState) FromVector(v []float64) {
	if len(v) < StateDim {
		return
	}
	s.ComplexitySimple = v[0]
	s.ComplexityModerate = v[1]
	s.ComplexityComplex = v[2]
	s.MemoryCount = v[3]
	s.KnowledgeCount = v[4]
	s.GraphCount = v[5]
	s.HistoryLength = v[6]
	s.ToolCount = v[7]
	s.SubTaskCount = v[8]
	s.PlanConfidence = v[9]
	s.SuccessCount = v[10]
	s.FailureCount = v[11]
	s.DeniedCount = v[12]
	s.Progress = v[13]
	s.ReplanCount = v[14]
	s.ReflectionConfidence = v[15]
	s.HasSkills = v[16]
	s.HasAgents = v[17]
	s.HasPersonality = v[18]
	s.WordCount = v[19]
	s.ErrorPatternCnt = v[20]
}

// ContextHash returns a hash of the context-relevant features for bandit arm lookup.
func (s *RLState) ContextHash() string {
	h := sha256.New()
	// Hash complexity one-hot
	binary.Write(h, binary.LittleEndian, s.ComplexitySimple)
	binary.Write(h, binary.LittleEndian, s.ComplexityModerate)
	binary.Write(h, binary.LittleEndian, s.ComplexityComplex)
	// Discretize continuous features into buckets
	binary.Write(h, binary.LittleEndian, discretize(s.MemoryCount, 3))
	binary.Write(h, binary.LittleEndian, discretize(s.ToolCount, 3))
	binary.Write(h, binary.LittleEndian, discretize(s.WordCount, 3))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// Encode serializes the state vector to bytes.
func (s *RLState) Encode() []byte {
	v := s.ToVector()
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	return buf
}

// DecodeState deserializes bytes back to an RLState.
func DecodeState(data []byte) *RLState {
	if len(data) < StateDim*8 {
		return &RLState{}
	}
	v := make([]float64, StateDim)
	for i := range v {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:]))
	}
	s := &RLState{}
	s.FromVector(v)
	return s
}

// normalize clamps and scales a value to [0, 1].
func normalize(val, maxVal float64) float64 {
	if maxVal <= 0 {
		return 0
	}
	r := val / maxVal
	if r > 1 {
		return 1
	}
	if r < 0 {
		return 0
	}
	return r
}

// discretize maps a [0,1] value into n buckets.
func discretize(val float64, n int) int64 {
	bucket := int64(val * float64(n))
	if bucket >= int64(n) {
		bucket = int64(n) - 1
	}
	if bucket < 0 {
		bucket = 0
	}
	return bucket
}

// BuildStateFromContext creates an RLState from cognitive context parameters.
func BuildStateFromContext(params StateParams) *RLState {
	s := &RLState{}

	// Complexity one-hot
	switch strings.ToLower(params.Complexity) {
	case "simple":
		s.ComplexitySimple = 1
	case "moderate":
		s.ComplexityModerate = 1
	case "complex":
		s.ComplexityComplex = 1
	}

	// Normalized counts
	s.MemoryCount = normalize(float64(params.MemoryCount), 10)
	s.KnowledgeCount = normalize(float64(params.KnowledgeCount), 10)
	s.GraphCount = normalize(float64(params.GraphCount), 10)
	s.HistoryLength = normalize(float64(params.HistoryLength), 20)
	s.ToolCount = normalize(float64(params.ToolCount), 20)
	s.SubTaskCount = normalize(float64(params.SubTaskCount), 10)
	s.PlanConfidence = clamp(params.PlanConfidence, 0, 1)
	s.SuccessCount = normalize(float64(params.SuccessCount), 10)
	s.FailureCount = normalize(float64(params.FailureCount), 10)
	s.DeniedCount = normalize(float64(params.DeniedCount), 10)
	s.Progress = clamp(params.Progress, 0, 1)
	s.ReplanCount = normalize(float64(params.ReplanCount), 5)
	s.ReflectionConfidence = clamp(params.ReflectionConfidence, 0, 1)

	// Binary features
	if params.HasSkills {
		s.HasSkills = 1
	}
	if params.HasAgents {
		s.HasAgents = 1
	}
	if params.HasPersonality {
		s.HasPersonality = 1
	}

	s.WordCount = normalize(float64(params.WordCount), 100)
	s.ErrorPatternCnt = normalize(float64(params.ErrorPatternCount), 5)

	return s
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// StateParams holds raw values used to build an RLState.
type StateParams struct {
	Complexity           string
	MemoryCount          int
	KnowledgeCount       int
	GraphCount           int
	HistoryLength        int
	ToolCount            int
	SubTaskCount         int
	PlanConfidence       float64
	SuccessCount         int
	FailureCount         int
	DeniedCount          int
	Progress             float64
	ReplanCount          int
	ReflectionConfidence float64
	HasSkills            bool
	HasAgents            bool
	HasPersonality       bool
	WordCount            int
	ErrorPatternCount    int
}
