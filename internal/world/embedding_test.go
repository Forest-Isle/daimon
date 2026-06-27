package world

import "testing"

func TestSerializeEmbeddingRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		vec  []float32
	}{
		{name: "mixed", vec: []float32{-2.5, 0, 3.25}},
		{name: "empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deserializeEmbedding(serializeEmbedding(tc.vec))
			if len(got) != len(tc.vec) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.vec))
			}
			for i := range got {
				if got[i] != tc.vec[i] {
					t.Fatalf("vec[%d] = %f, want %f", i, got[i], tc.vec[i])
				}
			}
		})
	}
}

func TestDeserializeEmbeddingRejectsMisalignedBytes(t *testing.T) {
	if got := deserializeEmbedding([]byte{1, 2, 3}); got != nil {
		t.Fatalf("deserializeEmbedding misaligned = %#v, want nil", got)
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a    []float32
		b    []float32
		want float64
	}{
		{name: "orthogonal", a: []float32{1, 0}, b: []float32{0, 1}, want: 0},
		{name: "same direction", a: []float32{2, 0}, b: []float32{4, 0}, want: 1},
		{name: "zero norm", a: []float32{0, 0}, b: []float32{1, 0}, want: 0},
		{name: "length mismatch", a: []float32{1}, b: []float32{1, 0}, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cosineSimilarity(tc.a, tc.b); got < tc.want-0.000001 || got > tc.want+0.000001 {
				t.Fatalf("cosineSimilarity() = %.9f, want %.9f", got, tc.want)
			}
		})
	}
}
