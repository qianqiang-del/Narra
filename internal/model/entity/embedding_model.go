package entity

// EmbeddingProviderOpenAICompatible 是写入 embedding_models.provider 与
// embedding_settings.provider 两列的固定值。
//
// 两处都只有这一个取值 —— Narra 只对接 OpenAI 兼容协议的向量服务，
// 表格设计上留了 provider 列是为了将来接原生协议的实现。
//
// 它放在 entity 而不是设置服务里，是因为它是**列取值**而不是某个服务的实现细节：
// 收录链路（internal/rag）在登记默认模型时也要写这一列。
const EmbeddingProviderOpenAICompatible = "openai-compatible"

// EmbeddingModel 向量模型实体对应向量模型表，记录一个可用于知识库的向量模型。
// 向量维度是模型的固定输出长度；检索时只可比较同一模型生成的向量。
// 默认标记为真的模型用于线上知识检索，数据库通过部分唯一索引保证最多一条。
//
// UNIQUE (name) 由 Name 上的 unique tag 声明；理由同 OrchestrationRun.TraceID——
// 单列唯一约束归 AutoMigrate，不能写进 0002 的 SQL。
// CHECK (dimensions > 0) 与「至多一条默认模型」的部分唯一索引同样由 tag 声明：
// uniqueIndex 建的是唯一索引而非约束，不会被 MigrateColumnUnique 对账（它只认 unique tag），
// 所以部分唯一可以安全地留在实体上。
type EmbeddingModel struct {
	BaseModel

	Name         string  `gorm:"column:name;type:varchar(160);not null;unique" json:"name"`                                                  // 模型名称或服务端模型 ID，如 text-embedding-3-small
	Provider     string  `gorm:"column:provider;type:varchar(80);not null" json:"provider"`                                                  // 模型服务提供方或协议类型，如 openai-compatible
	BaseURL      *string `gorm:"column:base_url;type:text" json:"base_url"`                                                                  // 该模型的服务根地址；为空时由应用的全局 embedding 配置提供
	Dimensions   int32   `gorm:"column:dimensions;not null;check:embedding_models_dimensions_check,dimensions > 0" json:"dimensions"`        // 模型固定输出的向量维度
	ModelVersion *string `gorm:"column:model_version;type:varchar(160)" json:"model_version"`                                                // 可选的提供方模型版本，用于追踪模型升级
	IsDefault    bool    `gorm:"column:is_default;not null;uniqueIndex:embedding_models_one_default_idx,where:is_default" json:"is_default"` // 是否为线上 RAG 检索默认使用的模型
	Enabled      bool    `gorm:"column:enabled;not null" json:"enabled"`                                                                     // 是否允许继续为知识切片生成或检索该模型的向量
}

func (EmbeddingModel) TableName() string { return "embedding_models" }
