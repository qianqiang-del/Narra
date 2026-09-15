// Package embedding provides access to OpenAI-compatible embedding services.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"narra/pkg/config"
)

const maxErrorBodyLength = 4 << 10

// Client is an HTTP client for the configured BGE-M3 embedding service.
type Client struct {
	apiKey        string
	dimensions    int
	embeddingsURL string
	httpClient    *http.Client
	model         string
}

type embeddingsRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type embeddingsResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// NewClient creates a client from the BGE-M3 configuration.
func NewClient(cfg config.EmbeddingConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid embedding configuration: %w", err)
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("embedding service is disabled")
	}

	return &Client{
		apiKey:        cfg.APIKey,
		dimensions:    cfg.Dimensions,
		embeddingsURL: strings.TrimRight(cfg.BaseURL, "/") + "/embeddings",
		httpClient:    &http.Client{Timeout: cfg.Timeout},
		model:         cfg.Model,
	}, nil
}

// Embed returns one dense BGE-M3 vector for every supplied text.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("embedding input cannot be empty")
	}
	for index, input := range inputs {
		if strings.TrimSpace(input) == "" {
			return nil, fmt.Errorf("embedding input at index %d cannot be empty", index)
		}
	}

	payload, err := json.Marshal(embeddingsRequest{Input: inputs, Model: c.model})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embeddingsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request embeddings: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyLength))
		if readErr != nil {
			return nil, fmt.Errorf("embedding service returned HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("embedding service returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var result embeddingsResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(result.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding response contains %d vectors, want %d", len(result.Data), len(inputs))
	}

	vectors := make([][]float32, len(inputs))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(vectors) {
			return nil, fmt.Errorf("embedding response has invalid index %d", item.Index)
		}
		if vectors[item.Index] != nil {
			return nil, fmt.Errorf("embedding response has duplicate index %d", item.Index)
		}
		if len(item.Embedding) != c.dimensions {
			return nil, fmt.Errorf("embedding at index %d has %d dimensions, want %d", item.Index, len(item.Embedding), c.dimensions)
		}
		vectors[item.Index] = item.Embedding
	}

	for index, vector := range vectors {
		if vector == nil {
			return nil, fmt.Errorf("embedding response is missing index %d", index)
		}
	}

	return vectors, nil
}
