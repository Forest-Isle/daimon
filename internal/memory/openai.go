package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	openAIEmbeddingURL     = "https://api.openai.com/v1/embeddings"
	openAIDefaultModel     = "text-embedding-3-small"
	openAIDimensions       = 1536
)

// OpenAIEmbedding implements EmbeddingProvider using the OpenAI Embeddings API.
type OpenAIEmbedding struct {
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAIEmbedding(apiKey, model string) *OpenAIEmbedding {
	if model == "" {
		model = openAIDefaultModel
	}
	return &OpenAIEmbedding{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{},
	}
}

func (o *OpenAIEmbedding) Dimensions() int { return openAIDimensions }

func (o *OpenAIEmbedding) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{
		"input": text,
		"model": o.model,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEmbeddingURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody)
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
