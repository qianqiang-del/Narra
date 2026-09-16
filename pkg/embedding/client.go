// Package embedding 提供对 OpenAI 兼容向量化服务的访问。
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

// Client 是已配置 OpenAI 兼容向量化服务的 HTTP 客户端。
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

// NewClient 根据 OpenAI 兼容向量化配置创建客户端。
func NewClient(cfg config.EmbeddingConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("向量服务配置无效: %w", err)
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("向量服务未启用")
	}

	return &Client{
		apiKey:        cfg.APIKey,
		dimensions:    cfg.Dimensions,
		embeddingsURL: strings.TrimRight(cfg.BaseURL, "/") + "/embeddings",
		httpClient:    &http.Client{Timeout: cfg.Timeout},
		model:         cfg.Model,
	}, nil
}

// Embed 为每个输入文本返回一个向量。
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("向量化输入不能为空")
	}
	for index, input := range inputs {
		if strings.TrimSpace(input) == "" {
			return nil, fmt.Errorf("第 %d 条向量化输入不能为空", index)
		}
	}

	payload, err := json.Marshal(embeddingsRequest{Input: inputs, Model: c.model})
	if err != nil {
		return nil, fmt.Errorf("编码向量化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embeddingsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建向量化请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求向量服务失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyLength))
		if readErr != nil {
			return nil, fmt.Errorf("向量服务返回 HTTP 状态码 %d", response.StatusCode)
		}
		return nil, fmt.Errorf("向量服务返回 HTTP 状态码 %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var result embeddingsResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析向量服务响应失败: %w", err)
	}
	if len(result.Data) != len(inputs) {
		return nil, fmt.Errorf("向量服务响应包含 %d 个向量，需要 %d 个", len(result.Data), len(inputs))
	}

	vectors := make([][]float32, len(inputs))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(vectors) {
			return nil, fmt.Errorf("向量服务响应包含无效索引 %d", item.Index)
		}
		if vectors[item.Index] != nil {
			return nil, fmt.Errorf("向量服务响应包含重复索引 %d", item.Index)
		}
		if len(item.Embedding) != c.dimensions {
			return nil, fmt.Errorf("索引 %d 的向量为 %d 维，需要 %d 维", item.Index, len(item.Embedding), c.dimensions)
		}
		vectors[item.Index] = item.Embedding
	}

	for index, vector := range vectors {
		if vector == nil {
			return nil, fmt.Errorf("向量服务响应缺少索引 %d", index)
		}
	}

	return vectors, nil
}
