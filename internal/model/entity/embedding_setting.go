package entity

// EmbeddingSetting 是一个可保存的 OpenAI 兼容 Embedding 服务配置。
// 系统可保存多条配置，但同一时间只有一条 IsActive 为 true——由 IsActive 上的
// 部分唯一索引（WHERE is_active）在数据库层保证，运行中的向量化请求只使用这条配置。
type EmbeddingSetting struct {
	BaseModel

	Name            string `gorm:"column:name;type:varchar(120);not null;unique;comment:配置名称，全局唯一" json:"name"`
	Provider        string `gorm:"column:provider;type:varchar(80);not null;default:openai-compatible;comment:服务方协议类型，默认 openai-compatible" json:"provider"`
	BaseURL         string `gorm:"column:base_url;type:text;not null;comment:服务根地址，如 https://api.siliconflow.cn/v1" json:"base_url"`
	Model           string `gorm:"column:model;type:varchar(160);not null;comment:调用的向量模型名，如 BAAI/bge-m3" json:"model"`
	Dimensions      int32  `gorm:"column:dimensions;not null;check:embedding_settings_dimensions_check,dimensions > 0;comment:该模型输出的向量维度，必须与模型真实维度一致" json:"dimensions"`
	TimeoutSeconds  int32  `gorm:"column:timeout_seconds;not null;check:embedding_settings_timeout_seconds_check,timeout_seconds > 0;comment:单次请求超时秒数" json:"timeout_seconds"`
	APIKeyEncrypted string `gorm:"column:api_key_encrypted;type:text;not null;default:'';comment:加密后的 API Key；明文不落库，接口也不返回" json:"-"`
	IsActive        bool   `gorm:"column:is_active;not null;default:false;uniqueIndex:embedding_settings_one_active_idx,where:is_active;comment:是否为当前生效的配置；全表最多一条为 true（部分唯一索引），运行中的向量化只用这条" json:"is_active"`
}

func (EmbeddingSetting) TableName() string { return "embedding_settings" }
