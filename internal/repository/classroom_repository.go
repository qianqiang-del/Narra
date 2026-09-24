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

func (r *classroomRepository) List(ctx context.Context) ([]entity.Classroom, error) {
	classrooms := make([]entity.Classroom, 0)
	err := r.db.WithContext(ctx).
		Where("status <> ?", entity.ClassroomStatusFailed).
		Order("updated_at DESC, id DESC").
		Find(&classrooms).Error
	return classrooms, err
}

func (r *classroomRepository) Delete(ctx context.Context, id uint64) error {
	result := conn(ctx, r.db).Where("id = ?", id).Delete(&entity.Classroom{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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

// ListGeneratingIDs 列出所有仍应有后台任务推进的课程 ID。
// playable 且 generation_error 为空表示大纲已完成、场景仍在生成。
func (r *classroomRepository) ListGeneratingIDs(ctx context.Context) ([]uint64, error) {
	var ids []uint64
	err := r.db.WithContext(ctx).
		Model(&entity.Classroom{}).
		Where("status = ? OR (status = ? AND generation_error IS NULL)", entity.ClassroomStatusGenerating, entity.ClassroomStatusPlayable).
		Pluck("id", &ids).
		Error
	return ids, err
}
