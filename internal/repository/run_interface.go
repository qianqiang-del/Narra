package repository

import (
	"context"
	"time"

	"narra/internal/model/entity"
)

// RunResult 是一次编排运行的收尾结果。
//
// 这几个字段放在一起更新，是因为它们描述的是同一个事实 —— 这次运行是怎么结束的。
// 拆成多次写入的话，中间失败会留下"已经完成、但没说为什么停"的记录，
// 而停止原因正是排查"为什么答偏了"的第一手线索。
type RunResult struct {
	Status       string    // 终态：completed / failed / cancelled
	StopReason   *string   // 停止原因：completed / waiting_user / max_turns / error / cancelled
	ErrorMessage *string   // 失败时的原因，成功时留空
	FinishedAt   time.Time // 结束时间
}

// RunRepository 负责编排运行（orchestration_runs）的读写。
//
// 一次运行 = 用户发一句话之后，系统内部干的一整趟活。它把"用户看到的一条消息"和
// "系统内部跑了多少回合、调用了几次模型、因为什么停下"这两件事分开记录。
type RunRepository interface {
	// CreateNextAttempt 建一次新运行，attempt_no 由本方法在事务内分配。
	// 必须在 TransactionManager.Run 内调用（通常和触发消息的写入同一个事务）。
	CreateNextAttempt(ctx context.Context, run *entity.OrchestrationRun) error

	// MarkRunning 把运行置为进行中并记录开始时间。
	MarkRunning(ctx context.Context, id uint64, startedAt time.Time) error

	// Finish 写入运行的最终状态、停止原因、错误信息和结束时间。
	Finish(ctx context.Context, id uint64, result RunResult) error

	// FindByID 按主键查运行。
	FindByID(ctx context.Context, id uint64) (*entity.OrchestrationRun, error)

	// FindByTraceID 按 trace_id 查运行，供追踪链路（agent_trace_spans）反查所属运行。
	FindByTraceID(ctx context.Context, traceID string) (*entity.OrchestrationRun, error)

	// ListByConversation 按对话列出运行，最近的排在前面。
	ListByConversation(ctx context.Context, conversationID uint64, limit int) ([]entity.OrchestrationRun, error)
}
