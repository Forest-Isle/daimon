package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultLocalEngine = "ollama"
	defaultLocalModel  = "nomic-embed-text"
	defaultLocalHost   = "http://localhost:11434"
)

// LocalEmbeddingOptions configures a local OpenAI-compatible embedding engine.
type LocalEmbeddingOptions struct {
	Engine   string
	Model    string
	Host     string
	AutoPull bool
}

// lazyEmbedder is an EmbeddingProvider whose real delegate is wired in
// asynchronously after a local engine (Ollama) is probed and the model pulled.
// Before the delegate is ready, Embed returns a nil vector (no-op semantics) so
// nothing blocks startup; once ready it forwards to the real provider.
type lazyEmbedder struct {
	delegate atomic.Pointer[EmbeddingProvider]
	ready    chan struct{}
	ok       atomic.Bool
}

func (l *lazyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if p := l.delegate.Load(); p != nil {
		return (*p).Embed(ctx, text)
	}
	return nil, nil
}

func (l *lazyEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if p := l.delegate.Load(); p != nil {
		return (*p).EmbedBatch(ctx, texts)
	}
	return make([][]float32, len(texts)), nil
}

func (l *lazyEmbedder) Dimensions() int {
	if p := l.delegate.Load(); p != nil {
		return (*p).Dimensions()
	}
	return 0
}

// WaitReady blocks until warmup finishes or ctx is done, reporting whether the
// local embedder became usable.
func (l *lazyEmbedder) WaitReady(ctx context.Context) bool {
	select {
	case <-l.ready:
		return l.ok.Load()
	case <-ctx.Done():
		return false
	}
}

func (l *lazyEmbedder) set(p EmbeddingProvider) {
	l.delegate.Store(&p)
	l.ok.Store(true)
}

// StartLocalEmbedding returns an EmbeddingProvider and a waitReady func, kicking
// off background warmup of the local engine. It never blocks and never fails
// hard: any error leaves the embedder in no-op state and waitReady reports false.
func StartLocalEmbedding(opt LocalEmbeddingOptions) (EmbeddingProvider, func(context.Context) bool) {
	if opt.Engine == "" {
		opt.Engine = defaultLocalEngine
	}
	if opt.Model == "" {
		opt.Model = defaultLocalModel
	}
	if opt.Host == "" {
		opt.Host = defaultLocalHost
	}
	le := &lazyEmbedder{ready: make(chan struct{})}
	go le.warmup(opt)
	return le, le.WaitReady
}

func (l *lazyEmbedder) warmup(opt LocalEmbeddingOptions) {
	defer close(l.ready)

	if opt.Engine != defaultLocalEngine {
		slog.Warn("local embedding: unsupported engine, staying no-op", "engine", opt.Engine)
		return
	}
	host := strings.TrimRight(opt.Host, "/")

	present, err := ollamaHasModel(host, opt.Model)
	if err != nil {
		slog.Warn("local embedding: daemon not reachable, staying no-op", "host", host, "err", err)
		return
	}
	if !present {
		if !opt.AutoPull {
			slog.Warn("local embedding: model missing and auto_pull off, staying no-op", "model", opt.Model)
			return
		}
		slog.Info("local embedding: pulling model (may take a while)", "model", opt.Model)
		if err := ollamaPull(host, opt.Model); err != nil {
			slog.Warn("local embedding: pull failed, staying no-op", "model", opt.Model, "err", err)
			return
		}
	}

	provider := NewCachedEmbedder(NewOpenAIEmbeddingWithURL("ollama", opt.Model, host+"/v1/embeddings"))
	// Verify the engine actually returns vectors before flipping to ready, so a
	// reachable-but-broken daemon degrades to no-op instead of poisoning the index.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vec, err := provider.Embed(ctx, "ok")
	if err != nil || len(vec) == 0 {
		slog.Warn("local embedding: verification embed failed, staying no-op", "err", err)
		return
	}
	l.set(provider)
	slog.Info("local embedding ready", "engine", opt.Engine, "model", opt.Model, "dim", len(vec))
}

// ollamaHasModel reports whether the daemon at host already has model pulled.
func ollamaHasModel(host, model string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/api/tags", nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama tags: status %d", resp.StatusCode)
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	for _, m := range body.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			return true, nil
		}
	}
	return false, nil
}

// ollamaPull blocks until the daemon finishes pulling model, draining the
// NDJSON progress stream. No client timeout: a cold pull can be hundreds of MB.
func ollamaPull(host, model string) error {
	payload, _ := json.Marshal(map[string]string{"name": model})
	req, err := http.NewRequest(http.MethodPost, host+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama pull: status %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Error != "" {
			return fmt.Errorf("ollama pull: %s", msg.Error)
		}
	}
	return scanner.Err()
}
