// Package embedding 提供对 OpenAI 兼容向量化服务的访问。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"narra/pkg/config"
)

// maxErrorBodyLength 出错时最多回显多少字节的响应体。
//
// 上游的错误响应里常常带着真正的原因（额度用完、模型名写错、鉴权失败），
// 丢掉它就只能看到一句"HTTP 400"；但也要防着它返回一整页 HTML 把日志撑爆。
const maxErrorBodyLength = 4 << 10

// Client 是已配置 OpenAI 兼容向量化服务的 HTTP 客户端。
type Client struct {
	apiKey        string
	dimensions    int
	embeddingsURL string
	httpClient    *http.Client
	model         string
}

// embeddingsRequest 是 OpenAI 兼容的 /embeddings 请求体。
// 整批文本放一次请求：逐条请求会把一篇文档的向量化变成几百次网络往返。
type embeddingsRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingsResponse 是 /embeddings 的响应体，只取用得到的 data 字段。
type embeddingsResponse struct {
	Data []embeddingData `json:"data"`
}

// embeddingData 是响应里的一条向量。Index 是上游给的归位依据，
// 但并非所有实现都会正确填写（SiliconFlow 会把每一项都写成 0），
// 所以它只作为参考，是否可用由 usableIndexes 判断。
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
		return nil, fmt.Errorf("向量服务响应包含 %d 个向量，需要 %d 个（索引依次为 %s）",
			len(result.Data), len(inputs), indexReport(result.Data))
	}

	vectors := make([][]float32, len(inputs))

	// 归位方式分两种，选哪种取决于上游给的 index 是否可用：
	// 可用就按 index 归位（抗乱序），不可用就按返回顺序归位。
	// 后者不是退让，而是唯一合理的解释 —— 详见 usableIndexes 的注释。
	if usableIndexes(result.Data, len(inputs)) {
		for _, item := range result.Data {
			vectors[item.Index] = item.Embedding
		}
	} else {
		for position, item := range result.Data {
			vectors[position] = item.Embedding
		}
	}

	for position, vector := range vectors {
		if len(vector) != c.dimensions {
			return nil, fmt.Errorf("第 %d 条向量的维度为 %d，需要 %d 维", position, len(vector), c.dimensions)
		}
	}

	return vectors, nil
}

// usableIndexes 判断响应里的 index 是否构成 0..n-1 的一个排列。
//
// 判成"不可用"不是报错，而是退回按返回顺序归位。这么做是因为存在这样的实现：
// 对数组输入把每一项的 index 都写成 0（SiliconFlow 的 embedding 接口就是如此）。
// 此时 index 不携带任何信息，而 OpenAI 规范本身要求响应按请求顺序返回，
// 按顺序归位是唯一合理的解释。若继续把重复索引当错误，这类服务会完全不可用。
//
// 反过来，只要 index 是完整且不重复的排列就按它归位：那是抗乱序的正确做法，
// 说明上游表达能力更强，没有理由不用。
//
// 注意顺序：这里只判断 index 自身是否自洽，不判断向量维度 ——
// 两种归位方式之后的维度校验是同一段代码，不会因为走了哪条分支而放松。
func usableIndexes(data []embeddingData, want int) bool {
	seen := make([]bool, want)
	for _, item := range data {
		if item.Index < 0 || item.Index >= want || seen[item.Index] {
			return false
		}
		seen[item.Index] = true
	}
	return true
}

// maxIndexReport 报错里最多列出几个索引。
const maxIndexReport = 8

// indexReport 把响应里的索引列成可读的一串。
//
// 存在意义是让"索引不可用"这类报错自己说清上游返回了什么，不用再抓一次包
// 才能判断是上游不报索引、还是我们解析错了字段。
func indexReport(data []embeddingData) string {
	limit := len(data)
	if limit > maxIndexReport {
		limit = maxIndexReport
	}
	parts := make([]string, 0, limit+1)
	for _, item := range data[:limit] {
		parts = append(parts, strconv.Itoa(item.Index))
	}
	if len(data) > limit {
		parts = append(parts, "...")
	}
	return "[" + strings.Join(parts, ",") + "]"
}
