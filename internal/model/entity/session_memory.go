package entity

import "encoding/json"

// 记忆类型 session_memories.kind（§4.9）。
const (
	SessionMemoryKindSummary    = "summary"
	SessionMemoryKindFact       = "fact"
	SessionMemoryKindPreference = "preference"
)

// SessionMemory 会话持久化记忆，对应表 session_memories（设计文档 §4.9）。
//
// 会话上下文和摘要以 PostgreSQL 为准，Redis 只缓存最近窗口：即使 Redis 被清空或过期，
// 也可以从本表和 workbench_messages 恢复上下文（§1.1）。
type SessionMemory struct {
	BaseModel

	SessionID uint64 `gorm:"column:session_id;not null" json:"session_id"`      // 所属会话；级联删除
	Kind      string `gorm:"column:kind;type:varchar(32);not null" json:"kind"` // summary | fact | preference
	Content   string `gorm:"column:content;type:text;not null" json:"content"`  // 摘要或记忆内容

	// Metadata 重要性、来源等。
	Metadata json.RawMessage `gorm:"column:metadata;type:jsonb;not null;default:'{}'" json:"metadata"`

	// MessageUntilSequence 此摘要已覆盖到的最后消息序号，可空。
	MessageUntilSequence *int32 `gorm:"column:message_until_sequence" json:"message_until_sequence"`
}

// TableName 返回表名。
func (SessionMemory) TableName() string { return "session_memories" }
