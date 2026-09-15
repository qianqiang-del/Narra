// Package tts 封装语音合成服务。
package tts

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

const (
	// maxTextLength 是 qwen3-tts-flash 对单次合成文本的长度上限（字符，不是字节）。
	maxTextLength = 600

	// maxErrorBodyLength 出错时最多读多少响应体进 error，避免把整个页面塞进日志。
	maxErrorBodyLength = 4 << 10

	// maxAudioBytes 限制单次下载的音频大小。一段 600 字的 WAV 也就几 MB，
	// 留到 32MB 是为了在拿到的地址根本不是音频时快速失败，而不是把内存吃光。
	maxAudioBytes = 32 << 20

	// qwenLanguageChinese 不用 Auto：文档明确说单一语言文本指定语言能显著提升
	// 合成质量。将来要合成英文课文时再把它提到配置里。
	qwenLanguageChinese = "Chinese"

	// qwenSynthesisPath 挂在 base_url 下面，而 base_url 到 /api/v1 为止——TTS
	// 不在 OpenAI 兼容端点（那是 /compatible-mode/v1，只有 chat/completions 和 embeddings）。
	qwenSynthesisPath = "/services/aigc/multimodal-generation/generation"
)

// Client 语音合成服务客户端。
type Client struct {
	apiKey       string
	languageType string
	synthesisURL string
	model        string
	httpClient   *http.Client
}

type synthesisRequest struct {
	Model string         `json:"model"`
	Input synthesisInput `json:"input"`
}

type synthesisInput struct {
	Text         string `json:"text"`
	Voice        string `json:"voice"`
	LanguageType string `json:"language_type"`
}

type synthesisResponse struct {
	Output synthesisOutput `json:"output"`
	// 下面三个是百炼出错时的字段，与 output 同级
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type synthesisOutput struct {
	Audio synthesisAudio `json:"audio"`
}

type synthesisAudio struct {
	URL string `json:"url"`
}

// NewClient 按 tts.provider 创建客户端。provider 决定走哪套协议，所以这里是厂商
// 分派的扩展点：加第二家就在 switch 里加一个分支和它自己的路径。
func NewClient(cfg config.TTSConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tts configuration: %w", err)
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("tts service is disabled")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("tts.api_key 未设置，请通过环境变量 TTS_API_KEY 注入")
	}

	switch strings.TrimSpace(cfg.Provider) {
	case config.TTSProviderQwen:
		return &Client{
			apiKey:       cfg.APIKey,
			languageType: qwenLanguageChinese,
			synthesisURL: strings.TrimRight(cfg.BaseURL, "/") + qwenSynthesisPath,
			model:        cfg.Model,
			httpClient:   &http.Client{Timeout: cfg.Timeout},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported tts provider %q", cfg.Provider)
	}
}

// Synthesize 把 text 合成语音，返回音频字节。
//
// 返回字节而不是百炼给的那个 URL：那个 URL 24 小时就过期，调用方拿到它唯一合理的
// 动作就是立刻下载，所以它是个没用的中间态——更糟的是留着它会诱导下一手把 URL 存进
// scene_segments.audio_path，一天后全库音频一起失效。
//
// text 超过 maxTextLength 时报错而不是在这里切分：切在哪决定了朗读自不自然（从句子
// 中间劈开会很怪），而"这一段是一句台词还是三句拼的"只有调用方知道。
func (c *Client) Synthesize(ctx context.Context, text, voice string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("tts text cannot be empty")
	}
	if count := len([]rune(text)); count > maxTextLength {
		return nil, fmt.Errorf("tts text is %d characters, exceeds the %d character limit", count, maxTextLength)
	}
	if strings.TrimSpace(voice) == "" {
		return nil, fmt.Errorf("tts voice cannot be empty")
	}

	audioURL, err := c.requestSynthesis(ctx, text, voice)
	if err != nil {
		return nil, err
	}

	return c.downloadAudio(ctx, audioURL)
}

// requestSynthesis 发一次合成请求，返回音频文件的地址。
func (c *Client) requestSynthesis(ctx context.Context, text, voice string) (string, error) {
	payload, err := json.Marshal(synthesisRequest{
		Model: c.model,
		Input: synthesisInput{Text: text, Voice: voice, LanguageType: c.languageType},
	})
	if err != nil {
		return "", fmt.Errorf("encode tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.synthesisURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create tts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	response, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request tts synthesis: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("tts service returned HTTP %d: %s", response.StatusCode, readErrorBody(response.Body))
	}

	var result synthesisResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode tts response: %w", err)
	}

	if result.Output.Audio.URL == "" {
		// 百炼出错时也走 HTTP 200，所以错误码要在这里再看一次
		if result.Code != "" {
			return "", fmt.Errorf("tts service returned %s: %s (request_id %s)", result.Code, result.Message, result.RequestID)
		}
		// 音色 ID 或模型名不对时就是这个现象，单独给一句能定位的话
		return "", fmt.Errorf("tts response has no audio url: voice %q may not be supported by model %q", voice, c.model)
	}

	return result.Output.Audio.URL, nil
}

// downloadAudio 把合成结果拉下来。那个地址是 24 小时后过期的 OSS 链接，必须当场落地。
func (c *Client) downloadAudio(ctx context.Context, audioURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create tts download request: %w", err)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download tts audio: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("tts audio download returned HTTP %d: %s", response.StatusCode, readErrorBody(response.Body))
	}

	// 多读一个字节：正好读满上限时无法区分"刚好这么长"和"被截断了"
	audio, err := io.ReadAll(io.LimitReader(response.Body, maxAudioBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tts audio: %w", err)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("tts audio download returned an empty body")
	}
	if len(audio) > maxAudioBytes {
		return nil, fmt.Errorf("tts audio exceeds %d bytes", maxAudioBytes)
	}

	return audio, nil
}

func readErrorBody(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBodyLength))
	if err != nil {
		return "(unreadable body)"
	}
	return strings.TrimSpace(string(raw))
}
