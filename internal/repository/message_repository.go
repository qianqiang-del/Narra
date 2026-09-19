package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type messageRepository struct {
	db *gorm.DB
}

// NewMessageRepository 创建对话消息仓储。
func NewMessageRepository(db *gorm.DB) MessageRepository {
	return &messageRepository{db: db}
}

// AppendNext 追加消息并分配对话内序号。
//
// 序号不能用进程内计数器、也不能"先查最大值再加一"：同一条对话可能被多个 goroutine
// 同时写（用户在等回复时又发了一条、或多个 Agent 并发产出消息），两边会算出同一个
// 下一号，插入时被 UNIQUE (conversation_id, sequence_no) 拒绝 —— 表现为偶发失败。
// 这里改成先锁对话行再取号，把取号和插入放进同一个事务，让数据库替我们把并发排成队。
//
// 顺序是"锁 → 取号 → 插入"，三步都在调用方的事务里（由 TransactionManager.Run 提供）。
func (r *messageRepository) AppendNext(ctx context.Context, message *entity.ConversationMessage) error {
	db := conn(ctx, r.db)

	if err := lockActiveConversation(db, message.ConversationID); err != nil {
		return err
	}

	next, err := nextSequenceNo(db, message.ConversationID)
	if err != nil {
		return err
	}
	message.SequenceNo = next

	return db.Create(message).Error
}

// nextSequenceNo 取该对话的下一个消息序号。
//
// 用 COALESCE(MAX(sequence_no), 0) + 1，而不是 COUNT(*) + 1：消息是可能被单独删掉的
// （agent_turns.output_message_id 是 ON DELETE SET NULL，允许消息先消失），
// COUNT 会因此算出一个已经用过的号，插入直接撞唯一约束。
func nextSequenceNo(tx *gorm.DB, conversationID uint64) (int64, error) {
	var current int64
	err := tx.Model(&entity.ConversationMessage{}).
		Where("conversation_id = ?", conversationID).
		Select("COALESCE(MAX(sequence_no), 0)").
		Scan(&current).Error
	if err != nil {
		return 0, err
	}
	return current + 1, nil
}

// AppendContent 把增量正文拼到已有内容后面。
//
// 用 SQL 的字符串拼接，而不是"读出来、在 Go 里拼、整列写回"：后者在流式场景下每次都要
// 把整段正文重新传一遍，消息越长开销越大；更重要的是，两次写入交错时后一次会覆盖前一次。
// 设计文档约定 SSE 侧每约 100 毫秒合并一批增量，所以这里不需要按 token 调用。
func (r *messageRepository) AppendContent(ctx context.Context, id uint64, delta string) error {
	if delta == "" {
		return nil
	}
	return conn(ctx, r.db).
		Model(&entity.ConversationMessage{}).
		Where("id = ?", id).
		Update("content", gorm.Expr("content || ?", delta)).Error
}

// Finish 写入消息的最终状态与 token 数。
//
// 两列一起写：只改状态会把"这条消息花了多少 token"留在默认值 0 上，而上下文预算靠它统计。
// content 不在这里写 —— 它由 AppendContent 累积，收尾时如果拿内存里那份副本覆盖，
// 会把最后一批还没落库的增量丢掉。
func (r *messageRepository) Finish(ctx context.Context, id uint64, status string, tokenCount int32) error {
	return conn(ctx, r.db).
		Model(&entity.ConversationMessage{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      status,
			"token_count": tokenCount,
		}).Error
}

func (r *messageRepository) FindByID(ctx context.Context, id uint64) (*entity.ConversationMessage, error) {
	var message entity.ConversationMessage
	if err := conn(ctx, r.db).First(&message, id).Error; err != nil {
		return nil, err
	}
	return &message, nil
}

func (r *messageRepository) ListByConversation(ctx context.Context, conversationID uint64, afterSequence int64, limit int) ([]entity.ConversationMessage, error) {
	var messages []entity.ConversationMessage
	err := conn(ctx, r.db).
		Where("conversation_id = ? AND sequence_no > ?", conversationID, afterSequence).
		Order("sequence_no ASC").
		Limit(normalizeLimit(limit)).
		Find(&messages).Error
	return messages, err
}

// ListRecentByConversation 取该对话最新的 limit 条消息，返回时按序号升序。
//
// 用"倒序取 N 条再翻正"而不是"先数总数、再从头取后面那段"：消息可能被单独删除，
// 序号会有空洞，"总数 - N"算出来的起点会偏，取到的条数也不对。
//
// 交给调用方之前必须翻回正序：这段数据的用途是喂给模型当对话上下文，
// 顺序反了模型看到的对话是倒着发生的。
func (r *messageRepository) ListRecentByConversation(ctx context.Context, conversationID uint64, limit int) ([]entity.ConversationMessage, error) {
	var messages []entity.ConversationMessage
	err := conn(ctx, r.db).
		Where("conversation_id = ?", conversationID).
		Order("sequence_no DESC").
		Limit(normalizeLimit(limit)).
		Find(&messages).Error
	if err != nil {
		return nil, err
	}

	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, nil
}

func (r *messageRepository) CountByConversation(ctx context.Context, conversationID uint64) (int64, error) {
	var count int64
	err := conn(ctx, r.db).
		Model(&entity.ConversationMessage{}).
		Where("conversation_id = ?", conversationID).
		Count(&count).Error
	return count, err
}
