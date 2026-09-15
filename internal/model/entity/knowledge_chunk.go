package entity

import "encoding/json"

// 知识切片实体对应知识切片表，是文章向量化和检索返回的最小文本单位。
//
// 所属文章标识指向原文章；切片序号从 0 开始，保证重组引用内容时仍保持原文顺序。
// 字符数和词元数用于控制上下文窗口及监测切分质量，扩展信息可保存章节、标签等筛选条件。
type KnowledgeChunk struct {
	BaseModel

	DocumentID     uint64          `gorm:"column:document_id;not null" json:"document_id"`         // 所属原文章 ID；删除文章时级联删除
	ChunkIndex     int32           `gorm:"column:chunk_index;not null" json:"chunk_index"`         // 在原文章中的顺序，从 0 开始
	Heading        *string         `gorm:"column:heading;type:varchar(300)" json:"heading"`        // 切片所在章节标题；没有章节时为空
	Content        string          `gorm:"column:content;type:text;not null" json:"content"`       // 实际参与向量化和检索的切片文本
	CharacterCount int32           `gorm:"column:character_count;not null" json:"character_count"` // 切片的字符数，用于切片质量和上下文长度控制
	TokenCount     *int32          `gorm:"column:token_count" json:"token_count"`                  // 可选的模型 token 数，用于精确计算提示词预算
	Metadata       json.RawMessage `gorm:"column:metadata;type:jsonb;not null" json:"metadata"`    // 扩展筛选信息，如章节路径、标签和关键词
}

func (KnowledgeChunk) TableName() string { return "knowledge_chunks" }
