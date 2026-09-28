package discussion

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appcrypto "narra/pkg/crypto"
	"narra/pkg/llm"

	"narra/internal/model/entity"
)

// ProviderFinder 是建模型运行时所需的最小配置来源。
//
// 只声明一个方法，而不是直接吃 repository.LLMProviderRepository 整个接口：
// 这里用得到的只有"按 ID 取一条配置"。接口越小，测试越好造替身，
// 也不会因为仓储那边多长出一个方法就牵动本包。
type ProviderFinder interface {
	FindByID(ctx context.Context, id uint64) (*Provider, error)
}

// Provider 是建模型要用到的几个字段。
//
// 刻意不是 entity.LLMProvider：那个结构体带着 GORM 标签、时间戳、测试状态等
// 与本包无关的东西，直接用会把数据层的形状焊进编排层。
// 装配方在外面做一次转换即可（见 internal/app）。
type Provider struct {
	BaseURL         string
	APIKeyEncrypted string
	TimeoutSeconds  int32
	Models          json.RawMessage
}

// RuntimeFactory 按课堂配置现建一套模型能力。
//
// 为什么是"现建"而不是"启动时建好一份共用"：每堂课选的服务商和模型都可能不同，
// 而 Client 是配置快照（base URL / key / model 在创建时就固化了），
// 共用一份的结果是 A 课堂的发言发到 B 课堂的模型上去，且不会报任何错。
type RuntimeFactory struct {
	providers ProviderFinder

	// encryptionKey 是解密 API Key 用的密钥，构造时拷一份。
	// 留着引用的话，调用方之后改了那个切片，这里读到的密文就解不开了 —— 而且报的是
	// "解密失败"这种看起来像数据坏了的错。
	encryptionKey []byte
}

// NewRuntimeFactory 创建模型运行时工厂。
func NewRuntimeFactory(providers ProviderFinder, encryptionKey []byte) *RuntimeFactory {
	key := make([]byte, len(encryptionKey))
	copy(key, encryptionKey)

	return &RuntimeFactory{providers: providers, encryptionKey: key}
}

// ProviderLookup 是按 ID 取一条大模型配置的能力，签名与仓储那一层一致。
//
// 单开一个接口，是为了让装配方不必为了喂给本包而写一层转换：
// 仓储直接实现它就够了（见 FromEntity）。
type ProviderLookup interface {
	FindByID(ctx context.Context, id uint64) (*entity.LLMProvider, error)
}

// NewEntityProviderFinder 把仓储包成本包要的配置来源。
//
// 为什么要包一层：本包只想要"地址、密文、超时、模型列表"这四个字段，
// 而仓储返回的实体带着 GORM 标签、测试状态、时间戳等等。直接在实体上开一个
// 只读四个字段的方法是不行的 —— 那会把数据层的形状拉进本包的依赖。
// 转换放在这里，还顺手把"仓储返回 nil"这种不该发生的情况挡掉。
func NewEntityProviderFinder(lookup ProviderLookup) ProviderFinder {
	return entityProviderFinder{lookup: lookup}
}

// entityProviderFinder 是 ProviderFinder 的实现。
type entityProviderFinder struct {
	lookup ProviderLookup
}

func (f entityProviderFinder) FindByID(ctx context.Context, id uint64) (*Provider, error) {
	provider, err := f.lookup.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, nil
	}
	return &Provider{
		BaseURL:         provider.BaseURL,
		APIKeyEncrypted: provider.APIKeyEncrypted,
		TimeoutSeconds:  provider.TimeoutSeconds,
		Models:          provider.Models,
	}, nil
}

// Build 按服务商 ID 与模型 ID 建一套模型能力。
//
// modelID 为空时取该服务商模型列表里的第一个 —— 课程快照可能只记了服务商，
// 让"没选具体模型"落到一个确定的行为上，而不是留个空字符串一路传到 HTTP 请求里。
//
// 三项能力共用同一个 Client：它们打的是同一个地址、同一个模型、同一个密钥，
// 建三次只会多出三份一模一样的连接池。
func (f *RuntimeFactory) Build(ctx context.Context, providerID uint64, modelID string) (Models, error) {
	if f.providers == nil {
		return Models{}, fmt.Errorf("建讨论模型：缺少大模型配置仓储")
	}
	if len(f.encryptionKey) == 0 {
		return Models{}, fmt.Errorf("建讨论模型：缺少解密密钥")
	}

	provider, err := f.providers.FindByID(ctx, providerID)
	if err != nil {
		return Models{}, fmt.Errorf("建讨论模型：读取大模型配置 #%d 失败: %w", providerID, err)
	}
	if provider == nil {
		return Models{}, fmt.Errorf("建讨论模型：大模型配置 #%d 不存在", providerID)
	}

	if modelID == "" {
		if modelID, err = firstModel(provider.Models); err != nil {
			return Models{}, fmt.Errorf("建讨论模型：%w", err)
		}
	}

	apiKey, err := decryptAPIKey(provider.APIKeyEncrypted, f.encryptionKey)
	if err != nil {
		return Models{}, fmt.Errorf("建讨论模型：%w", err)
	}

	client, err := llm.NewClient(llm.Config{
		BaseURL: provider.BaseURL,
		APIKey:  apiKey,
		Model:   modelID,
		Timeout: time.Duration(provider.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		return Models{}, fmt.Errorf("建讨论模型：创建大模型客户端失败: %w", err)
	}

	models, err := NewOpenAIModels(client)
	if err != nil {
		return Models{}, fmt.Errorf("建讨论模型：%w", err)
	}

	return Models{
		Model:      models,
		Summarizer: models,
		Extractor:  models,
	}, nil
}

// decryptAPIKey 解出明文密钥。
//
// 密文为空时直接返回空串，不去解密：本地跑的 OpenAI 兼容服务（Ollama、vLLM 之类）
// 根本不需要密钥，库里那一列就是空的。真去解一个空串只会得到 base64 解码失败，
// 把"本来能跑"的场景挡在门外。
//
// 空 Key 的语义由 llm.Client 承接：它在 Key 为空时不设 Authorization 头。
func decryptAPIKey(encrypted string, key []byte) (string, error) {
	if encrypted == "" {
		return "", nil
	}
	plaintext, err := appcrypto.Decrypt(encrypted, key)
	if err != nil {
		return "", fmt.Errorf("解密 API Key 失败: %w", err)
	}
	return plaintext, nil
}

// firstModel 取模型列表里的第一个。
func firstModel(raw json.RawMessage) (string, error) {
	var models []string
	if err := json.Unmarshal(raw, &models); err != nil {
		return "", fmt.Errorf("解析模型列表失败: %w", err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("大模型配置的模型列表为空")
	}
	// 首项是空串的列表等于没有可用模型：放过去会让失败推迟到第一次调模型，
	// 那时运行记录已经建了，用户看到的是一场莫名其妙的失败。
	if models[0] == "" {
		return "", fmt.Errorf("大模型配置的模型列表首项为空")
	}
	return models[0], nil
}
