package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	appcrypto "narra/pkg/crypto"
	apperrors "narra/pkg/errors"
)

type llmProviderService struct {
	repo          repository.LLMProviderRepository
	encryptionKey []byte
}

// NewLLMProviderService 构造大模型配置服务，encryptionKey 用于 API Key 的加解密。
func NewLLMProviderService(repo repository.LLMProviderRepository, encryptionKey []byte) LLMProviderService {
	return &llmProviderService{repo: repo, encryptionKey: encryptionKey}
}

// List 返回全部配置，含未测试和已停用的，供设置页展示。
func (s *llmProviderService) List(ctx context.Context) ([]responsedto.LLMProvider, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询大模型配置失败", err)
	}
	out := make([]responsedto.LLMProvider, 0, len(items))
	for _, item := range items {
		dto, err := s.toResponse(item)
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// AvailableModels 只返回已启用且测试通过的配置，供首页模型选择器用。
func (s *llmProviderService) AvailableModels(ctx context.Context) ([]responsedto.AvailableLLMModel, error) {
	items, err := s.repo.ListAvailable(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询可用大模型失败", err)
	}
	// 必须 make 出非 nil 切片：nil 切片会被 encoding/json 编成 null，
	// 前端拿到 null 再 .map 就炸。没有可用模型时也要返回 []。
	out := make([]responsedto.AvailableLLMModel, 0, len(items))
	for _, item := range items {
		models, err := decodeModels(item.Models)
		if err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "大模型配置数据无效", err)
		}
		for _, model := range models {
			out = append(out, responsedto.AvailableLLMModel{ProviderID: item.ID, ProviderName: item.Name, ModelID: model})
		}
	}
	return out, nil
}

// Create 新建配置，一律全部入库：测通了才能手动启用。
func (s *llmProviderService) Create(ctx context.Context, input requestdto.LLMProvider) (*responsedto.LLMProvider, error) {
	name, baseURL, timeout, models, err := validateLLMInput(input)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if err := validateAPIKey(apiKey); err != nil {
		return nil, err
	}
	encrypted, err := s.encrypt(apiKey)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "加密 API Key 失败", err)
	}
	modelJSON, _ := json.Marshal(models)
	item := &entity.LLMProvider{
		Name: name, Protocol: "openai-compatible", BaseURL: baseURL,
		APIKeyEncrypted: encrypted, TimeoutSeconds: int32(timeout / time.Second),
		Models: modelJSON, TestStatus: entity.LLMTestStatusUntested, IsEnabled: false,
	}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeConflict, "配置名称已存在或保存失败", err)
	}
	dto, err := s.toResponse(*item)
	return &dto, err
}

// Update 全量更新。地址、超时、模型、密钥任一变动，都把配置打回未测试并停用。
func (s *llmProviderService) Update(ctx context.Context, id uint64, input requestdto.LLMProvider) (*responsedto.LLMProvider, error) {
	item, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	name, baseURL, timeout, models, err := validateLLMInput(input)
	if err != nil {
		return nil, err
	}
	oldModels, err := decodeModels(item.Models)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "已有模型列表无效", err)
	}
	apiKeyChanged := input.ClearAPIKey || strings.TrimSpace(input.APIKey) != ""
	criticalChanged := item.BaseURL != baseURL || item.TimeoutSeconds != int32(timeout/time.Second) || !slices.Equal(oldModels, models) || apiKeyChanged

	if input.ClearAPIKey {
		item.APIKeyEncrypted = ""
	} else if key := strings.TrimSpace(input.APIKey); key != "" {
		if err := validateAPIKey(key); err != nil {
			return nil, err
		}
		item.APIKeyEncrypted, err = s.encrypt(key)
		if err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "加密 API Key 失败", err)
		}
	}
	modelJSON, _ := json.Marshal(models)
	item.Name, item.BaseURL, item.TimeoutSeconds, item.Models = name, baseURL, int32(timeout/time.Second), modelJSON
	if criticalChanged {
		item.TestStatus = entity.LLMTestStatusUntested
		item.LastTestModel, item.LastTestError, item.LastTestedAt = nil, nil, nil
		item.IsEnabled = false
	}
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeConflict, "更新大模型配置失败", err)
	}
	dto, err := s.toResponse(*item)
	return &dto, err
}

// Delete 删除配置；不检查是否正在被引用，因为目前没有别的表指向它。
func (s *llmProviderService) Delete(ctx context.Context, id uint64) error {
	if _, err := s.find(ctx, id); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "删除大模型配置失败", err)
	}
	return nil
}

// Test 逐个测模型列表里的每个模型，全部通过才算这条配置可用。
// 只测第一个的话，第二个模型名写错要等到真正生成时才暴露，而那时候报错已经离「配置」很远了。
func (s *llmProviderService) Test(ctx context.Context, id uint64) (*responsedto.LLMProviderTestResult, error) {
	item, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	models, err := decodeModels(item.Models)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "模型列表无效", err)
	}
	if len(models) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "至少需要配置一个模型")
	}
	apiKey, err := s.decrypt(item.APIKeyEncrypted)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "解密 API Key 失败", err)
	}
	timeout := time.Duration(item.TimeoutSeconds) * time.Second
	// 遇到第一个失败就停：地址或密钥错了的时候 N 个模型会各等满一个超时，
	// 五个模型就是五分钟。报到哪个模型上就够用户定位了，改完再测。
	var failedModel string
	var testErr error
	for _, model := range models {
		if err := testOpenAICompatible(ctx, item.BaseURL, apiKey, model, timeout); err != nil {
			failedModel, testErr = model, err
			break
		}
	}
	now := time.Now()
	item.LastTestedAt = &now
	if testErr != nil {
		// 只有失败才关：测不通的配置不该继续被首页选中。成功时保持原启用状态不动。
		item.IsEnabled = false
		item.LastTestModel = &failedModel
		message := truncateText(fmt.Sprintf("模型 %s 测试失败: %v", failedModel, testErr), 500)
		item.TestStatus, item.LastTestError = entity.LLMTestStatusFailed, &message
		if err := s.repo.Update(ctx, item); err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存测试结果失败", err)
		}
		return nil, apperrors.New(apperrors.CodeBadRequest, message)
	}
	item.LastTestModel, item.TestStatus, item.LastTestError = nil, entity.LLMTestStatusSuccess, nil
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存测试结果失败", err)
	}
	return &responsedto.LLMProviderTestResult{
		Success: true,
		Message: fmt.Sprintf("%d 个模型全部测试通过", len(models)),
	}, nil
}

// SetEnabled 切换启用状态；没测通过的配置不许启用，否则首页会拿到必然失败的模型。
func (s *llmProviderService) SetEnabled(ctx context.Context, id uint64, enabled bool) (*responsedto.LLMProvider, error) {
	item, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if enabled && item.TestStatus != entity.LLMTestStatusSuccess {
		return nil, apperrors.New(apperrors.CodeBadRequest, "请先测试连接，成功后才能启用")
	}
	item.IsEnabled = enabled
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "更新启用状态失败", err)
	}
	dto, err := s.toResponse(*item)
	return &dto, err
}

// find 按 ID 取配置，取不到就是 404 语义。
func (s *llmProviderService) find(ctx context.Context, id uint64) (*entity.LLMProvider, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "大模型配置不存在", err)
	}
	return item, nil
}

// toResponse 实体转响应结构。密钥只透出「是否已配置」这个布尔值，密文本身不出这层。
func (s *llmProviderService) toResponse(item entity.LLMProvider) (responsedto.LLMProvider, error) {
	models, err := decodeModels(item.Models)
	if err != nil {
		return responsedto.LLMProvider{}, apperrors.NewWithErr(apperrors.CodeInternalError, "模型列表无效", err)
	}
	return responsedto.LLMProvider{
		ID: item.ID, Name: item.Name, Protocol: item.Protocol, BaseURL: item.BaseURL,
		Timeout: (time.Duration(item.TimeoutSeconds) * time.Second).String(), Models: models,
		APIKeyConfigured: item.APIKeyEncrypted != "", TestStatus: item.TestStatus,
		LastTestModel: item.LastTestModel, LastTestError: item.LastTestError,
		LastTestedAt: item.LastTestedAt, Enabled: item.IsEnabled,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}, nil
}

// validateLLMInput 校验并归一化名称、地址、超时和模型列表，返回可以直接落库的值。
func validateLLMInput(input requestdto.LLMProvider) (string, string, time.Duration, []string, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return "", "", 0, nil, apperrors.New(apperrors.CodeBadRequest, "配置名称不能为空且不能超过 120 个字符")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", 0, nil, apperrors.New(apperrors.CodeBadRequest, "Base URL 必须是有效的 HTTP 或 HTTPS 地址")
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(input.Timeout))
	if err != nil || timeout < time.Second || timeout > 600*time.Second {
		return "", "", 0, nil, apperrors.New(apperrors.CodeBadRequest, "请求超时必须在 1 秒到 600 秒之间")
	}
	seen := make(map[string]struct{})
	models := make([]string, 0, len(input.Models))
	for _, raw := range input.Models {
		model := strings.TrimSpace(raw)
		if model == "" || len([]rune(model)) > 160 {
			return "", "", 0, nil, apperrors.New(apperrors.CodeBadRequest, "模型 ID 不能为空且不能超过 160 个字符")
		}
		if _, ok := seen[model]; ok {
			return "", "", 0, nil, apperrors.New(apperrors.CodeBadRequest, "模型 ID 不能重复")
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return "", "", 0, nil, apperrors.New(apperrors.CodeBadRequest, "至少需要配置一个模型")
	}
	return name, baseURL, timeout, models, nil
}

// validateAPIKey 拦住明显不是 API Key 的值。最典型的是把服务地址粘进了 Key 字段——
// 这种值会被加密存好，直到测试连接时才以 401 的形式暴露出来，排查时看不出是输入错了。
// 故意不校验前缀：本地部署的 OpenAI 兼容服务（vLLM、Ollama）常用 EMPTY 这类占位值。
func validateAPIKey(key string) error {
	if key == "" {
		return nil
	}
	if strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
		return apperrors.New(apperrors.CodeBadRequest, "API Key 填成了服务地址，请填写服务商提供的密钥")
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return apperrors.New(apperrors.CodeBadRequest, "API Key 不能包含空格或换行")
	}
	return nil
}

// decodeModels 解析 models 列里存的 JSON 数组。
func decodeModels(raw json.RawMessage) ([]string, error) {
	var models []string
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, err
	}
	return models, nil
}

// encrypt 加密 API Key；空值原样返回，不产生密文，也就不会有假的「已配置」。
func (s *llmProviderService) encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return appcrypto.Encrypt(value, s.encryptionKey)
}

// decrypt 解密 API Key；空值原样返回，对应的就是没配密钥的服务。
func (s *llmProviderService) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return appcrypto.Decrypt(value, s.encryptionKey)
}

// testOpenAICompatible 打一次最小 chat completion 请求，只验证地址、密钥、模型三者可用。
// 故意不带 temperature 和 max_tokens：o1/o3/o4-mini 这类推理模型会对它们直接返回 400
// （temperature 只接受默认值、max_tokens 要换成 max_completion_tokens），
// 于是好好的配置被判成测不通。只发三个必填字段，所有 OpenAI 兼容端点都收。
func testOpenAICompatible(ctx context.Context, baseURL, apiKey, model string, timeout time.Duration) error {
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK only."}},
		"stream": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建测试请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接失败或请求超时: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取服务响应失败")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("服务返回 HTTP %d: %s", resp.StatusCode, safeUpstreamMessage(raw))
	}
	// 只校验响应具备「OpenAI 兼容 chat completion」的形状，不要求 content 非空。
	// 推理模型会先把 token 花在 reasoning_content 上，content 为空但连接完全正常；
	// 这个测试要证明的是地址、密钥、模型三者可用，不是模型愿意说话。
	var result struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("服务响应不是合法 JSON，请确认 Base URL 指向 OpenAI 兼容接口")
	}
	if len(result.Choices) == 0 {
		return fmt.Errorf("服务响应没有 choices 字段，请确认 Base URL 指向 OpenAI 兼容接口")
	}
	return nil
}

// safeUpstreamMessage 只从上游错误体里取 message 字段，不回显整个响应。
func safeUpstreamMessage(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		return truncateText(body.Error.Message, 300)
	}
	return "请检查服务地址、API Key 和模型 ID"
}

// truncateText 按字符而不是字节截断，免得把汉字切坏。
func truncateText(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
