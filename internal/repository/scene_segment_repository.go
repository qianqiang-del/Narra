package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type sceneSegmentRepository struct{ db *gorm.DB }

func NewSceneSegmentRepository(db *gorm.DB) SceneSegmentRepository {
	return &sceneSegmentRepository{db: db}
}

func (r *sceneSegmentRepository) CreateBatch(ctx context.Context, segments []*entity.SceneSegment) error {
	if len(segments) == 0 {
		return nil
	}
	return conn(ctx, r.db).CreateInBatches(segments, len(segments)).Error
}

func (r *sceneSegmentRepository) DeleteByScene(ctx context.Context, sceneID uint64) error {
	return conn(ctx, r.db).Where("scene_id = ?", sceneID).Delete(&entity.SceneSegment{}).Error
}

func (r *sceneSegmentRepository) ListByScene(ctx context.Context, sceneID uint64) ([]entity.SceneSegment, error) {
	var segments []entity.SceneSegment
	err := r.db.WithContext(ctx).
		Where("scene_id = ?", sceneID).
		Order("sort_order ASC").
		Find(&segments).Error
	return segments, err
}

func (r *sceneSegmentRepository) UpdateAudio(ctx context.Context, id uint64, audioPath string, status string) error {
	return r.db.WithContext(ctx).
		Model(&entity.SceneSegment{}).
		Where("id = ?", id).
		Updates(map[string]any{"audio_path": audioPath, "status": status}).
		Error
}

func (r *sceneSegmentRepository) UpdateStatus(ctx context.Context, id uint64, status string, errorMessage *string) error {
	return r.db.WithContext(ctx).
		Model(&entity.SceneSegment{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "error_message": errorMessage}).
		Error
}
