package entity

import "encoding/json"

// ContextCompaction 保存一版可重新加入模型上下文的历史摘要。
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：两个跨列 CHECK、
// UNIQUE (conversation_id, covered_to_sequence)、两条外键（conversation 级联删除，
// 上一版摘要置 NULL，自引用），以及「按会话取最新摘要」的复合索引。
type ContextCompaction struct {
	BaseModel

	ConversationID       uint64          `gorm:"column:conversation_id;not null;uniqueIndex:context_compactions_conversation_covered_key;index:idx_context_compactions_latest;comment:所属会话 ID，指向 classroom_conversations.id；会话删除时级联删除" json:"conversation_id"`
	PreviousCompactionID *uint64         `gorm:"column:previous_compaction_id;comment:上一版摘要的 ID，自引用；为空表示这是该会话的第一版摘要" json:"previous_compaction_id"`
	CoveredFromSequence  int64           `gorm:"column:covered_from_sequence;not null;check:context_compactions_sequence_range_check,covered_from_sequence >= 1 AND covered_to_sequence >= covered_from_sequence;comment:本摘要覆盖的消息起始序号（含），从 1 开始" json:"covered_from_sequence"`
	CoveredToSequence    int64           `gorm:"column:covered_to_sequence;not null;uniqueIndex:context_compactions_conversation_covered_key;index:idx_context_compactions_latest,sort:DESC;comment:本摘要覆盖的消息结束序号（含）；与 conversation_id 组成唯一约束，同一段不会被重复摘要" json:"covered_to_sequence"`
	Summary              string          `gorm:"column:summary;type:text;not null;comment:历史摘要正文；上下文超长时用它替代被覆盖的那段原文重新进模型" json:"summary"`
	KeyPoints            json.RawMessage `gorm:"column:key_points;type:jsonb;not null;default:'{}';comment:摘要的结构化要点 JSON" json:"key_points"`
	SourceTokens         int32           `gorm:"column:source_tokens;not null;check:context_compactions_token_count_check,source_tokens > 0 AND summary_tokens >= 0 AND summary_tokens < source_tokens;comment:被这次压缩替掉的原文 token 数，必须大于 0" json:"source_tokens"`
	SummaryTokens        int32           `gorm:"column:summary_tokens;not null;comment:摘要本身的 token 数，必须小于 source_tokens，否则这次压缩没有意义" json:"summary_tokens"`
	Model                *string         `gorm:"column:model;type:varchar(120);comment:生成这份摘要用的模型 ID" json:"model"`

	// 以下关联仅供 AutoMigrate 建外键（CASCADE / 自引用 SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Conversation       *ClassroomConversation `gorm:"foreignKey:ConversationID;constraint:context_compactions_conversation_id_fkey,OnDelete:CASCADE" json:"-"`
	PreviousCompaction *ContextCompaction     `gorm:"foreignKey:PreviousCompactionID;constraint:context_compactions_previous_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (ContextCompaction) TableName() string { return "context_compactions" }
