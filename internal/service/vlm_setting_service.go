package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	appcrypto "narra/pkg/crypto"
	"narra/pkg/documentparser"
	apperrors "narra/pkg/errors"
	"narra/pkg/vlm"
)

// vlmProber 是一次连通性探测的最小依赖面；*vlm.Client 满足它。
//
// 定义成小接口是为了测试能注入替身（与 rerank 的 rerankProber 同一个路数）：
// 服务层要测的是"测试结果怎么流转"，不是 HTTP 协议本身。
type vlmProber interface {
	Probe(ctx context.Context) (vlm.ProbeResult, error)
}

type vlmSettingService struct {
	repo          repository.VLMSettingRepository
	encryptionKey []byte
	newProber     func(cfg vlm.Config) (vlmProber, error)
}

// NewVLMSettingService 构造视觉模型配置服务，encryptionKey 用于 API Key 的加解密。
//
// 与重排配置不同，这里没有 runtime 参数：解析侧（rag.Ingester）每次解析时现读
// "启用中的配置"，所以保存与启停不需要向任何常驻组件推送。
func NewVLMSettingService(repo repository.VLMSettingRepository, encryptionKey []byte) VLMSettingService {
	return &vlmSettingService{
		repo:          repo,
		encryptionKey: encryptionKey,
		newProber: func(cfg vlm.Config) (vlmProber, error) {
			return vlm.NewClient(cfg)
		},
	}
}

// CurrentVision 返回当前启用的视觉模型配置（含解密后的 API Key），供收录链路在
// 解析时下发给 Python 脚本。没有启用项时 ok = false —— 那是"视觉理解关闭"，不是错误；
// 查库或解密失败才返回 error（调用方应降级为纯 OCR 并留日志，而不是让文档失败）。
func (s *vlmSettingService) CurrentVision(ctx context.Context, ownerIDs ...uint64) (documentparser.VLMConfig, bool, error) {
	var item *entity.VLMSetting
	var err error
	if len(ownerIDs) > 0 {
		item, err = s.repo.GetEnabledByOwner(ctx, ownerIDs[0])
	} else {
		item, err = s.repo.GetEnabled(ctx)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return documentparser.VLMConfig{}, false, nil
	}
	if err != nil {
		return documentparser.VLMConfig{}, false, err
	}
	apiKey, err := s.decrypt(item.APIKeyEncrypted)
	if err != nil {
		return documentparser.VLMConfig{}, false, err
	}
	return documentparser.VLMConfig{
		APIBaseURL: item.BaseURL,
		APIKey:     apiKey,
		Model:      item.Model,
		Timeout:    time.Duration(item.TimeoutSeconds) * time.Second,
	}, true, nil
}

// List 返回全部配置，含未测试和已停用的，供设置页展示。
func (s *vlmSettingService) List(ctx context.Context, ownerIDs ...uint64) ([]responsedto.VLMSetting, error) {
	var items []entity.VLMSetting
	var err error
	if len(ownerIDs) > 0 {
		items, err = s.repo.ListByOwner(ctx, ownerIDs[0])
	} else {
		items, err = s.repo.List(ctx)
	}
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询视觉模型配置失败", err)
	}
	out := make([]responsedto.VLMSetting, 0, len(items))
	for _, item := range items {
		out = append(out, toVLMResponse(item))
	}
	return out, nil
}

// Create 新建配置：测通了才能手动启用，所以入库时固定是未测试 + 未启用。
func (s *vlmSettingService) Create(ctx context.Context, input requestdto.VLMSetting, ownerIDs ...uint64) (*responsedto.VLMSetting, error) {
	name, baseURL, timeout, model, err := validateVLMInput(input)
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
	item := &entity.VLMSetting{
		Name: name, BaseURL: baseURL, Model: model,
		APIKeyEncrypted: encrypted, TimeoutSeconds: int32(timeout / time.Second),
		TestStatus: entity.VLMTestStatusUntested, IsEnabled: false,
	}
	if len(ownerIDs) > 0 {
		item.OwnerID = ownerIDs[0]
	}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeConflict, "配置名称已存在或保存失败", err)
	}
	dto := toVLMResponse(*item)
	return &dto, nil
}

// Update 全量更新。地址、超时、模型、密钥任一变动，都把配置打回未测试并停用：
// 旧配置测通过不代表新配置能用，继续挂着"测试成功"的徽章是骗人的。
func (s *vlmSettingService) Update(ctx context.Context, id uint64, input requestdto.VLMSetting, ownerIDs ...uint64) (*responsedto.VLMSetting, error) {
	item, err := s.find(ctx, id, ownerIDs...)
	if err != nil {
		return nil, err
	}
	name, baseURL, timeout, model, err := validateVLMInput(input)
	if err != nil {
		return nil, err
	}
	apiKeyChanged := input.ClearAPIKey || strings.TrimSpace(input.APIKey) != ""
	criticalChanged := item.BaseURL != baseURL || item.TimeoutSeconds != int32(timeout/time.Second) ||
		item.Model != model || apiKeyChanged

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
	item.Name, item.BaseURL, item.TimeoutSeconds, item.Model = name, baseURL, int32(timeout/time.Second), model
	if criticalChanged {
		item.TestStatus = entity.VLMTestStatusUntested
		item.LastTestError = nil
		item.IsEnabled = false
	}
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeConflict, "更新视觉模型配置失败", err)
	}
	// 无需重载运行时：解析侧每次解析读的都是库里的"启用中的配置"，
	// 关键字段变动已即时反映（打回停用）。
	dto := toVLMResponse(*item)
	return &dto, nil
}

// Delete 删除配置；不检查是否正在被引用 —— 解析侧读取的是"当前启用"这一行，
// 删掉启用中的配置等于视觉理解关闭（不会自动切换到其它配置）。
func (s *vlmSettingService) Delete(ctx context.Context, id uint64, ownerIDs ...uint64) error {
	if _, err := s.find(ctx, id, ownerIDs...); err != nil {
		return err
	}
	var err error
	if len(ownerIDs) > 0 {
		err = s.repo.DeleteByOwner(ctx, id, ownerIDs[0])
	} else {
		err = s.repo.Delete(ctx, id)
	}
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "删除视觉模型配置失败", err)
	}
	return nil
}

// Test 发一次真实视觉探测。失败要写回状态并停用该配置——测不通的配置不该继续
// 被解析使用；成功时保持原启用状态不动（测试不是启用动作）。
func (s *vlmSettingService) Test(ctx context.Context, id uint64, ownerIDs ...uint64) (*responsedto.VLMSettingTestResult, error) {
	item, err := s.find(ctx, id, ownerIDs...)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.decrypt(item.APIKeyEncrypted)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "解密 API Key 失败", err)
	}
	prober, err := s.newProber(vlm.Config{
		BaseURL: item.BaseURL,
		APIKey:  apiKey,
		Model:   item.Model,
		Timeout: time.Duration(item.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "构建视觉模型客户端失败", err)
	}

	result, probeErr := prober.Probe(ctx)
	if probeErr != nil {
		item.IsEnabled = false
		message := truncateText(fmt.Sprintf("测试失败: %v", probeErr), 500)
		item.TestStatus, item.LastTestError = entity.VLMTestStatusFailed, &message
		if err := s.repo.Update(ctx, item); err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存测试结果失败", err)
		}
		return nil, apperrors.New(apperrors.CodeBadRequest, message)
	}

	item.TestStatus, item.LastTestError = entity.VLMTestStatusSuccess, nil
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存测试结果失败", err)
	}
	return &responsedto.VLMSettingTestResult{
		Success: true,
		Message: fmt.Sprintf("测试通过：模型回复 %q", truncateText(result.Reply, 120)),
	}, nil
}

// SetEnabled 切换启用状态；未测试通过的配置不许启用，否则每份含图文档都会白等一次超时。
func (s *vlmSettingService) SetEnabled(ctx context.Context, id uint64, enabled bool, ownerIDs ...uint64) (*responsedto.VLMSetting, error) {
	item, err := s.find(ctx, id, ownerIDs...)
	if err != nil {
		return nil, err
	}
	if enabled && item.TestStatus != entity.VLMTestStatusSuccess {
		return nil, apperrors.New(apperrors.CodeBadRequest, "请先测试连接，成功后才能启用")
	}
	var applied bool
	if len(ownerIDs) > 0 {
		applied, err = s.repo.SetEnabledForOwner(ctx, id, ownerIDs[0], enabled)
	} else {
		applied, err = s.repo.SetEnabled(ctx, id, enabled)
	}
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "更新启用状态失败", err)
	}
	if !applied {
		return nil, apperrors.New(apperrors.CodeNotFound, "视觉模型配置不存在")
	}
	item.IsEnabled = enabled
	dto := toVLMResponse(*item)
	return &dto, nil
}

// TestConnection 用未保存的表单值发一次探测，不写任何状态 —— 与 Test 的分工：
// Test 是"已保存配置的体检"（失败会停用并写回），这里是"保存前的试跑"。
func (s *vlmSettingService) TestConnection(ctx context.Context, input requestdto.VLMSettingProbe, ownerIDs ...uint64) (*responsedto.VLMSettingTestResult, error) {
	baseURL, timeout, model, err := validateVLMTarget(input.BaseURL, input.Timeout, input.Model)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if apiKey == "" && !input.ClearAPIKey && input.ID != 0 {
		// 编辑既有配置且没重新输入密钥：沿用库里那把，避免"只想测一下还得重填 Key"。
		var item *entity.VLMSetting
		var err error
		if len(ownerIDs) > 0 {
			item, err = s.repo.FindByIDAndOwner(ctx, input.ID, ownerIDs[0])
		} else {
			item, err = s.repo.FindByID(ctx, input.ID)
		}
		if err == nil {
			apiKey, err = s.decrypt(item.APIKeyEncrypted)
			if err != nil {
				return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "解密 API Key 失败", err)
			}
		}
	}
	if apiKey != "" {
		if err := validateAPIKey(apiKey); err != nil {
			return nil, err
		}
	}
	prober, err := s.newProber(vlm.Config{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: timeout,
	})
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "构建视觉模型客户端失败", err)
	}
	result, probeErr := prober.Probe(ctx)
	if probeErr != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, truncateText(fmt.Sprintf("测试失败: %v", probeErr), 500))
	}
	return &responsedto.VLMSettingTestResult{
		Success: true,
		Message: fmt.Sprintf("测试通过：模型回复 %q", truncateText(result.Reply, 120)),
	}, nil
}

// find 按 ID 取配置，取不到就是 404 语义。
func (s *vlmSettingService) find(ctx context.Context, id uint64, ownerIDs ...uint64) (*entity.VLMSetting, error) {
	var item *entity.VLMSetting
	var err error
	if len(ownerIDs) > 0 {
		item, err = s.repo.FindByIDAndOwner(ctx, id, ownerIDs[0])
	} else {
		item, err = s.repo.FindByID(ctx, id)
	}
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "视觉模型配置不存在", err)
	}
	return item, nil
}

// encrypt / decrypt 与 rerank_setting_service.go 实现相同：空值原样返回，
// 不产生密文，也就不会有假的「已配置」。
func (s *vlmSettingService) encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return appcrypto.Encrypt(value, s.encryptionKey)
}

func (s *vlmSettingService) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return appcrypto.Decrypt(value, s.encryptionKey)
}

// toVLMResponse 实体转响应结构。密钥只透出「是否已配置」这个布尔值。
func toVLMResponse(item entity.VLMSetting) responsedto.VLMSetting {
	return responsedto.VLMSetting{
		ID: item.ID, Name: item.Name, BaseURL: item.BaseURL,
		Timeout: (time.Duration(item.TimeoutSeconds) * time.Second).String(),
		Model:   item.Model, APIKeyConfigured: item.APIKeyEncrypted != "",
		TestStatus: item.TestStatus, LastTestError: item.LastTestError,
		Enabled: item.IsEnabled, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

// validateVLMInput 校验并归一化名称、地址、超时与模型 ID，返回可以直接落库的值。
// 规则与重排配置保持一致（同一套设置页交互，边界不该两处漂移）。
func validateVLMInput(input requestdto.VLMSetting) (string, string, time.Duration, string, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return "", "", 0, "", apperrors.New(apperrors.CodeBadRequest, "配置名称不能为空且不能超过 120 个字符")
	}
	baseURL, timeout, model, err := validateVLMTarget(input.BaseURL, input.Timeout, input.Model)
	if err != nil {
		return "", "", 0, "", err
	}
	return name, baseURL, timeout, model, nil
}

// validateVLMTarget 校验并归一化地址、超时与模型 ID —— 保存与"测试连接"共用，
// 保证"能测通的配置一定存得下、能存下的配置一定测得了"。
func validateVLMTarget(baseURLRaw, timeoutRaw, modelRaw string) (string, time.Duration, string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(baseURLRaw), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", 0, "", apperrors.New(apperrors.CodeBadRequest, "Base URL 必须是有效的 HTTP 或 HTTPS 地址")
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(timeoutRaw))
	if err != nil || timeout < time.Second || timeout > 600*time.Second {
		return "", 0, "", apperrors.New(apperrors.CodeBadRequest, "请求超时必须在 1 秒到 600 秒之间")
	}
	model := strings.TrimSpace(modelRaw)
	if model == "" || len([]rune(model)) > 160 {
		return "", 0, "", apperrors.New(apperrors.CodeBadRequest, "模型 ID 不能为空且不能超过 160 个字符")
	}
	return baseURL, timeout, model, nil
}
