package entity

import "time"

// KnowledgeEmbedding 知识向量实体对应知识向量表，保存一个切片在一个向量模型下的结果。
// 同一切片可对应多个模型，以支持模型升级和效果对比；向量值使用向量扩展的文本格式，
// 例如 "[0.1,0.2]"。向量维度与实际向量长度由字段上的 check tag 约束校验。
//
// knowledge_embeddings_chunk_id_idx 与 UNIQUE (chunk_id, model_id) 的伴生索引功能重复，
// 保留是沿用原 0002 SQL 的行为。
type KnowledgeEmbedding struct {
	ID          uint64    `gorm:"column:id;primaryKey;autoIncrement;comment:向量记录主键" json:"id"`                                                                                                                                                 // 向量记录主键
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime;comment:记录写入时间，timestamptz 按 UTC 存" json:"created_at"`                                                                                                              // 向量记录写入时间
	ChunkID     uint64    `gorm:"column:chunk_id;not null;uniqueIndex:knowledge_embeddings_chunk_id_model_id_key;index:knowledge_embeddings_chunk_id_idx;comment:对应的切片 ID，指向 knowledge_chunks.id；切片删除时本向量级联删除" json:"chunk_id"`                // 对应的知识切片 ID；删除切片时级联删除
	ModelID     uint64    `gorm:"column:model_id;not null;uniqueIndex:knowledge_embeddings_chunk_id_model_id_key;index:knowledge_embeddings_model_id_idx;comment:生成该向量的模型 ID，指向 embedding_models.id；仍被引用的模型不允许删除" json:"model_id"`             // 生成该向量的 embedding 模型 ID
	Dimensions  int32     `gorm:"column:dimensions;not null;check:knowledge_embeddings_dimensions_check,dimensions > 0;comment:本向量的实际维度，必须等于模型登记的输出维度" json:"dimensions"`                                                                      // 此向量的实际维度，必须与模型及向量值一致
	Embedding   string    `gorm:"column:embedding;type:vector;not null;check:knowledge_embeddings_vector_dimensions_check,vector_dims(embedding) = dimensions;comment:pgvector 格式的向量值，如 [0.1,0.2]；长度由 CHECK 校验等于 dimensions" json:"embedding"` // pgvector 格式的向量值，如 [0.1,0.2]
	GeneratedAt time.Time `gorm:"column:generated_at;not null;comment:调用向量服务成功生成该向量的时间，timestamptz 按 UTC 存" json:"generated_at"`                                                                                                               // 调用 embedding 服务成功生成向量的时间

	// Chunk / Model 仅供 AutoMigrate 建外键（分别 ON DELETE CASCADE / RESTRICT）。
	// 业务代码禁止给它们赋值或 Preload。
	Chunk *KnowledgeChunk `gorm:"foreignKey:ChunkID;constraint:knowledge_embeddings_chunk_id_fkey,OnDelete:CASCADE" json:"-"`
	Model *EmbeddingModel `gorm:"foreignKey:ModelID;constraint:knowledge_embeddings_model_id_fkey,OnDelete:RESTRICT" json:"-"`
}

func (KnowledgeEmbedding) TableName() string { return "knowledge_embeddings" }
