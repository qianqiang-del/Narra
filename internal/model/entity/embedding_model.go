package entity

// EmbeddingProviderOpenAICompatible 是写入 embedding_settings.provider 的固定值。
//
// 目前只有这一个取值 —— Narra 只对接 OpenAI 兼容协议的向量服务，
// 表格设计上留了 provider 列是为了将来接原生协议的实现。
//
// 它放在 entity 而不是设置服务里，是因为它是**列取值**而不是某个服务的实现细节。
//
// 注：embedding_models 曾有一份同样的 provider 列，2026-09-30 清理冗余列时删除 ——
// 协议类型属于"怎么连服务"，只归 embedding_settings；那张表只留模型的**身份**。
const EmbeddingProviderOpenAICompatible = "openai-compatible"

// EmbeddingModel 向量模型实体对应向量模型表，记录一个可用于知识库的向量模型。
// 向量维度是模型的固定输出长度；检索时只可比较同一模型生成的向量。
// 默认标记为真的模型用于线上知识检索，数据库通过部分唯一索引保证最多一条。
//
// 这张表只留模型的**身份**（名称、维度）与默认标记：连接信息（地址、密钥、超时）
// 归 embedding_settings，而身份一旦登记就不随配置改写 —— knowledge_embeddings.model_id
// 外键指向本表，向量因此有一个稳定的归属；两者混在一起会让"改配置"变成
// "把旧向量悄悄改判成新模型的产物"（维度不同报错还算幸运，维度相同的静默劣化最难查）。
// provider / base_url / model_version / enabled 四列因无人读写已于 2026-09-30 删除
// （存量库用 migrations/0007 清理）。
//
// UNIQUE (name) 由 Name 上的 unique tag 声明；理由同 OrchestrationRun.TraceID——
// 单列唯一约束归 AutoMigrate，不能写进 0002 的 SQL。
// CHECK (dimensions > 0) 与「至多一条默认模型」的部分唯一索引同样由 tag 声明：
// uniqueIndex 建的是唯一索引而非约束，不会被 MigrateColumnUnique 对账（它只认 unique tag），
// 所以部分唯一可以安全地留在实体上。
type EmbeddingModel struct {
	BaseModel

	Name       string `gorm:"column:name;type:varchar(160);not null;unique;comment:模型名称或服务端模型 ID，如 text-embedding-3-small；全局唯一" json:"name"`                                          // 模型名称或服务端模型 ID，如 text-embedding-3-small
	Dimensions int32  `gorm:"column:dimensions;not null;check:embedding_models_dimensions_check,dimensions > 0;comment:模型固定输出的向量维度，必须与模型真实维度一致" json:"dimensions"`                    // 模型固定输出的向量维度
	IsDefault  bool   `gorm:"column:is_default;not null;uniqueIndex:embedding_models_one_default_idx,where:is_default;comment:是否为线上检索默认使用的模型；全表最多一条为 true（部分唯一索引）" json:"is_default"` // 是否为线上 RAG 检索默认使用的模型
}

func (EmbeddingModel) TableName() string { return "embedding_models" }

// ModelVectorCount 是一个模型名下的向量数，统计产物、不落库。
//
// 用途是"向量召回静默为零"的体检：默认模型名下没有任何向量、而其他模型下还有，
// 说明换过默认模型却没重新收录（判定与措辞见 rag.VectorRecallHint）。
// 没有向量的模型也会出现（计 0）—— "没有"本身就是体检要看的事实。
type ModelVectorCount struct {
	ModelID uint64 `gorm:"column:model_id" json:"model_id"` // 模型 ID
	Name    string `gorm:"column:name" json:"name"`         // 模型名
	Vectors int64  `gorm:"column:vectors" json:"vectors"`   // 该模型名下的向量数
}
