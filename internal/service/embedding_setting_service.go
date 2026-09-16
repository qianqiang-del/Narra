package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/embedding"

	"gorm.io/gorm"
)

type embeddingSettingService struct {
	repo    repository.EmbeddingSettingRepository
	manager *embedding.Manager
	secret  string
}

func NewEmbeddingSettingService(repo repository.EmbeddingSettingRepository, manager *embedding.Manager, secret string) EmbeddingSettingService {
	return &embeddingSettingService{repo: repo, manager: manager, secret: secret}
}

func (s *embeddingSettingService) Current(ctx context.Context) (responsedto.EmbeddingSetting, error) {
	setting, err := s.repo.GetActive(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return settingResponse("默认配置", s.manager.Config()), nil
	}
	if err != nil {
		return responsedto.EmbeddingSetting{}, fmt.Errorf("查询向量配置失败: %w", err)
	}

	cfg, err := s.configFromSetting(setting)
	if err != nil {
		return responsedto.EmbeddingSetting{}, err
	}
	return settingResponse(setting.Name, cfg), nil
}

func (s *embeddingSettingService) Save(ctx context.Context, input requestdto.EmbeddingSetting) (responsedto.EmbeddingSetting, error) {
	current, err := s.repo.GetActive(ctx)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return responsedto.EmbeddingSetting{}, fmt.Errorf("查询当前向量配置失败: %w", err)
	}

	apiKey, err := s.resolveAPIKey(current, input.APIKey)
	if err != nil {
		return responsedto.EmbeddingSetting{}, err
	}
	cfg, err := configFromInput(input, apiKey)
	if err != nil {
		return responsedto.EmbeddingSetting{}, err
	}

	setting, err := s.settingFromConfig(current, cfg)
	if err != nil {
		return responsedto.EmbeddingSetting{}, err
	}
	if err := s.repo.SaveActive(ctx, setting); err != nil {
		return responsedto.EmbeddingSetting{}, fmt.Errorf("保存向量配置失败: %w", err)
	}
	if err := s.manager.Configure(cfg); err != nil {
		return responsedto.EmbeddingSetting{}, err
	}
	return settingResponse(setting.Name, cfg), nil
}

func (s *embeddingSettingService) Test(ctx context.Context, input requestdto.EmbeddingSetting) (int, error) {
	current, err := s.repo.GetActive(ctx)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("查询当前向量配置失败: %w", err)
	}
	apiKey, err := s.resolveAPIKey(current, input.APIKey)
	if err != nil {
		return 0, err
	}
	cfg, err := configFromInput(input, apiKey)
	if err != nil {
		return 0, err
	}
	if err := embedding.Test(ctx, cfg); err != nil {
		return 0, fmt.Errorf("向量服务连接或返回结果无效: %w", err)
	}
	return cfg.Dimensions, nil
}

func (s *embeddingSettingService) LoadActive(ctx context.Context) error {
	setting, err := s.repo.GetActive(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取已保存的向量配置失败: %w", err)
	}
	cfg, err := s.configFromSetting(setting)
	if err != nil {
		return err
	}
	return s.manager.Configure(cfg)
}

func (s *embeddingSettingService) resolveAPIKey(current *entity.EmbeddingSetting, input string) (string, error) {
	if apiKey := strings.TrimSpace(input); apiKey != "" {
		return apiKey, nil
	}
	if current != nil {
		return s.decrypt(current.APIKeyEncrypted)
	}
	return s.manager.Config().APIKey, nil
}

func (s *embeddingSettingService) settingFromConfig(current *entity.EmbeddingSetting, cfg config.EmbeddingConfig) (*entity.EmbeddingSetting, error) {
	encryptedKey, err := s.encrypt(cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("加密向量服务密钥失败: %w", err)
	}
	setting := &entity.EmbeddingSetting{
		Name:            "默认配置",
		Provider:        "openai-compatible",
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		Dimensions:      int32(cfg.Dimensions),
		TimeoutSeconds:  int32(cfg.Timeout / time.Second),
		APIKeyEncrypted: encryptedKey,
	}
	if current != nil {
		setting.Name = current.Name
	}
	return setting, nil
}

func (s *embeddingSettingService) configFromSetting(setting *entity.EmbeddingSetting) (config.EmbeddingConfig, error) {
	apiKey, err := s.decrypt(setting.APIKeyEncrypted)
	if err != nil {
		return config.EmbeddingConfig{}, fmt.Errorf("解密向量服务密钥失败: %w", err)
	}
	cfg := config.EmbeddingConfig{
		Enabled:    true,
		APIKey:     apiKey,
		BaseURL:    setting.BaseURL,
		Model:      setting.Model,
		Timeout:    time.Duration(setting.TimeoutSeconds) * time.Second,
		Dimensions: int(setting.Dimensions),
	}
	return cfg, cfg.Validate()
}

func configFromInput(input requestdto.EmbeddingSetting, apiKey string) (config.EmbeddingConfig, error) {
	timeout, err := time.ParseDuration(input.Timeout)
	if err != nil {
		return config.EmbeddingConfig{}, fmt.Errorf("向量服务超时格式无效: %w", err)
	}
	cfg := config.EmbeddingConfig{Enabled: true, APIKey: apiKey, BaseURL: input.BaseURL, Model: input.Model, Timeout: timeout, Dimensions: input.Dimensions}
	return cfg, cfg.Validate()
}

func settingResponse(name string, cfg config.EmbeddingConfig) responsedto.EmbeddingSetting {
	return responsedto.EmbeddingSetting{Name: name, BaseURL: cfg.BaseURL, Model: cfg.Model, Timeout: cfg.Timeout.String(), Dimensions: cfg.Dimensions, APIKeyConfigured: cfg.APIKey != ""}
}

func (s *embeddingSettingService) encrypt(plainText string) (string, error) {
	if plainText == "" {
		return "", nil
	}
	block, err := aes.NewCipher(s.key())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plainText), nil)), nil
}

func (s *embeddingSettingService) decrypt(cipherText string) (string, error) {
	if cipherText == "" {
		return "", nil
	}
	block, err := aes.NewCipher(s.key())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	cipherBytes, err := base64.RawStdEncoding.DecodeString(cipherText)
	if err != nil {
		return "", fmt.Errorf("密文编码无效: %w", err)
	}
	if len(cipherBytes) < gcm.NonceSize() {
		return "", fmt.Errorf("密文格式无效")
	}
	nonce, cipherBytes := cipherBytes[:gcm.NonceSize()], cipherBytes[gcm.NonceSize():]
	plainText, err := gcm.Open(nil, nonce, cipherBytes, nil)
	if err != nil {
		return "", err
	}
	return string(plainText), nil
}

func (s *embeddingSettingService) key() []byte {
	key := sha256.Sum256([]byte(s.secret))
	return key[:]
}
