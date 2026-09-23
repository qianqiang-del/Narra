package repository

import (
	"context"

	"narra/internal/model/entity"
)

// MessageRepository 负责对话消息（conversation_messages）的读写。
//
// 消息既是用户直接看到的内容，也是上下文压缩与 SSE 补发的数据来源。写入路径按
// "一条消息的生命周期"切开，而不是给一个通用的 Update：
//
//	AppendNext  → 占位插入，拿到 id 和序号，状态为 streaming
//	AppendContent → 流式期间分批把正文拼上去
//	Finish      → 收尾，定格状态和 token 数
//
// 这样每个方法能改写哪些列是固定的，调用方无法在流式过程中做出
// "清空正文"或"改发送者"这类破坏历史的操作。
type MessageRepository interface {
	// AppendNext 追加一条消息，sequence_no 由本方法在事务内分配（对话内从 1 开始）。
	// 必须在 TransactionManager.Run 内调用，否则行锁随语句结束即释放，序号不再安全。
	AppendNext(ctx context.Context, message *entity.ConversationMessage) error

	// AppendContent 把一段新增正文拼到消息末尾，供流式输出分批落库。
	AppendContent(ctx context.Context, id uint64, delta string) error

	// Finish 写入消息的最终状态与 token 数。
	Finish(ctx context.Context, id uint64, status string, tokenCount int32) error

	// FindByID 按主键查消息。
	FindByID(ctx context.Context, id uint64) (*entity.ConversationMessage, error)

	// ListByConversation 按对话读消息，按序号升序。
	// afterSequence 用于增量拉取（传 0 表示从头读），断线重连时传最后收到的序号即可；
	// 上下文组装也走这个方法 —— 从"上一版摘要覆盖到哪"往后取，已压过的区间不再重复取。
	ListByConversation(ctx context.Context, conversationID uint64, afterSequence int64, limit int) ([]entity.ConversationMessage, error)

	// CountByConversation 统计对话内的消息条数，供上下文预算与分页判断使用。
	CountByConversation(ctx context.Context, conversationID uint64) (int64, error)
}
