// Package vlm 提供对视觉语言模型服务（OpenAI 兼容的 POST /chat/completions）的访问。
//
// 它目前只承担一件事：设置页的连通性测试（Probe）。真正的图片理解调用发生在
// Python 解析子进程里（pkg/documentparser/python/vlm_image.py）—— 两处对协议的
// 约定必须保持一致：base URL 拼 /chat/completions，图片以 data:image/png;base64
// 放进 image_url.url。改动任一处时记得同步另一处。
package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxErrorBodyLength 出错时最多回显多少字节的响应体 —— 与 pkg/rerank 同一考虑：
// 保留上游给的真因（额度、鉴权、模型名），又不让一页 HTML 撑爆日志。
const maxErrorBodyLength = 4 << 10

// defaultTimeout 未指定超时时的默认值（120 秒）。视觉推理比普通文本慢，
// 给得比 rerank 宽；大图在慢速上游上几十秒才回来并不罕见。
const defaultTimeout = 120 * time.Second

// Config 是一次视觉模型调用所需的连接信息。APIKey 传明文，解密由调用方负责。
type Config struct {
	BaseURL string        // 服务根地址（不含 /chat/completions 路径），如 https://api.siliconflow.cn/v1
	APIKey  string        // 可空：自建服务（Ollama / vLLM）常无鉴权
	Model   string        // 视觉模型 ID，如 Qwen/Qwen3.5-35B-A3B
	Timeout time.Duration // 单次请求超时；<=0 时默认 120 秒
}

// Client 是已配置视觉模型服务的 HTTP 客户端。
type Client struct {
	apiKey     string
	model      string
	chatURL    string
	httpClient *http.Client
}

// HTTPError 是视觉服务返回的非 2xx 响应。保留状态码与响应体，供设置页展示真因。
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("视觉模型服务返回 HTTP 状态码 %d", e.StatusCode)
	}
	return fmt.Sprintf("视觉模型服务返回 HTTP 状态码 %d: %s", e.StatusCode, e.Body)
}

// NewClient 根据配置创建客户端。BaseURL 与 Model 必填，Timeout 缺省 120 秒。
func NewClient(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("视觉模型服务地址不能为空")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("视觉模型不能为空")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		apiKey:     cfg.APIKey,
		model:      model,
		chatURL:    baseURL + "/chat/completions",
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

// ProbeResult 是一次连通性探测的结果。
type ProbeResult struct {
	// Reply 是模型的实际回复（已裁剪空白）。用于在设置页给用户一个"端点真的在应答"的凭据。
	Reply string
}

// probeImageEdge 探测图的边长（像素）。
//
// 不能用 1x1：视觉模型普遍对最小边长有要求（Qwen3 VL 系实测要求长宽都大于 28，
// 否则 400），太小的图还会被某些服务的预处理器直接拒绝。64 对所有常见模型都安全，
// 像素量又极小 —— 探测仍是一次"最便宜的真实调用"。
const probeImageEdge = 64

// probePrompt 是探测用的提示：要求短回复，避免模型长篇输出浪费额度。
const probePrompt = "这是一次接口连通性测试。请只回复两个字：OK"

// Probe 发一次最小真实调用：一张 64x64 像素的 PNG + 一句要求回复 OK 的提示。
//
// 探测走真实的图片输入而不是纯文本请求，因为"纯文本能通、图片报错"（模型不支持视觉、
// 图片格式被拒）是这条链路最常见的配置错误 —— 只测文本会让设置页给出一枚假的安全徽章。
func (c *Client) Probe(ctx context.Context) (ProbeResult, error) {
	imageBytes, err := tinyPNG()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("生成探测图片失败: %w", err)
	}

	payload := map[string]any{
		"model": c.model,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": probePrompt},
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes),
						},
					},
				},
			},
		},
		"max_tokens": 32,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("编码探测请求失败: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatURL, bytes.NewReader(encoded))
	if err != nil {
		return ProbeResult{}, fmt.Errorf("构建探测请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("请求视觉模型服务失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyLength))
		return ProbeResult{}, &HTTPError{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&decoded); err != nil {
		return ProbeResult{}, fmt.Errorf("解析视觉模型响应失败: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return ProbeResult{}, fmt.Errorf("视觉模型响应缺少 choices")
	}
	reply := strings.TrimSpace(decodeContent(decoded.Choices[0].Message.Content))
	if reply == "" {
		return ProbeResult{}, fmt.Errorf("视觉模型返回了空内容")
	}
	return ProbeResult{Reply: reply}, nil
}

// decodeContent 兼容 OpenAI 的两种 content 形态：字符串，或分段数组（取 text 段拼接）。
func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// tinyPNG 现编一张 probeImageEdge × probeImageEdge 的 PNG。
//
// 不内嵌 base64 常量：手写的 PNG 字节流一旦出错，表现是上游返回"无法解析图片"，
// 排查会往鉴权/模型方向上跑偏；用标准库现编一次只有几十行、无出错空间。
func tinyPNG() ([]byte, error) {
	imageData := image.NewRGBA(image.Rect(0, 0, probeImageEdge, probeImageEdge))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageData); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
