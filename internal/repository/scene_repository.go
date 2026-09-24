package repository

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type sceneRepository struct{ db *gorm.DB }

func NewSceneRepository(db *gorm.DB) SceneRepository {
	return &sceneRepository{db: db}
}

func (r *sceneRepository) CreateBatch(ctx context.Context, scenes []*entity.Scene) error {
	if len(scenes) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(scenes, len(scenes)).Error
}

// DeleteByClassroom 删掉某课程的全部场景，讲解段落随外键级联删除。
func (r *sceneRepository) DeleteByClassroom(ctx context.Context, classroomID uint64) error {
	return r.db.WithContext(ctx).
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

// UpdateContent 写 jsonb 内容列。json.RawMessage 是 []byte，直接当参数会被当成 bytea，
// 所以转成字符串交给 PostgreSQL 按目标列类型解析。
func (r *sceneRepository) UpdateContent(ctx context.Context, id uint64, content json.RawMessage) error {
	return conn(ctx, r.db).
		Model(&entity.Scene{}).
		Where("id = ?", id).
		Update("content", string(content)).
		Error
}

func (r *sceneRepository) UpdateStatus(ctx context.Context, id uint64, status string, errorMessage *string) error {
	return conn(ctx, r.db).
		Model(&entity.Scene{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "error_message": errorMessage}).
		Error
}
