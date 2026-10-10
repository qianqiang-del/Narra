package entity

// EmbeddingSetting 是一个可保存的 OpenAI 兼容 Embedding 服务配置。
// 系统可保存多条配置，但同一时间只有一条 IsActive 为 true——由 IsActive 上的
// 部分唯一索引（WHERE is_active）在数据库层保证，运行中的向量化请求只使用这条配置。
type EmbeddingSetting struct {
	BaseModel
	OwnerID uint64 `gorm:"column:owner_id;not null;index:idx_embedding_settings_owner_id;uniqueIndex:embedding_settings_owner_name_key,priority:1;uniqueIndex:embedding_settings_one_active_idx,priority:1,where:is_active" json:"-"`
	Owner   *User  `gorm:"foreignKey:OwnerID;constraint:embedding_settings_owner_id_fkey,OnDelete:RESTRICT" json:"-"`

	Name            string `gorm:"column:name;type:varchar(120);not null;uniqueIndex:embedding_settings_owner_name_key,priority:2;comment:用户内唯一的配置名称" json:"name"`
	Provider        string `gorm:"column:provider;type:varchar(80);not null;default:openai-compatible;comment:服务方协议类型，默认 openai-compatible" json:"provider"`
	BaseURL         string `gorm:"column:base_url;type:text;not null;comment:服务根地址，如 https://api.siliconflow.cn/v1" json:"base_url"`
	Model           string `gorm:"column:model;type:varchar(160);not null;comment:调用的向量模型名，如 BAAI/bge-m3" json:"model"`
	Dimensions      int32  `gorm:"column:dimensions;not null;check:embedding_settings_dimensions_check,dimensions > 0;comment:该模型输出的向量维度，必须与模型真实维度一致" json:"dimensions"`
	TimeoutSeconds  int32  `gorm:"column:timeout_seconds;not null;check:embedding_settings_timeout_seconds_check,timeout_seconds > 0;comment:单次请求超时秒数" json:"timeout_seconds"`
	APIKeyEncrypted string `gorm:"column:api_key_encrypted;type:text;not null;default:'';comment:加密后的 API Key；明文不落库，接口也不返回" json:"-"`
	IsActive        bool   `gorm:"column:is_active;not null;default:false;comment:是否为当前用户生效的配置" json:"is_active"`
}

func (EmbeddingSetting) TableName() string { return "embedding_settings" }
