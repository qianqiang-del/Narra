package entity

import "encoding/json"

// ContextCompaction 保存一版可重新加入模型上下文的历史摘要。
type ContextCompaction struct {
	BaseModel

	ConversationID       uint64          `gorm:"column:conversation_id;not null" json:"conversation_id"`
	PreviousCompactionID *uint64         `gorm:"column:previous_compaction_id" json:"previous_compaction_id"`
	CoveredFromSequence  int64           `gorm:"column:covered_from_sequence;not null" json:"covered_from_sequence"`
	CoveredToSequence    int64           `gorm:"column:covered_to_sequence;not null" json:"covered_to_sequence"`
	Summary              string          `gorm:"column:summary;type:text;not null" json:"summary"`
	KeyPoints            json.RawMessage `gorm:"column:key_points;type:jsonb;not null;default:'{}'" json:"key_points"`
	SourceTokens         int32           `gorm:"column:source_tokens;not null" json:"source_tokens"`
	SummaryTokens        int32           `gorm:"column:summary_tokens;not null" json:"summary_tokens"`
	Model                *string         `gorm:"column:model;type:varchar(120)" json:"model"`
}

func (ContextCompaction) TableName() string { return "context_compactions" }
