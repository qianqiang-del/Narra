package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type conversationEventRepository struct {
	db *gorm.DB
}

// NewConversationEventRepository 创建对话事件仓储。
func NewConversationEventRepository(db *gorm.DB) ConversationEventRepository {
	return &conversationEventRepository{db: db}
}

// AppendNext 追加事件并分配对话内序号。
//
// 与消息的 AppendNext 同一套做法（锁对话行 → 取号 → 插入，三步都在调用方事务里）：
// 事件天然会被多个 goroutine 同时写（多个 Agent 并发产出、加上 100ms 合批的正文增量），
// "先查 MAX 再 +1" 会让两边算出同一个号、被 UNIQUE (conversation_id, sequence_no) 拒掉 ——
// 表现为流上偶发丢事件，而丢的恰恰是"过程"。
func (r *conversationEventRepository) AppendNext(ctx context.Context, event *entity.ConversationEvent) error {
	db := conn(ctx, r.db)

	if err := lockActiveConversation(db, event.ConversationID); err != nil {
		return err
	}

	next, err := nextEventSequenceNo(db, event.ConversationID)
	if err != nil {
		return err
	}
	event.SequenceNo = next

	return db.Create(event).Error
}

// nextEventSequenceNo 取该对话的下一个事件序号。
//
// 用 COALESCE(MAX(sequence_no), 0) + 1，而不是 COUNT(*) + 1：事件有 7 天到期清理，
// 删过之后 COUNT 会算出已经用过的号，插入直接撞唯一约束。
func nextEventSequenceNo(tx *gorm.DB, conversationID uint64) (int64, error) {
	var current int64
	err := tx.Model(&entity.ConversationEvent{}).
		Where("conversation_id = ?", conversationID).
		Select("COALESCE(MAX(sequence_no), 0)").
		Scan(&current).Error
	if err != nil {
		return 0, err
	}
	return current + 1, nil
}

// ListAfter 取序号大于 after 的事件，走 (conversation_id, sequence_no) 唯一索引。
func (r *conversationEventRepository) ListAfter(ctx context.Context, conversationID uint64, after int64, limit int) ([]entity.ConversationEvent, error) {
	var events []entity.ConversationEvent
	err := conn(ctx, r.db).
		Where("conversation_id = ? AND sequence_no > ?", conversationID, after).
		Order("sequence_no ASC").
		Limit(normalizeLimit(limit)).
		Find(&events).Error
	return events, err
}

// DeleteExpired 删除到期事件，返回删除条数。
//
// 一条 DELETE 走 idx_conversation_events_expires_at：到期清理按天计，不需要分批；
// 单轮超时由调用方（retention.Cleaner）控制。
func (r *conversationEventRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	result := conn(ctx, r.db).
		Where("expires_at < ?", before).
		Delete(&entity.ConversationEvent{})
	return result.RowsAffected, result.Error
}
