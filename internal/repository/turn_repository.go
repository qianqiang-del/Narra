package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

type turnRepository struct {
	db *gorm.DB
}

// NewTurnRepository 创建 Agent 回合仓储。
func NewTurnRepository(db *gorm.DB) TurnRepository {
	return &turnRepository{db: db}
}

// CreateNext 建一个回合并分配运行内序号。
//
// 这里的取号锁的是**运行行**，而不是对话行 —— 序号的作用域决定了锁的粒度：
// turn_no 在同一个运行内唯一，不同运行之间互不相干，锁运行行既够用又不会让
// 同一对话里两个并行的运行互相等待。
//
// 与消息序号同理，取号必须在事务里（由 TransactionManager.Run 提供），
// 否则 SELECT ... FOR UPDATE 在语句结束时就释放了，等于没锁。
func (r *turnRepository) CreateNext(ctx context.Context, turn *entity.AgentTurn) error {
	db := conn(ctx, r.db)

	if err := lockRun(db, turn.RunID); err != nil {
		return err
	}

	next, err := nextTurnNo(db, turn.RunID)
	if err != nil {
		return err
	}
	turn.TurnNo = next

	return db.Create(turn).Error
}

// lockRun 锁住运行行，顺带确认它存在。
//
// 运行不存在时这里会返回 gorm.ErrRecordNotFound，让上层能区分"父对象写错了"
// 和"取号失败"，而不是插进一条 turn_no 从 1 开始、却挂在不存在的运行上的孤儿回合
// （外键会拦下来，但错误信息远不如这里直接）。
func lockRun(tx *gorm.DB, id uint64) error {
	var run entity.OrchestrationRun
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").
		First(&run, id).Error
}

// nextTurnNo 取该运行的下一个回合号。
func nextTurnNo(tx *gorm.DB, runID uint64) (int16, error) {
	var current int64
	err := tx.Model(&entity.AgentTurn{}).
		Where("run_id = ?", runID).
		Select("COALESCE(MAX(turn_no), 0)").
		Scan(&current).Error
	if err != nil {
		return 0, err
	}
	return int16(current + 1), nil
}

// AttachOutputMessage 关联回合与它产出的可见消息。
//
// 这条链路是"内部执行"和"用户看到的东西"之间唯一的桥：回合记录谁被选中、为什么，
// 消息记录说了什么；output_message_id 一挂上，前端点开某条消息就能回溯到产生它的那轮执行。
// 列上有单列唯一约束，一条消息只能归属一个回合，重复挂会直接报错而不是静默覆盖。
func (r *turnRepository) AttachOutputMessage(ctx context.Context, id uint64, messageID uint64) error {
	return conn(ctx, r.db).
		Model(&entity.AgentTurn{}).
		Where("id = ?", id).
		Update("output_message_id", messageID).Error
}

func (r *turnRepository) Finish(ctx context.Context, id uint64, result TurnResult) error {
	return conn(ctx, r.db).
		Model(&entity.AgentTurn{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        result.Status,
			"next_action":   result.NextAction,
			"input_tokens":  result.InputTokens,
			"output_tokens": result.OutputTokens,
			"error_message": result.ErrorMessage,
			"finished_at":   result.FinishedAt,
		}).Error
}

func (r *turnRepository) FindByID(ctx context.Context, id uint64) (*entity.AgentTurn, error) {
	var turn entity.AgentTurn
	if err := conn(ctx, r.db).First(&turn, id).Error; err != nil {
		return nil, err
	}
	return &turn, nil
}

func (r *turnRepository) ListByRun(ctx context.Context, runID uint64) ([]entity.AgentTurn, error) {
	var turns []entity.AgentTurn
	err := conn(ctx, r.db).
		Where("run_id = ?", runID).
		Order("turn_no ASC").
		Find(&turns).Error
	return turns, err
}

func (r *turnRepository) CountByRun(ctx context.Context, runID uint64) (int64, error) {
	var count int64
	err := conn(ctx, r.db).
		Model(&entity.AgentTurn{}).
		Where("run_id = ?", runID).
		Count(&count).Error
	return count, err
}
