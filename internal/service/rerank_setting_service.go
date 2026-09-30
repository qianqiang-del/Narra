package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	appcrypto "narra/pkg/crypto"
	apperrors "narra/pkg/errors"
	"narra/pkg/logger"
	"narra/pkg/rerank"
)

// rerankProber 是一次连通性探测的最小依赖面；*rerank.Client 满足它。
//
// 定义成小接口是为了测试能注入替身（与 rag 的 embedderFactory 同一个路数）：
// 服务层要测的是"测试结果怎么流转"，不是 HTTP 协议本身，后者在 pkg/rerank 有自己的用例。
type rerankProber interface {
	Probe(ctx context.Context) (rerank.ProbeResult, error)
}

// RerankRuntime 是检索侧的精排运行时（*pkg/rerank.Manager 满足它）。
// 服务层只负责"什么时候换配置"，不关心"换了之后检索怎么用"。
type RerankRuntime interface {
	// Configure 用新配置替换当前生效的精排能力；配置非法时报错且保持原状。
	Configure(cfg rerank.Config) error

	// Disable 关闭精排（当前没有启用配置）。
	Disable()
}

type rerankSettingService struct {
	repo          repository.RerankSettingRepository
	encryptionKey []byte
	runtime       RerankRuntime
	newProber     func(cfg rerank.Config) (rerankProber, error)
}

// NewRerankSettingService 构造重排配置服务，encryptionKey 用于 API Key 的加解密，
// runtime 是检索侧的热更新入口。
func NewRerankSettingService(repo repository.RerankSettingRepository, encryptionKey []byte, runtime RerankRuntime) RerankSettingService {
	return &rerankSettingService{
		repo:          repo,
		encryptionKey: encryptionKey,
		runtime:       runtime,
		newProber: func(cfg rerank.Config) (rerankProber, error) {
			return rerank.NewClient(cfg)
		},
	}
}

// List 返回全部配置，含未测试和已停用的，供设置页展示。
func (s *rerankSettingService) List(ctx context.Context) ([]responsedto.RerankSetting, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询重排配置失败", err)
	}
	out := make([]responsedto.RerankSetting, 0, len(items))
	for _, item := range items {
		out = append(out, toRerankResponse(item))
	}
	return out, nil
}

// Create 新建配置：测通了才能手动启用，所以入库时固定是未测试 + 未启用。
func (s *rerankSettingService) Create(ctx context.Context, input requestdto.RerankSetting) (*responsedto.RerankSetting, error) {
	name, baseURL, timeout, model, err := validateRerankInput(input)
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
	item := &entity.RerankSetting{
		Name: name, BaseURL: baseURL, Model: model,
		APIKeyEncrypted: encrypted, TimeoutSeconds: int32(timeout / time.Second),
		TestStatus: entity.RerankTestStatusUntested, IsEnabled: false,
	}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeConflict, "配置名称已存在或保存失败", err)
	}
	// 新建一律不启用，运行时无需重载（启用要经过测试与 SetEnabled）。
	dto := toRerankResponse(*item)
	return &dto, nil
}

// Update 全量更新。地址、超时、模型、密钥任一变动，都把配置打回未测试并停用：
// 旧配置测通过不代表新配置能用，继续挂着"测试成功"的徽章是骗人的。
func (s *rerankSettingService) Update(ctx context.Context, id uint64, input requestdto.RerankSetting) (*responsedto.RerankSetting, error) {
	item, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	name, baseURL, timeout, model, err := validateRerankInput(input)
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
		item.TestStatus = entity.RerankTestStatusUntested
		item.LastTestError = nil
		item.IsEnabled = false
	}
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeConflict, "更新重排配置失败", err)
	}
	// 关键字段变动会把生效配置打回停用；无论改的是不是启用中的那条，都重载一次。
	s.reload(ctx)
	dto := toRerankResponse(*item)
	return &dto, nil
}

// Delete 删除配置；不检查是否正在被引用，因为检索侧读取的是"当前启用"这一行，
// 删掉启用中的配置等于精排关闭（不会自动切换到其它配置）。
func (s *rerankSettingService) Delete(ctx context.Context, id uint64) error {
	if _, err := s.find(ctx, id); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "删除重排配置失败", err)
	}
	// 删掉的正好是启用中的那条时，重载会把精排关掉。
	s.reload(ctx)
	return nil
}

// Test 发一次真实重排探测。失败要写回状态并停用该配置——测不通的配置不该继续被检索使用；
// 成功时保持原启用状态不动（测试不是启用动作）。
func (s *rerankSettingService) Test(ctx context.Context, id uint64) (*responsedto.RerankSettingTestResult, error) {
	item, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.decrypt(item.APIKeyEncrypted)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "解密 API Key 失败", err)
	}
	prober, err := s.newProber(rerank.Config{
		BaseURL: item.BaseURL,
		APIKey:  apiKey,
		Model:   item.Model,
		Timeout: time.Duration(item.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "构建重排客户端失败", err)
	}

	result, probeErr := prober.Probe(ctx)
	if probeErr != nil {
		item.IsEnabled = false
		message := truncateText(fmt.Sprintf("测试失败: %v", probeErr), 500)
		item.TestStatus, item.LastTestError = entity.RerankTestStatusFailed, &message
		if err := s.repo.Update(ctx, item); err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存测试结果失败", err)
		}
		// 失败会停用配置：如果它正生效，运行时必须跟着关掉。
		s.reload(ctx)
		return nil, apperrors.New(apperrors.CodeBadRequest, message)
	}

	item.TestStatus, item.LastTestError = entity.RerankTestStatusSuccess, nil
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "保存测试结果失败", err)
	}
	// 测试成功不改变启用状态，重载只是取最新（配置内容没变，代价可忽略）。
	s.reload(ctx)
	return &responsedto.RerankSettingTestResult{
		Success: true,
		Message: fmt.Sprintf("测试通过：相关内容 %.2f / 无关内容 %.2f", result.Relevant, result.Irrelevant),
	}, nil
}

// SetEnabled 切换启用状态；未测试通过的配置不许启用，否则检索会拿到一个必然失败的精排。
func (s *rerankSettingService) SetEnabled(ctx context.Context, id uint64, enabled bool) (*responsedto.RerankSetting, error) {
	item, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if enabled && item.TestStatus != entity.RerankTestStatusSuccess {
		return nil, apperrors.New(apperrors.CodeBadRequest, "请先测试连接，成功后才能启用")
	}
	applied, err := s.repo.SetEnabled(ctx, id, enabled)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "更新启用状态失败", err)
	}
	if !applied {
		return nil, apperrors.New(apperrors.CodeNotFound, "重排配置不存在")
	}
	item.IsEnabled = enabled
	// 启用/停用立即反映到检索侧：这是"设置页点一下开关，排序就变"的那根线。
	s.reload(ctx)
	dto := toRerankResponse(*item)
	return &dto, nil
}

// LoadActive 在启动时把持久化的启用配置灌进检索侧运行时。
// 没有启用记录时关闭精排并返回 nil —— "还没配过重排"不该让进程起不来。
func (s *rerankSettingService) LoadActive(ctx context.Context) error {
	item, err := s.repo.GetEnabled(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.runtime.Disable()
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取已保存的重排配置失败: %w", err)
	}
	apiKey, err := s.decrypt(item.APIKeyEncrypted)
	if err != nil {
		return fmt.Errorf("解密重排 API Key 失败: %w", err)
	}
	if err := s.runtime.Configure(rerank.Config{
		BaseURL: item.BaseURL,
		APIKey:  apiKey,
		Model:   item.Model,
		Timeout: time.Duration(item.TimeoutSeconds) * time.Second,
	}); err != nil {
		return fmt.Errorf("应用重排配置失败: %w", err)
	}
	return nil
}

// reload 把"当前启用配置"同步给检索侧运行时，供写操作落库后调用。
//
// 失败只告警、不上抛：配置已经保存成功，把请求判失败会让用户以为没存上；而重载
// 只是"让运行中的检索立刻用上新配置"，下一次写操作或重启都会再对齐。
func (s *rerankSettingService) reload(ctx context.Context) {
	item, err := s.repo.GetEnabled(ctx)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		s.runtime.Disable()
		return
	case err != nil:
		logger.Warn("读取启用的重排配置失败，精排保持当前状态", zap.Error(err))
		return
	}
	apiKey, err := s.decrypt(item.APIKeyEncrypted)
	if err != nil {
		logger.Warn("解密重排 API Key 失败，精排保持当前状态", zap.Uint64("id", item.ID), zap.Error(err))
		return
	}
	if err := s.runtime.Configure(rerank.Config{
		BaseURL: item.BaseURL,
		APIKey:  apiKey,
		Model:   item.Model,
		Timeout: time.Duration(item.TimeoutSeconds) * time.Second,
	}); err != nil {
		logger.Warn("应用重排配置失败，精排保持当前状态", zap.Uint64("id", item.ID), zap.Error(err))
	}
}

// find 按 ID 取配置，取不到就是 404 语义。
func (s *rerankSettingService) find(ctx context.Context, id uint64) (*entity.RerankSetting, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeNotFound, "重排配置不存在", err)
	}
	return item, nil
}

// encrypt / decrypt 与 llm_provider_service.go 实现相同：空值原样返回，
// 不产生密文，也就不会有假的「已配置」。
func (s *rerankSettingService) encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return appcrypto.Encrypt(value, s.encryptionKey)
}

func (s *rerankSettingService) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return appcrypto.Decrypt(value, s.encryptionKey)
}

// toRerankResponse 实体转响应结构。密钥只透出「是否已配置」这个布尔值。
func toRerankResponse(item entity.RerankSetting) responsedto.RerankSetting {
	return responsedto.RerankSetting{
		ID: item.ID, Name: item.Name, BaseURL: item.BaseURL,
		Timeout: (time.Duration(item.TimeoutSeconds) * time.Second).String(),
		Model:   item.Model, APIKeyConfigured: item.APIKeyEncrypted != "",
		TestStatus: item.TestStatus, LastTestError: item.LastTestError,
		Enabled: item.IsEnabled, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

// validateRerankInput 校验并归一化名称、地址、超时与模型 ID，返回可以直接落库的值。
// API Key 的校验复用同包 validateAPIKey（大模型配置也用它），规则保持一致。
func validateRerankInput(input requestdto.RerankSetting) (string, string, time.Duration, string, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return "", "", 0, "", apperrors.New(apperrors.CodeBadRequest, "配置名称不能为空且不能超过 120 个字符")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", 0, "", apperrors.New(apperrors.CodeBadRequest, "Base URL 必须是有效的 HTTP 或 HTTPS 地址")
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(input.Timeout))
	if err != nil || timeout < time.Second || timeout > 600*time.Second {
		return "", "", 0, "", apperrors.New(apperrors.CodeBadRequest, "请求超时必须在 1 秒到 600 秒之间")
	}
	model := strings.TrimSpace(input.Model)
	if model == "" || len([]rune(model)) > 160 {
		return "", "", 0, "", apperrors.New(apperrors.CodeBadRequest, "模型 ID 不能为空且不能超过 160 个字符")
	}
	return name, baseURL, timeout, model, nil
}
