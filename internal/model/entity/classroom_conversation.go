package entity

import "time"

const (
	ConversationTypeQA         = "qa"
	ConversationTypeDiscussion = "discussion"
	ConversationTypeLecture    = "lecture"

	ConversationStatusActive = "active"
	ConversationStatusClosed = "closed"
)

// ClassroomConversation 是课堂内一个独立的话题或问答会话。
type ClassroomConversation struct {
	BaseModel

	ClassroomID   uint64     `gorm:"column:classroom_id;not null" json:"classroom_id"`
	OriginSceneID *uint64    `gorm:"column:origin_scene_id" json:"origin_scene_id"`
	Title         string     `gorm:"column:title;type:varchar(200);not null" json:"title"`
	Type          string     `gorm:"column:type;type:varchar(32);not null" json:"type"`
	Status        string     `gorm:"column:status;type:varchar(32);not null;default:active" json:"status"`
	LastMessageAt *time.Time `gorm:"column:last_message_at" json:"last_message_at"`
	EndedAt       *time.Time `gorm:"column:ended_at" json:"ended_at"`
}

func (ClassroomConversation) TableName() string { return "classroom_conversations" }
