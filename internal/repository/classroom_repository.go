package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type classroomRepository struct{ db *gorm.DB }

func NewClassroomRepository(db *gorm.DB) ClassroomRepository {
	return &classroomRepository{db: db}
}

// Create 新建一条课程记录。
func (r *classroomRepository) Create(ctx context.Context, classroom *entity.Classroom) error {
	return conn(ctx, r.db).Create(classroom).Error
}

// FindByID 按 ID 读取课程，取不到返回错误。
func (r *classroomRepository) FindByID(ctx context.Context, id uint64) (*entity.Classroom, error) {
	var classroom entity.Classroom
	if err := r.db.WithContext(ctx).First(&classroom, id).Error; err != nil {
		return nil, err
	}
	return &classroom, nil
}

// UpdateTitle 更新课程标题。
func (r *classroomRepository) UpdateTitle(ctx context.Context, id uint64, title string) error {
	return r.db.WithContext(ctx).
		Model(&entity.Classroom{}).
		Where("id = ?", id).
		Update("title", title).
		Error
}

// UpdateStatus 更新课程状态与生成失败原因。
func (r *classroomRepository) UpdateStatus(ctx context.Context, id uint64, status string, generationError *string) error {
	return r.db.WithContext(ctx).
		Model(&entity.Classroom{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "generation_error": generationError}).
		Error
}

// ListGeneratingIDs 列出所有正在生成的课程 ID。
func (r *classroomRepository) ListGeneratingIDs(ctx context.Context) ([]uint64, error) {
	var ids []uint64
	err := r.db.WithContext(ctx).
		Model(&entity.Classroom{}).
		Where("status = ?", entity.ClassroomStatusGenerating).
		Pluck("id", &ids).
		Error
	return ids, err
}
