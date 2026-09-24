package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

// ErrConversationNotActive 表示目标对话已结束，不能再往里追加内容。
//
// 单独定一个哨兵错误，而不是让调用方去比对字符串或猜 gorm 的错误类型：
// 上层要能区分"对话不存在"（找不到记录，多半是参数错）和"对话已结束"（状态问题，
// 该提示用户开新话题），这两者的处理方式完全不同。
var ErrConversationNotActive = errors.New("对话已结束")

type conversationRepository struct {
	db *gorm.DB
}

// NewConversationRepository 创建课堂对话仓储。
func NewConversationRepository(db *gorm.DB) ConversationRepository {
	return &conversationRepository{db: db}
}

func (r *conversationRepository) Create(ctx context.Context, conversation *entity.ClassroomConversation) error {
	return conn(ctx, r.db).Create(conversation).Error
}

func (r *conversationRepository) FindByID(ctx context.Context, id uint64) (*entity.ClassroomConversation, error) {
	var conversation entity.ClassroomConversation
	if err := conn(ctx, r.db).First(&conversation, id).Error; err != nil {
		return nil, err
	}
	return &conversation, nil
}

func (r *conversationRepository) ListByClassroom(ctx context.Context, classroomID uint64, limit int) ([]entity.ClassroomConversation, error) {
	var conversations []entity.ClassroomConversation
	err := conn(ctx, r.db).
		Where("classroom_id = ?", classroomID).
		Order("id DESC").
		Limit(normalizeLimit(limit)).
		Find(&conversations).Error
	return conversations, err
}

// TouchLastMessage 只更新时间列，不整行回写。
// 用 Update 而不是 Save：Save 会把内存里那份可能已经过期的整行数据覆盖回数据库
// （比如 status 刚被别的请求关掉），而这里只关心"最近一条消息的时间"这一个事实。
func (r *conversationRepository) TouchLastMessage(ctx context.Context, id uint64, at time.Time) error {
	return conn(ctx, r.db).
		Model(&entity.ClassroomConversation{}).
		Where("id = ?", id).
		Update("last_message_at", at).Error
}

func (r *conversationRepository) Close(ctx context.Context, id uint64, endedAt time.Time) error {
	return conn(ctx, r.db).
		Model(&entity.ClassroomConversation{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":   entity.ConversationStatusClosed,
			"ended_at": endedAt,
		}).Error
}

// lockActiveConversation 锁住对话行，并确认它还在进行中。
//
// 这是"同一条对话里的序号只能由数据库串行发放"的实现基础，被消息、运行与事件仓储共用：
//
//   - SELECT ... FOR UPDATE 之后，同一对话上的并发写入会排队，后到的会在前一个事务
//     提交后读到最新值。没有这把锁，两个 goroutine 会同时算出同一个 MAX+1，
//     再被 UNIQUE (conversation_id, sequence_no) 拒掉一个 —— 结果是"用户偶发地发不出消息"，
//     数据不错乱，但同样不可接受。
//   - 行锁只在事务内有效，语句一结束（自动提交）就释放。所以取号必须和插入在同一个事务里，
//     调用方必须经由 TransactionManager.Run 进来；事务外调用不会报错，只是没有保护。
//
// 顺带校验状态：对话已 closed 时返回 ErrConversationNotActive，让调用方拿到明确的拒绝原因，
// 而不是把消息插进一个已经结束的话题。
func lockActiveConversation(tx *gorm.DB, id uint64) error {
	var conversation entity.ClassroomConversation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "status").
		First(&conversation, id).Error; err != nil {
		return err
	}
	if conversation.Status != entity.ConversationStatusActive {
		return ErrConversationNotActive
	}
	return nil
}

// normalizeLimit 兜住 limit 的非法取值。
//
// limit <= 0 在 GORM 里等于"不加 LIMIT"，也就是把整张表读进内存 —— 这是分页接口上
// 最容易放大的写法，所以统一在这里换成一个默认条数，并给一个上限。
func normalizeLimit(limit int) int {
	const (
		defaultLimit = 50
		maxLimit     = 500
	)
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	default:
		return limit
	}
}
