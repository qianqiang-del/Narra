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

// embeddingSettingService 是 Embedding 配置管理的业务实现。
//
// 它维护的是两份配置的一致性：repo 里那份持久化、可跨重启恢复，
// manager 里那份是进程内当前生效的。写路径两份一起改，读路径一律以 manager 为准。
type embeddingSettingService struct {
	repo    repository.EmbeddingSettingRepository
	manager *embedding.Manager
	secret  string
}

// NewEmbeddingSettingService 创建 Embedding 配置服务。
// secret 用来派生加密密钥，APIKey 用它加密后才落库。
func NewEmbeddingSettingService(repo repository.EmbeddingSettingRepository, manager *embedding.Manager, secret string) EmbeddingSettingService {
	return &embeddingSettingService{repo: repo, manager: manager, secret: secret}
}

// Current 返回当前生效配置的对外结构。
// 一条配置都没保存过时不算错误：回退启动时读到的 manager 配置，
// 让设置页首次打开就有一份可编辑的初值，而不是报错或者空白表单。
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

// Save 保存配置并让它立刻生效。
// 顺序是先落库、再热更新 manager：落库失败时运行时配置原封不动，
// 内存里那份要么还是旧的完整配置，要么已经换成新的完整配置，不会半新半旧。
func (s *embeddingSettingService) Save(ctx context.Context, input requestdto.EmbeddingSetting) (responsedto.EmbeddingSetting, error) {
	// 读当前记录有两个用途：请求没带密钥时沿用旧的，以及复用它的行做覆盖更新。
	// 首次保存查不到记录不算错误。
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

// Test 用给定配置真实请求一次 embedding 服务，验证连通性和返回是否可用。
// 不写库、也不改当前生效配置，用户可以先试再存。
// 能走通就说明上游返回的向量条数和维度都跟配置对得上（client.Embed 会逐条校验维度），
// 成功时再把配置里的 Dimensions 回给前端，让它跟模型文档核对。
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

// LoadActive 在启动时把持久化的配置灌进 manager。
// 没有保存过记录时返回 nil 而不是报错：此时继续用配置文件里的默认配置，
// 不该因为"用户还没配过"就让进程起不来。读到记录却还原失败（密钥解不开、
// 参数校验不过）才返回错误，那是真的配置损坏，启动时就暴露比运行时才炸好。
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

// resolveAPIKey 决定这次保存该用哪个密钥，优先级依次是：
// 请求里填的 > 库里已存（解密后）> 配置文件默认值。
// 之所以不是"请求为空就存空"，是因为响应里的密钥是脱敏的，
// 用户只改模型名时表单回填的密钥天然是空的，直接采信会把已保存的密钥清掉。
func (s *embeddingSettingService) resolveAPIKey(current *entity.EmbeddingSetting, input string) (string, error) {
	if apiKey := strings.TrimSpace(input); apiKey != "" {
		return apiKey, nil
	}
	if current != nil {
		return s.decrypt(current.APIKeyEncrypted)
	}
	return s.manager.Config().APIKey, nil
}

// settingFromConfig 把运行时配置转成可落库的实体，APIKey 在这里加密。
// 名称沿用原有记录：Name 上有唯一约束又没有改名入口，沿用才能让每次保存
// 都是覆盖同一行。Provider 固定写 openai-compatible，当前只支持这一种协议。
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

// configFromSetting 把库里的记录还原成运行时配置。
// Enabled 固定为 true：能存进这张表就说明用户要启用它，
// 配置文件里的 Enabled 只决定启动时有没有那份默认配置。
// 末尾再跑一次 Validate，是因为库里的数据可能来自更早的版本，参数未必仍然合法。
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

// configFromInput 把请求 DTO 转成运行时配置。
// 超时在 DTO 里是 "30s" 这类字符串，先解析回 time.Duration，
// 解析失败和后面的 Validate 失败都会带上原因返回给调用方。
func configFromInput(input requestdto.EmbeddingSetting, apiKey string) (config.EmbeddingConfig, error) {
	timeout, err := time.ParseDuration(input.Timeout)
	if err != nil {
		return config.EmbeddingConfig{}, fmt.Errorf("向量服务超时格式无效: %w", err)
	}
	cfg := config.EmbeddingConfig{Enabled: true, APIKey: apiKey, BaseURL: input.BaseURL, Model: input.Model, Timeout: timeout, Dimensions: input.Dimensions}
	return cfg, cfg.Validate()
}

// settingResponse 组装对外响应。
// 密钥只以 APIKeyConfigured 这个布尔值的形式出网：明文有泄露风险，
// 密文出网则等于把离线爆破的素材送给前端。
func settingResponse(name string, cfg config.EmbeddingConfig) responsedto.EmbeddingSetting {
	return responsedto.EmbeddingSetting{Name: name, BaseURL: cfg.BaseURL, Model: cfg.Model, Timeout: cfg.Timeout.String(), Dimensions: cfg.Dimensions, APIKeyConfigured: cfg.APIKey != ""}
}

// encrypt 用 AES-256-GCM 加密 APIKey，再按 Base64 落库。
// 空串原样返回，允许对接不校验密钥的本地 embedding 服务。
// 用 GCM 而不是 CBC 这类模式：它自带完整性校验，密文被人改过会在解密时报错，
// 而不是解出一段乱码拿去请求上游。
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

// decrypt 解开 encrypt 写下的密文。
// nonce 每次加密都是随机的，并被拼在密文最前面一起 Base64：
// GCM 要求同一密钥下不要复用 nonce，随密文存才能在解密时取回它。
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

// key 把配置里的 secret 派生成 32 字节的 AES-256 密钥。
// 用 SHA-256 而不是截断：secret 长度不定，哈希能统一规整到密钥所需长度。
// 注意这里复用的是 JWT 的 secret（见 app.go），改动 JWT.Secret 会让已存的
// APIKey 全部解不开，需要重新保存一次配置。
func (s *embeddingSettingService) key() []byte {
	key := sha256.Sum256([]byte(s.secret))
	return key[:]
}
