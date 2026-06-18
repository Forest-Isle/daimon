package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultOpenAIEmbeddingURL = "https://api.openai.com/v1/embeddings"
	openAIDefaultModel        = "text-embedding-3-small"
	openAIDimensions          = 1536
)

var openAIRetryBaseDelay = 500 * time.Millisecond

// OpenAIEmbedding implements EmbeddingProvider using the OpenAI Embeddings API.
type OpenAIEmbedding struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func NewOpenAIEmbedding(apiKey, model string) *OpenAIEmbedding {
	return NewOpenAIEmbeddingWithURL(apiKey, model, "")
}

func NewOpenAIEmbeddingWithURL(apiKey, model, baseURL string) *OpenAIEmbedding {
	if model == "" {
		model = openAIDefaultModel
	}
	if baseURL == "" {
		baseURL = defaultOpenAIEmbeddingURL
	}
	return &OpenAIEmbedding{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *OpenAIEmbedding) Dimensions() int { return openAIDimensions }

func (o *OpenAIEmbedding) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := o.postJSONWithRetry(ctx, map[string]any{
		"input": text,
		"model": o.model,
	})
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("openai API %d: %s", resp.StatusCode, errBody.Error.Message)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai decode: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai: empty response")
	}
	return result.Data[0].Embedding, nil
}

// EmbedBatch generates embeddings for multiple texts in a single API call.
func (o *OpenAIEmbedding) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	resp, err := o.postJSONWithRetry(ctx, map[string]any{
		"input": texts,
		"model": o.model,
	})
	if err != nil {
		return nil, fmt.Errorf("openai batch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("openai API %d: %s", resp.StatusCode, errBody.Error.Message)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai decode: %w", err)
	}

	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("openai: expected %d embeddings, got %d", len(texts), len(result.Data))
	}

	// Sort by index to ensure correct order
	embeddings := make([][]float32, len(texts))
	for _, item := range result.Data {
		if item.Index >= 0 && item.Index < len(embeddings) {
			embeddings[item.Index] = item.Embedding
		}
	}

	return embeddings, nil
}

// postJSONWithRetry POSTs payload to o.baseURL and returns a successful (2xx)
// response, retrying on 429 and 5xx with exponential backoff (honoring any
// Retry-After header and ctx cancellation). The caller owns resp.Body.
func (o *OpenAIEmbedding) postJSONWithRetry(ctx context.Context, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var lastResp *http.Response
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := o.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastResp = nil
			lastErr = err
			if resp != nil && resp.Body != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			if attempt == 3 {
				break
			}
			if err := waitOpenAIRetry(ctx, openAIRetryDelay(attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		if !shouldRetryOpenAI(resp.StatusCode) {
			return resp, nil
		}

		lastResp = resp
		lastErr = nil
		if attempt == 3 {
			break
		}

		delay := openAIRetryDelay(attempt, resp)
		if resp != nil && resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		if err := waitOpenAIRetry(ctx, delay); err != nil {
			return nil, err
		}
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func shouldRetryOpenAI(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitOpenAIRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func openAIRetryDelay(attempt int, resp *http.Response) time.Duration {
	delay := openAIRetryBaseDelay * time.Duration(1<<attempt)
	if resp == nil {
		return delay
	}
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return delay
	}
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil {
		return delay
	}
	headerDelay := time.Duration(seconds) * time.Second
	if headerDelay > delay {
		return headerDelay
	}
	return delay
}
