package knowledge

import (
	"context"
	"math"
	"testing"
)

func TestNoopReranker(t *testing.T) {
	r := &NoopReranker{}
	results := []KnowledgeResult{
		{Chunk: Chunk{ID: "a"}, Score: 0.5},
		{Chunk: Chunk{ID: "b"}, Score: 0.8},
	}
	out, err := r.Rerank(context.Background(), "query", results)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(results) {
		t.Fatalf("expected %d results, got %d", len(results), len(out))
	}
	for i := range results {
		if out[i].Chunk.ID != results[i].Chunk.ID {
			t.Errorf("result %d: expected ID %s, got %s", i, results[i].Chunk.ID, out[i].Chunk.ID)
		}
	}
}

func TestNoopReranker_Empty(t *testing.T) {
	r := &NoopReranker{}
	out, err := r.Rerank(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty results, got %d", len(out))
	}
}

func TestRRFScore(t *testing.T) {
	tests := []struct {
		name      string
		vRank     int
		bRank     int
		vWeight   float64
		bWeight   float64
		expected  float64
		tolerance float64
	}{
		{
			name:  "both present rank 0",
			vRank: 0, bRank: 0,
			vWeight: 0.6, bWeight: 0.4,
			expected: 0.6/61.0 + 0.4/61.0, // 0.6/(60+0+1) + 0.4/(60+0+1)
		},
		{
			name:  "only vector",
			vRank: 0, bRank: -1,
			vWeight: 0.6, bWeight: 0.4,
			expected: 0.6 / 61.0,
		},
		{
			name:  "only bm25",
			vRank: -1, bRank: 0,
			vWeight: 0.6, bWeight: 0.4,
			expected: 0.4 / 61.0,
		},
		{
			name:  "deeper ranks",
			vRank: 9, bRank: 4,
			vWeight: 0.6, bWeight: 0.4,
			expected: 0.6/70.0 + 0.4/65.0,
		},
		{
			name:  "both absent",
			vRank: -1, bRank: -1,
			expected: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kbRRFScore(tt.vRank, tt.bRank, tt.vWeight, tt.bWeight)
			if math.Abs(got-tt.expected) > 1e-10 {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestCosineSim(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{
			name: "identical vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1.0,
		},
		{
			name: "orthogonal vectors",
			a:    []float32{1, 0},
			b:    []float32{0, 1},
			want: 0.0,
		},
		{
			name: "opposite vectors",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1.0,
		},
		{
			name: "mismatched length",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0},
			want: 0.0,
		},
		{
			name: "zero vector",
			a:    []float32{0, 0, 0},
			b:    []float32{1, 0, 0},
			want: 0.0,
		},
		{
			name: "both zero vectors",
			a:    []float32{0, 0},
			b:    []float32{0, 0},
			want: 0.0,
		},
		{
			name: "known cosine",
			a:    []float32{1, 2, 3},
			b:    []float32{4, 5, 6},
			// dot = 4+10+18=32, |a|=sqrt(14)=3.7417, |b|=sqrt(77)=8.7750
			// cos = 32/(3.7417*8.7750) = 32/32.833 = 0.9746
			want: 0.974631846,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSim(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestFloat32BytesRoundtrip(t *testing.T) {
	original := []float32{1.5, -2.0, 3.75, 0, 100.25, -0.5}
	encoded := float32ToBytes(original)
	decoded := bytesToFloat32(encoded)

	if len(original) != len(decoded) {
		t.Fatalf("length mismatch: %d vs %d", len(original), len(decoded))
	}
	for i := range original {
		if original[i] != decoded[i] {
			t.Errorf("index %d: expected %v, got %v", i, original[i], decoded[i])
		}
	}
}

func TestFloat32ToBytes_Empty(t *testing.T) {
	if b := float32ToBytes(nil); b != nil {
		t.Errorf("expected nil for empty input, got %v", b)
	}
}

func TestBytesToFloat32_InvalidLength(t *testing.T) {
	if f := bytesToFloat32([]byte{1, 2, 3}); f != nil {
		t.Errorf("expected nil for invalid length, got %v", f)
	}
}
