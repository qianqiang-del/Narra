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
//
// 约束全部由字段上的 tag 声明，AutoMigrate 建：两个 CHECK、两条外键
// （classroom_id 级联删除，origin_scene_id 删场景时置 NULL），
// 以及「按课程查最近会话」的复合索引 idx_classroom_conversations_recent。
type ClassroomConversation struct {
	BaseModel

	ClassroomID   uint64     `gorm:"column:classroom_id;not null;index:idx_classroom_conversations_recent;comment:所属课程 ID，指向 classrooms.id；课程删除时级联删除" json:"classroom_id"`
	OriginSceneID *uint64    `gorm:"column:origin_scene_id;comment:这次会话由哪一页场景发起，指向 scenes.id；场景被删除时置空" json:"origin_scene_id"`
	Title         string     `gorm:"column:title;type:varchar(200);not null;comment:会话标题" json:"title"`
	Type          string     `gorm:"column:type;type:varchar(32);not null;check:classroom_conversations_type_check,type IN ('qa', 'discussion', 'lecture');comment:会话类型，取值 qa（问答）/ discussion（讨论）/ lecture（讲授）" json:"type"`
	Status        string     `gorm:"column:status;type:varchar(32);not null;default:active;check:classroom_conversations_status_check,status IN ('active', 'closed');comment:会话状态，取值 active（进行中）/ closed（已结束）" json:"status"`
	LastMessageAt *time.Time `gorm:"column:last_message_at;index:idx_classroom_conversations_recent,sort:DESC;comment:最后一条消息的时间；列表按它倒序，为空表示还没有人说过话" json:"last_message_at"`
	EndedAt       *time.Time `gorm:"column:ended_at;comment:会话结束时间；未结束时为空" json:"ended_at"`

	// Classroom / OriginScene 仅供 AutoMigrate 建外键（分别 ON DELETE CASCADE / SET NULL）。
	// 业务代码禁止给它们赋值或 Preload。
	Classroom   *Classroom `gorm:"foreignKey:ClassroomID;constraint:classroom_conversations_classroom_id_fkey,OnDelete:CASCADE" json:"-"`
	OriginScene *Scene     `gorm:"foreignKey:OriginSceneID;constraint:classroom_conversations_origin_scene_id_fkey,OnDelete:SET NULL" json:"-"`
}

func (ClassroomConversation) TableName() string { return "classroom_conversations" }
