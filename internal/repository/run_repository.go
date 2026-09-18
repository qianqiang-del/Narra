package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type runRepository struct {
	db *gorm.DB
}

// NewRunRepository 创建编排运行仓储。
func NewRunRepository(db *gorm.DB) RunRepository {
	return &runRepository{db: db}
}

// CreateNextAttempt 建一次运行并分配 attempt_no。
//
// attempt_no 的语义是"同一条用户消息的第几次执行"。重试不覆盖旧记录，而是新增一行，
// 所以每次都取 UNIQUE (trigger_message_id, attempt_no) 的下一个空位 ——
// 这样"第一次跑失败了，第二次换了策略跑通了"能同时留在库里，而不是把失败痕迹抹掉。
//
// 取号前同样锁住对话行。正常情况下这次运行和触发它的消息写在同一个事务里，锁已经由
// 消息那边拿过，重复加锁不额外收费；如果调用方只建运行、不写消息，这一步就顺带补上了保护，
// 让同一对话上的并发建运行排队，而不是两边都算出 attempt_no = 1。
func (r *runRepository) CreateNextAttempt(ctx context.Context, run *entity.OrchestrationRun) error {
	db := conn(ctx, r.db)

	if err := lockActiveConversation(db, run.ConversationID); err != nil {
		return err
	}

	next, err := nextAttemptNo(db, run.TriggerMessageID)
	if err != nil {
		return err
	}
	run.AttemptNo = next

	return db.Create(run).Error
}

// nextAttemptNo 取该触发消息的下一次执行序号。
func nextAttemptNo(tx *gorm.DB, triggerMessageID uint64) (int16, error) {
	var current int64
	err := tx.Model(&entity.OrchestrationRun{}).
		Where("trigger_message_id = ?", triggerMessageID).
		Select("COALESCE(MAX(attempt_no), 0)").
		Scan(&current).Error
	if err != nil {
		return 0, err
	}
	return int16(current + 1), nil
}

// MarkRunning 记录运行真正开始执行的时间点。
//
// 与 created_at 分开：create 只说明"用户说了这句话"，started_at 才说明"系统什么时候
// 真正开始为它干活"。中间可能排队等模型、等上一个运行结束，两者差值就是用户感知的等待。
func (r *runRepository) MarkRunning(ctx context.Context, id uint64, startedAt time.Time) error {
	return conn(ctx, r.db).
		Model(&entity.OrchestrationRun{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     entity.RunStatusRunning,
			"started_at": startedAt,
		}).Error
}

func (r *runRepository) Finish(ctx context.Context, id uint64, result RunResult) error {
	return conn(ctx, r.db).
		Model(&entity.OrchestrationRun{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        result.Status,
			"stop_reason":   result.StopReason,
			"error_message": result.ErrorMessage,
			"finished_at":   result.FinishedAt,
		}).Error
}

func (r *runRepository) FindByID(ctx context.Context, id uint64) (*entity.OrchestrationRun, error) {
	var run entity.OrchestrationRun
	if err := conn(ctx, r.db).First(&run, id).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *runRepository) FindByTraceID(ctx context.Context, traceID string) (*entity.OrchestrationRun, error) {
	var run entity.OrchestrationRun
	if err := conn(ctx, r.db).Where("trace_id = ?", traceID).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *runRepository) ListByConversation(ctx context.Context, conversationID uint64, limit int) ([]entity.OrchestrationRun, error) {
	var runs []entity.OrchestrationRun
	err := conn(ctx, r.db).
		Where("conversation_id = ?", conversationID).
		Order("id DESC").
		Limit(normalizeLimit(limit)).
		Find(&runs).Error
	return runs, err
}
