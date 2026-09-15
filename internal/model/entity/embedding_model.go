package entity

// 向量模型实体对应向量模型表，记录一个可用于知识库的向量模型。
//
// 向量维度是模型的固定输出长度；检索时只可比较同一模型生成的向量。
// 默认标记为真的模型用于线上知识检索，数据库通过部分唯一索引保证最多一条。
type EmbeddingModel struct {
	BaseModel

	Name         string  `gorm:"column:name;type:varchar(160);not null" json:"name"`          // 模型名称或服务端模型 ID，如 text-embedding-3-small
	Provider     string  `gorm:"column:provider;type:varchar(80);not null" json:"provider"`   // 模型服务提供方或协议类型，如 openai-compatible
	BaseURL      *string `gorm:"column:base_url;type:text" json:"base_url"`                   // 该模型的服务根地址；为空时由应用的全局 embedding 配置提供
	Dimensions   int32   `gorm:"column:dimensions;not null" json:"dimensions"`                // 模型固定输出的向量维度
	ModelVersion *string `gorm:"column:model_version;type:varchar(160)" json:"model_version"` // 可选的提供方模型版本，用于追踪模型升级
	IsDefault    bool    `gorm:"column:is_default;not null" json:"is_default"`                // 是否为线上 RAG 检索默认使用的模型
	Enabled      bool    `gorm:"column:enabled;not null" json:"enabled"`                      // 是否允许继续为知识切片生成或检索该模型的向量
}

func (EmbeddingModel) TableName() string { return "embedding_models" }
