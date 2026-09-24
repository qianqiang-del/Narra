package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type sharedMemoryRepository struct {
	db *gorm.DB
}

// NewSharedMemoryRepository 创建共享记忆仓储。
func NewSharedMemoryRepository(db *gorm.DB) SharedMemoryRepository {
	return &sharedMemoryRepository{db: db}
}

// ListForContext 取这次发言可以带的记忆。
//
// 三个条件缺一不可，各自对应一条业务规则：
//
//	status = 'active'  —— 被取代（superseded）/ 被撤回（retracted）的记忆已经不再成立，不能带进上下文
//	作用域匹配          —— 对话级只在本条对话里有效；课堂级在同一门课里通用（设计文档 §5.6）
//	未过期              —— expires_at 为空表示长期有效；有值时要还没到点
//
// 排序是 importance DESC, id DESC：第一个是"重要的先带"；第二个是兜底 ——
// 同样重要度的行如果不指定次序，数据库返回的顺序是不保证的，
// 会出现"同样的数据、两次带进去的却不一样"，这种不可复现的行为最难查。
func (r *sharedMemoryRepository) ListForContext(ctx context.Context, classroomID uint64, conversationID uint64, limit int) ([]entity.SharedContextMemory, error) {
	memories := make([]entity.SharedContextMemory, 0, normalizeLimit(limit))

	err := conn(ctx, r.db).
		Where("status = ?", entity.MemoryStatusActive).
		Where("((scope = ? AND conversation_id = ?) OR (scope = ? AND classroom_id = ?))",
			entity.MemoryScopeConversation, conversationID,
			entity.MemoryScopeClassroom, classroomID).
		Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC()).
		Order("importance DESC, id DESC").
		Limit(normalizeLimit(limit)).
		Find(&memories).Error
	if err != nil {
		return nil, err
	}
	return memories, nil
}

// Create 写入一条新记忆，落库后回填 ID。
func (r *sharedMemoryRepository) Create(ctx context.Context, memory *entity.SharedContextMemory) error {
	return conn(ctx, r.db).Create(memory).Error
}
