package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIEmbeddingEmbedRetries429(t *testing.T) {
	oldDelay := openAIRetryBaseDelay
	openAIRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { openAIRetryBaseDelay = oldDelay })

	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	embedder := NewOpenAIEmbeddingWithURL("test-key", "test-model", server.URL)
	embedding, err := embedder.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 server hits, got %d", hits.Load())
	}
	if len(embedding) != 2 || embedding[0] != 0.1 || embedding[1] != 0.2 {
		t.Fatalf("unexpected embedding: %#v", embedding)
	}
}

func TestOpenAIEmbeddingEmbedReturnsErrorAfter5xxRetries(t *testing.T) {
	oldDelay := openAIRetryBaseDelay
	openAIRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { openAIRetryBaseDelay = oldDelay })

	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"unavailable"}}`))
	}))
	defer server.Close()

	embedder := NewOpenAIEmbeddingWithURL("test-key", "test-model", server.URL)
	_, err := embedder.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if hits.Load() != 4 {
		t.Fatalf("expected 4 server hits, got %d", hits.Load())
	}
}
