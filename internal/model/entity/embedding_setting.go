package entity

// EmbeddingSetting 是一个可保存的 OpenAI 兼容 Embedding 服务配置。
//
// 系统可保存多条配置，但业务层保证同一时间只有一条 IsActive 为 true，
// 运行中的向量化请求只使用这条配置。
type EmbeddingSetting struct {
	BaseModel

	Name            string `gorm:"column:name;type:varchar(120);not null;unique" json:"name"`
	Provider        string `gorm:"column:provider;type:varchar(80);not null;default:openai-compatible" json:"provider"`
	BaseURL         string `gorm:"column:base_url;type:text;not null" json:"base_url"`
	Model           string `gorm:"column:model;type:varchar(160);not null" json:"model"`
	Dimensions      int32  `gorm:"column:dimensions;not null" json:"dimensions"`
	TimeoutSeconds  int32  `gorm:"column:timeout_seconds;not null" json:"timeout_seconds"`
	APIKeyEncrypted string `gorm:"column:api_key_encrypted;type:text;not null;default:''" json:"-"`
	IsActive        bool   `gorm:"column:is_active;not null;default:false" json:"is_active"`
}

func (EmbeddingSetting) TableName() string { return "embedding_settings" }
