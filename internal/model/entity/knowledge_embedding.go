package entity

import "time"

// KnowledgeEmbedding 知识向量实体对应知识向量表，保存一个切片在一个向量模型下的结果。
// 同一切片可对应多个模型，以支持模型升级和效果对比；向量值使用向量扩展的文本格式，
// 例如 "[0.1,0.2]"。向量维度与实际向量长度由数据库约束校验。
type KnowledgeEmbedding struct {
	ID          uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`                // 向量记录主键
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"created_at"` // 向量记录写入时间
	ChunkID     uint64    `gorm:"column:chunk_id;not null" json:"chunk_id"`                    // 对应的知识切片 ID；删除切片时级联删除
	ModelID     uint64    `gorm:"column:model_id;not null" json:"model_id"`                    // 生成该向量的 embedding 模型 ID
	Dimensions  int32     `gorm:"column:dimensions;not null" json:"dimensions"`                // 此向量的实际维度，必须与模型及向量值一致
	Embedding   string    `gorm:"column:embedding;type:vector;not null" json:"embedding"`      // pgvector 格式的向量值，如 [0.1,0.2]
	GeneratedAt time.Time `gorm:"column:generated_at;not null" json:"generated_at"`            // 调用 embedding 服务成功生成向量的时间
}

func (KnowledgeEmbedding) TableName() string { return "knowledge_embeddings" }
