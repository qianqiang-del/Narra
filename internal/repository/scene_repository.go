package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

// ErrLeaseLost 表示这一页的租约已经不在本次执行者手里：要么被别的执行者接管，要么页面已被删除。
var ErrLeaseLost = errors.New("这一页的租约已失效")

type sceneRepository struct{ db *gorm.DB }

func NewSceneRepository(db *gorm.DB) SceneRepository {
	return &sceneRepository{db: db}
}

func (r *sceneRepository) CreateBatch(ctx context.Context, scenes []*entity.Scene) error {
	if len(scenes) == 0 {
		return nil
	}
	return conn(ctx, r.db).CreateInBatches(scenes, len(scenes)).Error
}

func (r *sceneRepository) FindByID(ctx context.Context, id uint64) (*entity.Scene, error) {
	var scene entity.Scene
	if err := r.db.WithContext(ctx).First(&scene, id).Error; err != nil {
		return nil, err
	}
	return &scene, nil
}

// DeleteByClassroom 删掉某课程的全部场景，讲解段落随外键级联删除。
func (r *sceneRepository) DeleteByClassroom(ctx context.Context, classroomID uint64) error {
	return conn(ctx, r.db).
		Where("classroom_id = ?", classroomID).
		Delete(&entity.Scene{}).
		Error
}

func (r *sceneRepository) ListByClassroom(ctx context.Context, classroomID uint64) ([]entity.Scene, error) {
	var scenes []entity.Scene
	err := r.db.WithContext(ctx).
		Where("classroom_id = ?", classroomID).
		Order("sort_order ASC").
		Find(&scenes).Error
	return scenes, err
}

// UpdateContent 写内容列、审核结论列与交互 HTML 列。json.RawMessage 是 []byte，直接当参数会被当成 bytea，
// 所以转成字符串交给 PostgreSQL 按目标列类型解析。interactiveHTML 只有交互页非空。
func (r *sceneRepository) UpdateContent(ctx context.Context, id uint64, owner string, content, review json.RawMessage, interactiveHTML string) error {
	return r.guardedUpdate(ctx, id, owner, map[string]any{
		"content":          string(content),
		"review":           string(review),
		"interactive_html": interactiveHTML,
	})
}

// UpdatePhase 只更新进度标记，不碰状态与内容。
func (r *sceneRepository) UpdatePhase(ctx context.Context, id uint64, owner string, phase string) error {
	return r.guardedUpdate(ctx, id, owner, map[string]any{"phase": phase})
}

func (r *sceneRepository) UpdateStatus(ctx context.Context, id uint64, owner string, status string, errorMessage *string) error {
	return r.guardedUpdate(ctx, id, owner, map[string]any{"status": status, "error_message": errorMessage})
}

// guardedUpdate 带租约校验地更新一行；租约不在本执行者手里时影响 0 行，返回 ErrLeaseLost。
func (r *sceneRepository) guardedUpdate(ctx context.Context, id uint64, owner string, values map[string]any) error {
	result := conn(ctx, r.db).
		Model(&entity.Scene{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// AcquireLease 抢这一页的租约：没人持有、租约已过期，或租约属于本轮运行的另一次尝试，才算抢到。
//
// 「属于本轮运行的另一次尝试」这条是给崩溃恢复留的：同一个 run 里同一页只会被调度一次，
// 所以带着同一个 run_id 却换了执行者，只能是上一次尝试已经死了（进程崩溃、任务超时被杀），
// 这时不必干等租约过期，直接接管。接管是安全的——旧执行者手里的 owner 已经不同，
// 它之后的每一次写库都会影响 0 行而自行停手。
func (r *sceneRepository) AcquireLease(ctx context.Context, id uint64, owner, runID string, ttl time.Duration) (bool, error) {
	now := time.Now()
	conditions := []string{"lease_owner IS NULL", "lease_expires_at IS NULL", "lease_expires_at < ?"}
	args := []any{id, now}
	if runID != "" {
		conditions = append(conditions, "run_id = ?")
		args = append(args, runID)
	}
	result := conn(ctx, r.db).
		Model(&entity.Scene{}).
		Where("id = ? AND ("+strings.Join(conditions, " OR ")+")", args...).
		Updates(map[string]any{
			"lease_owner":      owner,
			"lease_expires_at": now.Add(ttl),
			"run_id":           runID,
			"attempt":          gorm.Expr("attempt + 1"),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// RenewLease 延长租约；租约已不属于 owner 时返回 false。
func (r *sceneRepository) RenewLease(ctx context.Context, id uint64, owner string, ttl time.Duration) (bool, error) {
	result := conn(ctx, r.db).
		Model(&entity.Scene{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Update("lease_expires_at", time.Now().Add(ttl))
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ReleaseLease 主动释放租约，让重投的执行者立刻能接管。
func (r *sceneRepository) ReleaseLease(ctx context.Context, id uint64, owner string) error {
	return conn(ctx, r.db).
		Model(&entity.Scene{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Updates(map[string]any{"lease_owner": nil, "lease_expires_at": nil}).
		Error
}
