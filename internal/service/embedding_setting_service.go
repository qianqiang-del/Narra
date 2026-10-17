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
	"narra/internal/rag"
	"narra/internal/repository"
	"narra/pkg/config"
	"narra/pkg/embedding"
	"narra/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// defaultEmbeddingSettingName 是用户个人配置的默认名称。
// Name 在"账号 + 名字"上有唯一约束又没有改名入口，沿用同一个名字才能让每次保存
// 都是覆盖同一行。
const defaultEmbeddingSettingName = "默认配置"

// embeddingSettingService 是 Embedding 配置管理的业务实现。
//
// 它维护三份事实的一致性：
//   - repo 里那份持久化、可跨重启恢复的**用户配置**（只有"用户自己保存过"才存在）；
//   - defaults 那份**平台默认配置**（来自 config.yaml / 环境变量，只读、不落库）；
//   - modelRepo 里"这个模型是谁"（按账号登记，knowledge_embeddings.model_id 指向它）。
//
// 运行时不再有"进程级一份生效配置"：收录与检索通过 CurrentEmbedding 按账号现取，
// 用户配置优先、缺失回退平台默认。因此设置页保存之后立即生效，不需要热更新任何
// 共享状态 —— 这也是"多账号各用各的"能成立的前提。
type embeddingSettingService struct {
	repo      repository.EmbeddingSettingRepository
	modelRepo repository.EmbeddingModelRepository
	defaults  config.EmbeddingConfig
	secret    string
}

// NewEmbeddingSettingService 创建 Embedding 配置服务。
// defaults 是平台默认配置（config.yaml / 环境变量）；secret 用来派生加密密钥，
// APIKey 用它加密后才落库。
func NewEmbeddingSettingService(repo repository.EmbeddingSettingRepository, modelRepo repository.EmbeddingModelRepository, defaults config.EmbeddingConfig, secret string) EmbeddingSettingService {
	return &embeddingSettingService{repo: repo, modelRepo: modelRepo, defaults: defaults, secret: secret}
}

// Current 返回该账号当前生效配置的对外结构。
//
// 该账号没有保存过时不算错误：返回一份空表单并把 UsingPlatformDefault 标出来。
// 刻意不预填平台默认的地址与模型 —— 那样用户只改一个字段、直接保存就会造出
// 一份"半拷贝"的个人配置，而平台默认的密钥又不会被继承，看似保存成功实际不可用。
// 空表单让"要自己配就从头填"语义明确。
func (s *embeddingSettingService) Current(ctx context.Context, ownerID uint64) (responsedto.EmbeddingSetting, error) {
	setting, err := s.repo.GetActiveByOwner(ctx, ownerID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return responsedto.EmbeddingSetting{
			Name:                 defaultEmbeddingSettingName,
			Timeout:              s.defaultTimeout().String(),
			UsingPlatformDefault: s.defaults.Enabled,
		}, nil
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

// Save 保存该账号的配置并让它立刻生效。
//
// 顺序是先登记模型行、再落库：落库失败时模型行最多留下一行没被引用的记录，
// 下次保存即修正；反过来的话一旦登记因维度冲突被拒，配置已经改成新维度、模型行
// 还停在旧维度，两边长期不一致而且没有任何提示。见 ensureDefaultModel 的注释。
//
// 不再有"热更新运行时"这一步：CurrentEmbedding 每次现取，落库成功即生效。
func (s *embeddingSettingService) Save(ctx context.Context, ownerID uint64, input requestdto.EmbeddingSetting) (responsedto.EmbeddingSetting, error) {
	if ownerID == 0 {
		return responsedto.EmbeddingSetting{}, fmt.Errorf("缺少用户身份，无法保存向量配置")
	}

	// 读当前记录有两个用途：请求没带密钥时沿用旧的，以及复用它的行做覆盖更新。
	// 首次保存查不到记录不算错误。
	current, err := s.repo.GetActiveByOwner(ctx, ownerID)
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

	if err := s.ensureDefaultModel(ctx, ownerID, cfg); err != nil {
		return responsedto.EmbeddingSetting{}, err
	}

	setting, err := s.settingFromConfig(current, cfg)
	if err != nil {
		return responsedto.EmbeddingSetting{}, err
	}
	if err := s.repo.SaveActiveForOwner(ctx, ownerID, setting); err != nil {
		return responsedto.EmbeddingSetting{}, fmt.Errorf("保存向量配置失败: %w", err)
	}
	return settingResponse(setting.Name, cfg), nil
}

// Test 用给定配置真实请求一次 embedding 服务，验证连通性和返回是否可用。
// 不写库、也不改当前生效配置，用户可以先试再存。
// 能走通就说明上游返回的向量条数和维度都跟配置对得上（client.Embed 会逐条校验维度），
// 成功时再把配置里的 Dimensions 回给前端，让它跟模型文档核对。
func (s *embeddingSettingService) Test(ctx context.Context, ownerID uint64, input requestdto.EmbeddingSetting) (int, error) {
	if ownerID == 0 {
		return 0, fmt.Errorf("缺少用户身份，无法测试向量配置")
	}

	current, err := s.repo.GetActiveByOwner(ctx, ownerID)
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

// CurrentEmbedding 返回该账号当前生效的连接配置：用户自己的配置优先，
// 没有则回退平台默认；平台默认也没启用时返回 Enabled = false。
//
// 它是 rag.EmbeddingProvider 的实现：收录与检索在每次使用前现取一次，
// 因此不持有任何跨请求的共享状态。ownerID 为 0（没有账号上下文的后台路径）
// 时按"没有用户配置"处理，直接回退平台默认。
func (s *embeddingSettingService) CurrentEmbedding(ctx context.Context, ownerID uint64) (config.EmbeddingConfig, error) {
	setting, err := s.repo.GetActiveByOwner(ctx, ownerID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.defaults, nil
	}
	if err != nil {
		return config.EmbeddingConfig{}, fmt.Errorf("查询向量配置失败: %w", err)
	}

	cfg, err := s.configFromSetting(setting)
	if err != nil {
		return config.EmbeddingConfig{}, err
	}
	return cfg, nil
}

// defaultTimeout 是平台默认配置的超时展示值；默认配置没填时给前端一个可编辑的初值。
func (s *embeddingSettingService) defaultTimeout() time.Duration {
	if s.defaults.Timeout > 0 {
		return s.defaults.Timeout
	}
	return 120 * time.Second
}

// ensureDefaultModel 把该账号当前生效配置登记进 embedding_models，供知识库写入向量时
// 引用，并为它补建检索用的向量索引（见 repository.EnsureVectorIndex）。
//
// 两张表职责不同：embedding_settings 记"怎么连服务"（地址、密钥、超时），
// embedding_models 记"这个模型是谁"（名称、维度，按账号登记）。knowledge_embeddings.model_id
// 外键指向后者，所以知识库要写向量就必须先有 model_id —— 缺了这一层，上传、解析、
// 切分全做完，最后一步会卡在"没有模型可指向"。
func (s *embeddingSettingService) ensureDefaultModel(ctx context.Context, ownerID uint64, cfg config.EmbeddingConfig) error {
	// 未启用时不登记：这时 model / dimensions 允许为空，登记出来的行对知识库也没有意义。
	if !cfg.Enabled {
		return nil
	}

	model, err := s.modelRepo.EnsureDefault(ctx, entity.EmbeddingModel{
		OwnerID:    ownerID,
		Name:       cfg.Model,
		Dimensions: int32(cfg.Dimensions),
	})
	if err != nil {
		var mismatch *repository.DimensionsMismatchError
		if errors.As(err, &mismatch) {
			return fmt.Errorf(
				"模型 %s 已按 %d 维登记，本次提交的是 %d 维，而该模型下已经有 %d 个切片向量。"+
					"就地改维度会让新旧向量归到同一个 model_id 下，检索时维度不一致会直接失败。"+
					"请改用另一个模型名（例如 %s-1024），或先删除该模型下的向量",
				mismatch.ModelName, mismatch.Recorded, mismatch.Requested, mismatch.Vectors, mismatch.ModelName,
			)
		}
		return fmt.Errorf("登记向量模型失败: %w", err)
	}

	// 换默认模型是"向量召回静默为零"最常见的来源：新模型名下还没有向量，而检索只查
	// 新模型名下的向量。保存配置的人正是刚刚做出这个动作的人，在这里立刻告警一次；
	// 判定与检索侧的空命中体检共用（rag.VectorRecallHint），不各写一份。
	s.warnIfDefaultModelLacksVectors(ctx, ownerID, model)

	// 默认模型定下来之后顺手把它的向量索引补上（幂等，维度或精度变了会自动重建）。
	// 挂在这里是因为这段代码是"换模型 / 改维度"的主要入口，跟着它就不会漏建。
	//
	// 失败只让检索退化成顺序扫描、不影响正确性，所以记日志即可、不上抛：
	// 为一条索引把"保存配置"判成失败是本末倒置。
	//
	// 两类别混为一谈：维度超过 halfvec 上限（4000 维，HNSW 连半精度也建不出）
	// 是**模型的长期属性**（不会被修好，检索也照常正确），每次启动报一条 Warn
	// 像是在出事，降成 Info；其余的失败（DDL 权限、数据库故障、pgvector 太老
	// 没有 halfvec）才是真要人看的，保持 Warn。
	if err := s.modelRepo.EnsureVectorIndex(ctx, model); err != nil {
		if errors.Is(err, repository.ErrVectorIndexUnsupported) {
			logger.Info("模型维度超过 halfvec 索引上限（4000 维），跳过建索引；知识检索将走精确顺序扫描（结果不受影响）",
				zap.String("model", cfg.Model),
				zap.Int32("dimensions", model.Dimensions),
			)
			return nil
		}
		logger.Warn("为默认模型建立向量索引失败，知识检索将退化为顺序扫描",
			zap.String("model", cfg.Model),
			zap.Int32("dimensions", model.Dimensions),
			zap.Error(err),
		)
	}
	return nil
}

// warnIfDefaultModelLacksVectors 在该账号默认模型名下没有任何向量、而其他模型下还有时告警。
//
// 与检索侧的空命中体检（rag.VectorRecallHint）共用一个判定，这里多一个时机：
// 保存配置的当下 —— 错误刚被做出来的时候提示，比等到用户搜不出东西再查强。
// 体检失败不喧哗：它是排障辅助，不该把"保存配置"判成失败。
func (s *embeddingSettingService) warnIfDefaultModelLacksVectors(ctx context.Context, ownerID uint64, model *entity.EmbeddingModel) {
	counts, err := s.modelRepo.CountVectorsByModel(ctx, ownerID)
	if err != nil {
		return
	}
	if hint := rag.VectorRecallHint(model, counts); hint != "" {
		logger.Warn(hint, zap.String("model", model.Name))
	}
}

// resolveAPIKey 决定这次保存该用哪个密钥，优先级依次是：
// 请求里填的 > 该账号库里已存（解密后）> 空串。
//
// 之所以不是"请求为空就存空"，是因为响应里的密钥是脱敏的，用户只改模型名时
// 表单回填的密钥天然是空的，直接采信会把已保存的密钥清掉。
//
// 最后一级刻意**不落到平台默认的 key**：平台默认是"用户没配时的兜底"，不是
// "可以被继承的密钥"。把平台 key 写进用户自己的记录，会让"用平台额度"变成
// 静默的既成事实。对接不校验密钥的本地服务时留空即合法，无需兜底。
func (s *embeddingSettingService) resolveAPIKey(current *entity.EmbeddingSetting, input string) (string, error) {
	if apiKey := strings.TrimSpace(input); apiKey != "" {
		return apiKey, nil
	}
	if current != nil {
		return s.decrypt(current.APIKeyEncrypted)
	}
	return "", nil
}

// settingFromConfig 把运行时配置转成可落库的实体，APIKey 在这里加密。
// 名称沿用原有记录：Name 上有"账号 + 名字"唯一约束又没有改名入口，沿用才能让
// 每次保存都是覆盖同一行。Provider 固定写 openai-compatible，当前只支持这一种协议。
func (s *embeddingSettingService) settingFromConfig(current *entity.EmbeddingSetting, cfg config.EmbeddingConfig) (*entity.EmbeddingSetting, error) {
	encryptedKey, err := s.encrypt(cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("加密向量服务密钥失败: %w", err)
	}
	setting := &entity.EmbeddingSetting{
		Name:            defaultEmbeddingSettingName,
		Provider:        entity.EmbeddingProviderOpenAICompatible,
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
// 配置文件里的 Enabled 只决定平台默认配置是否存在。
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
