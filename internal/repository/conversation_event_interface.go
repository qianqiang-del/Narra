package repository

import (
	"context"
	"time"

	"narra/internal/model/entity"
)

// ConversationEventRepository 负责对话事件的追加与增量读取。
//
// 它是 SSE 链路的数据面：写入侧把"执行过程"（谁开始说话、正文增量、谁结束）
// 落成追加式事件，读取侧按 sequence_no 增量取，供 SSE 断线续传与重放。
//
// 序号分配与消息仓储是同一条约束：AppendNext 必须经由 TransactionManager.Run 进来，
// 取号与插入才会落在同一个事务里、受对话行锁保护（见 lockActiveConversation）。
// 事件序号只保证单调递增，允许空洞 —— 到期清理会删掉旧事件。
type ConversationEventRepository interface {
	// AppendNext 追加一条事件并分配对话内序号，成功后回填 event.SequenceNo。
	// 对话不存在时返回 gorm.ErrRecordNotFound，已结束时返回 ErrConversationNotActive。
	AppendNext(ctx context.Context, event *entity.ConversationEvent) error

	// ListAfter 取该对话中 sequence_no 大于 after 的事件，按序号升序，最多 limit 条。
	// after = 0 表示从头取（重放）；limit 会被归一化到 [1, 500]。
	ListAfter(ctx context.Context, conversationID uint64, after int64, limit int) ([]entity.ConversationEvent, error)

	// DeleteExpired 删除 expires_at 早于 before 的事件，返回删除条数。
	// 只由过期清理任务调用（见 internal/retention）：事件只保留 7 天，
	// 更早的过程以 conversation_messages 为准。
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
