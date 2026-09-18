package repository

import (
	"context"
	"time"

	"narra/internal/model/entity"
)

// TurnResult 是一次 Agent 回合的收尾结果。
//
// 与 RunResult 分开定义，而不是共用一个"通用结果"结构：两者的字段几乎没有交集
// （回合有 token 消耗和下一步动作，运行有停止原因），合成一个只会让每次调用都
// 需要判断"哪些字段这次该填"。
type TurnResult struct {
	Status       string    // 终态：completed / failed / cancelled
	NextAction   *string   // Director 的下一步动作：continue / switch_agent / ask_user / end
	InputTokens  int32     // 本次回合的输入 token
	OutputTokens int32     // 本次回合的输出 token
	ErrorMessage *string   // 失败原因，成功时留空
	FinishedAt   time.Time // 结束时间
}

// TurnRepository 负责 Agent 回合（agent_turns）的读写。
//
// 一个回合 = 某个课堂角色被 Director 选中后，真正执行的那一轮。它是"谁在什么时候
// 为什么被选中、说了什么、花了多少 token"这条链路的落点，也是 trace 里 span 的归属对象。
type TurnRepository interface {
	// CreateNext 建一个回合，turn_no 由本方法在事务内分配（运行内从 1 开始）。
	// 必须在 TransactionManager.Run 内调用。
	CreateNext(ctx context.Context, turn *entity.AgentTurn) error

	// AttachOutputMessage 把回合和它产出的可见消息关联起来。
	// 消息先插入拿到 id，再回填到这里：反过来做的话，回合会先指向一个还不存在的消息。
	AttachOutputMessage(ctx context.Context, id uint64, messageID uint64) error

	// Finish 写入回合的终态、下一步动作和 token 消耗。
	Finish(ctx context.Context, id uint64, result TurnResult) error

	// FindByID 按主键查回合。
	FindByID(ctx context.Context, id uint64) (*entity.AgentTurn, error)

	// ListByRun 按运行列出回合，按回合号升序 —— 这就是"这场讨论的发言顺序"。
	ListByRun(ctx context.Context, runID uint64) ([]entity.AgentTurn, error)

	// CountByRun 统计运行内已产生的回合数，供最大回合数判断使用。
	CountByRun(ctx context.Context, runID uint64) (int64, error)
}
