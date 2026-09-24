package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type conversationEventRepository struct {
	db *gorm.DB
}

// NewConversationEventRepository 创建事件仓储。
func NewConversationEventRepository(db *gorm.DB) ConversationEventRepository {
	return &conversationEventRepository{db: db}
}

// AppendNext 追加一条事件并分配会话内序号。
//
// 三步和消息完全一样：锁对话行 → 取号 → 插入，都走在调用方的事务里。复用同一个
// lockActiveConversation，为的是两件事保持一致：
//
//   - **并发**：同一会话的多个 goroutine 排队取号，不会算出同一个号。
//     撞上 UNIQUE (conversation_id, sequence_no) 的表现是"偶发断流"，最难查。
//   - **已结束的会话会被拒绝**：事件和消息写在**同一笔事务**里，
//     只放行其中一个，就会给前端推出一条"有事件、没消息"的假象。
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

// nextEventSequenceNo 取该会话的下一个事件序号。
//
// 用 COALESCE(MAX(sequence_no), 0) + 1 而不是 COUNT(*) + 1：事件会被过期清理
// **从最老的开始**整批删掉，删完之后 COUNT 会从 1 重新数，算出一个已经用过的号 ——
// 一插就撞唯一约束（而且是"跑了 7 天之后才开始报错"那种）。MAX 只受现存事件影响，
// 而清理删的永远是最老那些，所以 MAX + 1 始终是个空号。
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

// ListByConversation 按序号升序读事件。
func (r *conversationEventRepository) ListByConversation(ctx context.Context, conversationID uint64, afterSequence int64, limit int) ([]entity.ConversationEvent, error) {
	var events []entity.ConversationEvent
	err := conn(ctx, r.db).
		Where("conversation_id = ? AND sequence_no > ?", conversationID, afterSequence).
		Order("sequence_no ASC").
		Limit(normalizeLimit(limit)).
		Find(&events).Error
	return events, err
}
