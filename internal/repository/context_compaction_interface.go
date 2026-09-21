package repository

import (
	"context"

	"narra/internal/model/entity"
)

// ContextCompactionRepository 负责历史摘要（context_compactions）的读写。
//
// 这张表存的不是"消息"，而是"一段老对话被压成的一句话"：上下文超长时，
// 用摘要替代被覆盖的那段原文重新进模型。所以它的读法很固定 ——
// 永远只关心"最新一版覆盖到哪了"，既不需要按 id 查，也不需要列表与分页。
//
// 版本是累积的：新摘要吸收上一版摘要 + 之后的老消息，靠 previous_compaction_id 串成链。
// 因此这里只开两个方法，**没有通用的 Update / Delete** —— 摘要一旦生成就是那一版的证据，
// 事后改写它会让"当时模型究竟看过什么"变得不可考。
type ContextCompactionRepository interface {
	// LatestByConversation 取该会话**覆盖得最远**的那一版摘要；**没有摘要时返回 (nil, nil)**。
	//
	// 不返回 gorm.ErrRecordNotFound：调用方（上下文组装）每次都要先问一句
	// "有没有上一版摘要"，第一次当然没有 —— 那是正常流程，不是异常。
	// 让调用方为此去 errors.Is(gorm.ErrRecordNotFound)，等于把 GORM 的细节漏到业务层。
	LatestByConversation(ctx context.Context, conversationID uint64) (*entity.ContextCompaction, error)

	// Create 存一版新摘要，落库后回填 ID。
	//
	// 落库前调用方应先自行确认 summary_tokens < source_tokens。库上那条 CHECK 会拦，
	// 但它报出来的只是"某个约束被违反"，不如调用方自己判一下、再记一条
	// "这次压缩被放弃、原因是什么"的日志来得清楚。
	Create(ctx context.Context, compaction *entity.ContextCompaction) error
}
