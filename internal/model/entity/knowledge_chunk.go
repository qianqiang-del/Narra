package entity

import "encoding/json"

// KnowledgeChunk 知识切片实体对应知识切片表，是文章向量化和检索返回的最小文本单位。
// 所属文章标识指向原文章；切片序号从 0 开始，保证重组引用内容时仍保持原文顺序。
// 字符数和词元数用于控制上下文窗口及监测切分质量，扩展信息可保存章节、标签等筛选条件。
//
// knowledge_chunks_document_id_idx 与 UNIQUE (document_id, chunk_index) 的伴生索引
// 功能重复，保留是沿用原 0002 SQL 的行为，代价只是多一份索引。
type KnowledgeChunk struct {
	BaseModel

	DocumentID     uint64          `gorm:"column:document_id;not null;uniqueIndex:knowledge_chunks_document_id_chunk_index_key;index:knowledge_chunks_document_id_idx;comment:所属文档 ID，指向 knowledge_documents.id；文档删除时本切片级联删除" json:"document_id"`                                                                // 所属原文章 ID；删除文章时级联删除
	ChunkIndex     int32           `gorm:"column:chunk_index;not null;uniqueIndex:knowledge_chunks_document_id_chunk_index_key;index:knowledge_chunks_document_id_idx;check:knowledge_chunks_chunk_index_check,chunk_index >= 0;comment:切片在原文中的顺序号，从 0 开始；与 document_id 组成唯一约束，检索后按它还原上下文顺序" json:"chunk_index"` // 在原文章中的顺序，从 0 开始
	Heading        *string         `gorm:"column:heading;type:varchar(300);comment:切片所在章节的标题（取自 Markdown 标题）；无章节归属时为空" json:"heading"`                                                                                                                                                                           // 切片所在章节标题；没有章节时为空
	Content        string          `gorm:"column:content;type:text;not null;comment:参与向量化与检索的切片正文，是检索命中的最小单位" json:"content"`                                                                                                                                                                                    // 实际参与向量化和检索的切片文本
	CharacterCount int32           `gorm:"column:character_count;not null;check:knowledge_chunks_character_count_check,character_count > 0;comment:切片字符数（按 rune 计），用于控制上下文长度、监测切分质量" json:"character_count"`                                                                                                     // 切片的字符数，用于切片质量和上下文长度控制
	TokenCount     *int32          `gorm:"column:token_count;check:knowledge_chunks_token_count_check,token_count IS NULL OR token_count > 0;comment:可选的模型 token 数，用于精确估算提示词预算" json:"token_count"`                                                                                                              // 可选的模型 token 数，用于精确计算提示词预算
	Metadata       json.RawMessage `gorm:"column:metadata;type:jsonb;not null;index:knowledge_chunks_metadata_idx,type:gin;comment:扩展筛选信息 JSON，如章节路径、标签、关键词" json:"metadata"`                                                                                                                                    // 扩展筛选信息，如章节路径、标签和关键词

	// Document 仅供 AutoMigrate 建外键 knowledge_chunks_document_id_fkey（ON DELETE CASCADE）。
	// 业务代码禁止给它赋值或 Preload。
	Document *KnowledgeDocument `gorm:"foreignKey:DocumentID;constraint:knowledge_chunks_document_id_fkey,OnDelete:CASCADE" json:"-"`
}

func (KnowledgeChunk) TableName() string { return "knowledge_chunks" }
