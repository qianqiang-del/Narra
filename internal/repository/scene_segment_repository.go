package repository

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"narra/internal/model/entity"
)

// segmentSortOrderShift 是替换讲解段落时把旧行顺序号挪开的偏移量。
const segmentSortOrderShift = 100000

type sceneSegmentRepository struct{ db *gorm.DB }

func NewSceneSegmentRepository(db *gorm.DB) SceneSegmentRepository {
	return &sceneSegmentRepository{db: db}
}

func (r *sceneSegmentRepository) ListByScene(ctx context.Context, sceneID uint64) ([]entity.SceneSegment, error) {
	var segments []entity.SceneSegment
	err := conn(ctx, r.db).
		Where("scene_id = ?", sceneID).
		Order("sort_order ASC").
		Find(&segments).Error
	return segments, err
}

func (r *sceneSegmentRepository) UpdateAudio(ctx context.Context, sceneID, id uint64, owner string, audioPath string, status string) error {
	return r.guardedUpdate(ctx, sceneID, id, owner, map[string]any{"audio_path": audioPath, "status": status})
}

func (r *sceneSegmentRepository) UpdateStatus(ctx context.Context, sceneID, id uint64, owner string, status string, errorMessage *string) error {
	return r.guardedUpdate(ctx, sceneID, id, owner, map[string]any{"status": status, "error_message": errorMessage})
}

// guardedUpdate 带页面租约校验地更新一段讲解；租约不在本执行者手里时影响 0 行，返回 ErrLeaseLost。
//
// 校验写成 EXISTS 子查询而不是先查后写：先查后写中间有窗口，租约恰好在这个窗口里易主就漏过去了。
// 后一个参数传空串表示不校验租约（owner=="" 时换成恒真的条件不可取，所以直接判空跳过校验）。
func (r *sceneSegmentRepository) guardedUpdate(ctx context.Context, sceneID, id uint64, owner string, values map[string]any) error {
	query := conn(ctx, r.db).
		Model(&entity.SceneSegment{}).
		Where("id = ? AND scene_id = ?", id, sceneID)
	if owner != "" {
		query = query.Where("EXISTS (SELECT 1 FROM scenes WHERE scenes.id = scene_segments.scene_id AND scenes.lease_owner = ?)", owner)
	}
	result := query.Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// ReplaceByScene 按 content_key 幂等替换某页的讲解段落。
//
// 讲稿没变、音频还在的段落沿用原行的主键与音频路径（重新生成时不重做语音），
// 讲稿变了的段落清掉音频路径交由合成阶段重做，本轮不再需要的旧段落删除。
// 顺序号先整体挪开再落位，避免撞上 (scene_id, sort_order) 唯一约束。
//
// 行是按 (scene_id, content_key) 复用的：同一个内容块在重做后仍占同一行、同一个主键，
// 所以音频文件名按主键命名不会凭空多出一堆孤儿；反过来，讲稿变了就必须把音频路径清掉，
// 否则新讲稿会配上旧录音。
func (r *sceneSegmentRepository) ReplaceByScene(ctx context.Context, sceneID uint64, segments []*entity.SceneSegment) error {
	db := conn(ctx, r.db)
	var existing []entity.SceneSegment
	if err := db.Where("scene_id = ?", sceneID).Find(&existing).Error; err != nil {
		return err
	}
	byKey := make(map[string]entity.SceneSegment, len(existing))
	for _, item := range existing {
		byKey[item.ContentKey] = item
	}
	for _, segment := range segments {
		old, ok := byKey[segment.ContentKey]
		if ok && old.Text == segment.Text && old.Status == entity.SceneSegmentStatusReady &&
			old.AudioPath != nil && strings.TrimSpace(*old.AudioPath) != "" {
			segment.Status = entity.SceneSegmentStatusReady
			segment.AudioPath = old.AudioPath
			continue
		}
		segment.AudioPath = nil
	}

	if err := db.Model(&entity.SceneSegment{}).
		Where("scene_id = ?", sceneID).
		Update("sort_order", gorm.Expr("sort_order + ?", segmentSortOrderShift)).Error; err != nil {
		return err
	}
	if len(segments) > 0 {
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "scene_id"}, {Name: "content_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"sort_order", "text", "status", "audio_path", "error_message", "updated_at"}),
		}).Create(segments).Error; err != nil {
			return err
		}
	}
	return db.Where("scene_id = ? AND sort_order >= ?", sceneID, segmentSortOrderShift).
		Delete(&entity.SceneSegment{}).
		Error
}
