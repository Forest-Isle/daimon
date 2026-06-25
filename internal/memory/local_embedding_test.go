package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockOllama serves the minimal Ollama endpoints warmup touches. hasModel
// controls whether /api/tags reports the embedding model as already present.
func mockOllama(t *testing.T, hasModel bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		var models []map[string]string
		if hasModel {
			models = []map[string]string{{"name": "nomic-embed-text:latest"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	})
	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pulling"}` + "\n" + `{"status":"success"}` + "\n"))
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float32{0.1, 0.2, 0.3}}},
		})
	})
	return httptest.NewServer(mux)
}

func waitCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestLocalEmbeddingReadyWhenModelPresent(t *testing.T) {
	srv := mockOllama(t, true)
	defer srv.Close()

	provider, waitReady := StartLocalEmbedding(LocalEmbeddingOptions{Host: srv.URL, Model: "nomic-embed-text"})
	ctx := waitCtx(t)
	if !waitReady(ctx) {
		t.Fatal("expected local embedder to become ready")
	}
	vec, err := provider.Embed(ctx, "hello")
	if err != nil || len(vec) != 3 {
		t.Fatalf("embed after ready: vec=%v err=%v", vec, err)
	}
}

func TestLocalEmbeddingAutoPull(t *testing.T) {
	srv := mockOllama(t, false) // model absent → must pull
	defer srv.Close()

	_, waitReady := StartLocalEmbedding(LocalEmbeddingOptions{Host: srv.URL, AutoPull: true})
	if !waitReady(waitCtx(t)) {
		t.Fatal("expected ready after auto-pull")
	}
}

func TestLocalEmbeddingNoPullStaysNoop(t *testing.T) {
	srv := mockOllama(t, false)
	defer srv.Close()

	provider, waitReady := StartLocalEmbedding(LocalEmbeddingOptions{Host: srv.URL, AutoPull: false})
	ctx := waitCtx(t)
	if waitReady(ctx) {
		t.Fatal("expected NOT ready: model missing and auto_pull off")
	}
	vec, err := provider.Embed(ctx, "x")
	if err != nil || vec != nil {
		t.Fatalf("expected no-op embed, got vec=%v err=%v", vec, err)
	}
}

func TestLocalEmbeddingUnreachableStaysNoop(t *testing.T) {
	// Port 1 refuses immediately, so warmup fails fast and stays no-op.
	provider, waitReady := StartLocalEmbedding(LocalEmbeddingOptions{Host: "http://127.0.0.1:1"})
	ctx := waitCtx(t)
	if waitReady(ctx) {
		t.Fatal("expected NOT ready when daemon unreachable")
	}
	if _, err := provider.Embed(ctx, "x"); err != nil {
		t.Fatalf("no-op embed must not error: %v", err)
	}
}

func TestLazyEmbedderNoopBeforeReady(t *testing.T) {
	le := &lazyEmbedder{ready: make(chan struct{})}
	ctx := context.Background()

	if v, err := le.Embed(ctx, "x"); err != nil || v != nil {
		t.Fatalf("pre-ready Embed should be no-op: v=%v err=%v", v, err)
	}
	if d := le.Dimensions(); d != 0 {
		t.Fatalf("pre-ready Dimensions = %d, want 0", d)
	}
	batch, err := le.EmbedBatch(ctx, []string{"a", "b"})
	if err != nil || len(batch) != 2 {
		t.Fatalf("pre-ready EmbedBatch: err=%v len=%d", err, len(batch))
	}
}

func TestOllamaHasModelMatch(t *testing.T) {
	srv := mockOllama(t, true) // reports "nomic-embed-text:latest"
	defer srv.Close()

	if ok, err := ollamaHasModel(srv.URL, "nomic-embed-text"); err != nil || !ok {
		t.Fatalf("expected tag-suffix match: ok=%v err=%v", ok, err)
	}
	if ok, _ := ollamaHasModel(srv.URL, "other-model"); ok {
		t.Fatal("unexpected match for absent model")
	}
}
