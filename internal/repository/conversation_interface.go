package repository

import (
	"context"
	"time"

	"narra/internal/model/entity"
)

// ConversationRepository 负责课堂对话（classroom_conversations）的读写。
//
// 接口按业务动作开方法，不做一表一套 Create/Update/Delete/List —— 每个方法都对应
// 上层真实需要做的一件事，避免出现"定义了但没人用、也没人知道该怎么用"的空方法。
type ConversationRepository interface {
	// Create 新建一个对话，成功后 conversation.ID 被填充。
	Create(ctx context.Context, conversation *entity.ClassroomConversation) error

	// FindByID 按主键查对话，查不到返回 gorm.ErrRecordNotFound。
	FindByID(ctx context.Context, id uint64) (*entity.ClassroomConversation, error)

	// ListByClassroom 按课堂列出对话，新建立的排在前面。
	ListByClassroom(ctx context.Context, classroomID uint64, limit int) ([]entity.ClassroomConversation, error)

	// TouchLastMessage 记录最近一条消息的时间，供列表排序与"有新消息"提示使用。
	TouchLastMessage(ctx context.Context, id uint64, at time.Time) error

	// Close 结束对话。已结束的对话不能再追加消息。
	Close(ctx context.Context, id uint64, endedAt time.Time) error
}
