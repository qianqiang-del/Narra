package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type contextCompactionRepository struct {
	db *gorm.DB
}

// NewContextCompactionRepository 创建摘要仓储。
func NewContextCompactionRepository(db *gorm.DB) ContextCompactionRepository {
	return &contextCompactionRepository{db: db}
}

// LatestByConversation 取覆盖得最远的那一版摘要；没有就返回 (nil, nil)。
//
// 排序键用 covered_to_sequence 而不是 created_at：摘要被重新生成时 created_at 会更新，
// 而"覆盖到第几条消息"才是版本新旧的事实依据。它和 conversation_id 上还有唯一约束，
// 所以按它倒序取第一条就是最新版，不必再拿时间做二次比较。
func (r *contextCompactionRepository) LatestByConversation(ctx context.Context, conversationID uint64) (*entity.ContextCompaction, error) {
	var compaction entity.ContextCompaction
	err := conn(ctx, r.db).
		Where("conversation_id = ?", conversationID).
		Order("covered_to_sequence DESC").
		First(&compaction).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// "这个会话还没有任何摘要"是正常状态，不是错误 —— 见接口注释。
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &compaction, nil
}

// Create 存一版新摘要，落库后回填 ID。
func (r *contextCompactionRepository) Create(ctx context.Context, compaction *entity.ContextCompaction) error {
	return conn(ctx, r.db).Create(compaction).Error
}
