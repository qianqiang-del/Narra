// Package rerank 提供对重排序服务（兼容 Cohere / Jina / 硅基流动的 POST /rerank）的访问。
//
// 精排是检索的第二阶段：两路召回与 RRF 融合之后，把候选正文和检索词一起交给
// 交叉编码器重新打分。Client 是配置快照：base URL / key / model 在 NewClient 时固化，
// 需在使用点现建现用（与 pkg/llm 同一个装法）。失败由调用方判断——检索链路里
// 精排失败只降级到融合顺序，不能让整次检索失败。
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxErrorBodyLength 出错时最多回显多少字节的响应体。
//
// 上游的错误响应常常带着真正的原因（额度用完、模型名写错、鉴权失败），丢掉它就只能
// 看到一句"HTTP 400"；但也要防着它返回一整页 HTML 把日志撑爆。
const maxErrorBodyLength = 4 << 10

// Config 是一次重排请求所需的连接信息。APIKey 传明文，解密由调用方负责。
type Config struct {
	BaseURL string        // 服务根地址（不含 /rerank 路径），如 https://api.siliconflow.cn/v1
	APIKey  string        // 可空：自建服务（TEI / Xinference）常无鉴权
	Model   string        // 重排模型 ID，如 BAAI/bge-reranker-v2-m3
	Timeout time.Duration // 单次请求超时；<=0 时默认 5 秒
}

// Client 是已配置重排服务的 HTTP 客户端。
type Client struct {
	apiKey     string
	model      string
	rerankURL  string
	httpClient *http.Client
}

// NewClient 根据配置创建客户端。BaseURL 与 Model 必填，Timeout 缺省 5 秒。
func NewClient(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("重排服务地址不能为空")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("重排模型不能为空")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		apiKey:     cfg.APIKey,
		model:      model,
		rerankURL:  baseURL + "/rerank",
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

// HTTPError 是重排服务返回的非 2xx 响应。
//
// 做成有类型的错误、而不是一句格式化字符串，是因为调用方要按状态码决定"值不值得重试"：
// 429 与 5xx 是上游限流或抽风，隔一会儿再来可能就好；其余 4xx 是请求本身不对。
// 第一版检索链路不重试（失败直接降级），类型先留着给设置页的测试与将来的重试策略。
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("重排服务返回 HTTP 状态码 %d", e.StatusCode)
	}
	return fmt.Sprintf("重排服务返回 HTTP 状态码 %d: %s", e.StatusCode, e.Body)
}

// Retryable 表示这个错误是不是"过一会儿再来可能就好了"。
func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

// rerankRequest 是重排请求体，字段名与 Cohere / Jina / 硅基流动的 /rerank 契约定。
//
// TopN 显式等于候选条数：不传的话有的服务只返回默认条数，候选缺一条就得走降级，
// 而我们本来就需要全部候选的分数来重排。
type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

// rerankResponse 只取用得到的 results；return_documents 之类的回显一律不管。
type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

// rerankResult 是响应里的一条：Index 指明它对应请求里的第几条候选。
type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// Rerank 为每条文档返回相关度分（与 documents 等长、同序，分越大越相关）。
//
// 响应里的 results 按相关度排序，但 index 才是归位依据：拿返回顺序当输入顺序，
// 一旦上游排序或截断就张冠李戴。因此这里要求结果条数相符、index 构成完整排列，
// 不合法一律报错，由调用方降级 —— 重排错位比不重排更糟。
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("重排检索词不能为空")
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("重排文档不能为空")
	}
	for index, document := range documents {
		if strings.TrimSpace(document) == "" {
			return nil, fmt.Errorf("第 %d 条重排文档不能为空", index)
		}
	}

	payload, err := json.Marshal(rerankRequest{
		Model:     c.model,
		Query:     query,
		Documents: documents,
		TopN:      len(documents),
	})
	if err != nil {
		return nil, fmt.Errorf("编码重排请求失败: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rerankURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建重排请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求重排服务失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyLength))
		if readErr != nil {
			// 连错误体都读不出来时只保留状态码：它是判断"值不值得重试"的唯一依据。
			return nil, &HTTPError{StatusCode: response.StatusCode}
		}
		return nil, &HTTPError{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	var result rerankResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析重排服务响应失败: %w", err)
	}
	if len(result.Results) != len(documents) {
		return nil, fmt.Errorf("重排服务返回 %d 条结果，需要 %d 条（请确认模型与 top_n 是否被上游截断）",
			len(result.Results), len(documents))
	}

	scores := make([]float64, len(documents))
	seen := make([]bool, len(documents))
	for _, item := range result.Results {
		if item.Index < 0 || item.Index >= len(documents) || seen[item.Index] {
			return nil, fmt.Errorf("重排服务返回的索引不合法：%d（候选 %d 条）", item.Index, len(documents))
		}
		seen[item.Index] = true
		scores[item.Index] = item.RelevanceScore
	}
	return scores, nil
}

// ProbeResult 是一次连通性探测的结果：一条相关内容与一条无关内容的相关度分。
type ProbeResult struct {
	Relevant   float64 // 相关内容的分
	Irrelevant float64 // 无关内容的分
}

// 探测用的固定文本。用一对"明显相关 / 明显无关"的文档，让设置页的测试按钮能回答
// "这个模型真的会区分相关性吗"，而不只是"HTTP 通了没有"。
const (
	probeQuery              = "知识库检索很慢，怎么排查性能问题？"
	probeRelevantDocument   = "向量索引与词法索引的检索性能优化：为高维向量建 HNSW 索引，为中文词法匹配建 GIN 索引。"
	probeIrrelevantDocument = "本周食堂菜单：周一红烧肉，周二清蒸鱼，周三番茄炒蛋，周四糖醋排骨。"
)

// Probe 打一次最小重排请求，验证地址、密钥、模型与分值形态。
//
// 它与 llm.Ping 同一种用途：设置页的"测试"按钮在启用前先问一句"这条路真的通吗"。
// 返回值不校验大小关系（有的模型分值形态特殊，硬校验会误报失败），把两个分数原样
// 交给调用方展示 —— 用户看到 0.97 / 0.02 就知道它在正常工作。
func (c *Client) Probe(ctx context.Context) (ProbeResult, error) {
	scores, err := c.Rerank(ctx, probeQuery, []string{probeRelevantDocument, probeIrrelevantDocument})
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Relevant: scores[0], Irrelevant: scores[1]}, nil
}
