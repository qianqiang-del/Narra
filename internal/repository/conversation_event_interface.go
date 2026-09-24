package repository

import (
	"context"

	"narra/internal/model/entity"
)

// ConversationEventRepository 负责 SSE 事件（conversation_events）的读写。
//
// 这张表是"编排层"与"SSE 推送层"之间的唯一接口：编排把发生的事写进去，
// SSE 层按 conversation_id + sequence_no 读出来推给前端，并在重连时续上断点。
// 所以这里只开两个方法 —— 一个写、一个按序号增量读，都是这条接口的两端各自需要的。
//
// 有意**不提供** Update：事件是"当时确实发生过什么"的记录，事后再改它，
// 等于把已经推给前端的流和库里的记录对不上了。要纠正只能在后面追加新事件。
//
// 也**没有** DeleteExpired：过期清理属于第 7 步（和 trace span 一起做），
// 现在加上去只会是一个"定义了却没人调用"的方法。
type ConversationEventRepository interface {
	// AppendNext 追加一条事件，sequence_no 由本方法在事务内分配（会话内从 1 开始）。
	//
	// 必须在 TransactionManager.Run 内调用，否则行锁随语句结束即释放，序号不再安全。
	AppendNext(ctx context.Context, event *entity.ConversationEvent) error

	// ListByConversation 按会话读事件，按序号升序。
	//
	// afterSequence 传 0 表示从头读；断线重连时传**最后收到的那个序号**，
	// 拿到的就正好是漏掉的那一段（比较是严格大于，不会把已收到的那条重复发一次）。
	ListByConversation(ctx context.Context, conversationID uint64, afterSequence int64, limit int) ([]entity.ConversationEvent, error)
}
