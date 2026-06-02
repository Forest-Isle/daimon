package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultOpenAIEmbeddingURL = "https://api.openai.com/v1/embeddings"
	openAIDefaultModel        = "text-embedding-3-small"
	openAIDimensions          = 1536
)

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
	body, _ := json.Marshal(map[string]any{
		"input": text,
		"model": o.model,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
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

	body, _ := json.Marshal(map[string]any{
		"input": texts,
		"model": o.model,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
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
