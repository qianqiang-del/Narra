package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type folderRepository struct{ db *gorm.DB }

func NewFolderRepository(db *gorm.DB) FolderRepository { return &folderRepository{db: db} }

func (r *folderRepository) Create(ctx context.Context, folder *entity.Folder) error {
	return conn(ctx, r.db).Create(folder).Error
}

func (r *folderRepository) List(ctx context.Context) ([]entity.Folder, error) {
	folders := make([]entity.Folder, 0)
	err := conn(ctx, r.db).Order("created_at DESC, id DESC").Find(&folders).Error
	return folders, err
}

func (r *folderRepository) FindByID(ctx context.Context, id uint64) (*entity.Folder, error) {
	var folder entity.Folder
	if err := conn(ctx, r.db).First(&folder, id).Error; err != nil {
		return nil, err
	}
	return &folder, nil
}

func (r *folderRepository) UpdateName(ctx context.Context, id uint64, name string) error {
	result := conn(ctx, r.db).Model(&entity.Folder{}).Where("id = ?", id).Update("name", name)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *folderRepository) Delete(ctx context.Context, id uint64) error {
	result := conn(ctx, r.db).Where("id = ?", id).Delete(&entity.Folder{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *folderRepository) CountClassrooms(ctx context.Context) (map[uint64]int64, error) {
	var rows []struct {
		FolderID uint64
		Count    int64
	}
	err := conn(ctx, r.db).Model(&entity.Classroom{}).
		Select("folder_id, COUNT(*) AS count").
		Where("folder_id IS NOT NULL").
		Group("folder_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[uint64]int64, len(rows))
	for _, row := range rows {
		counts[row.FolderID] = row.Count
	}
	return counts, nil
}

func (r *folderRepository) ListClassrooms(ctx context.Context, folderID uint64) ([]entity.Classroom, error) {
	classrooms := make([]entity.Classroom, 0)
	err := conn(ctx, r.db).Model(&entity.Classroom{}).
		Select("id, title, mode, status, created_at, updated_at").
		Where("folder_id = ?", folderID).
		Order("updated_at DESC, id DESC").Find(&classrooms).Error
	return classrooms, err
}

func (r *folderRepository) AssignClassroom(ctx context.Context, classroomID, folderID uint64) error {
	result := conn(ctx, r.db).Model(&entity.Classroom{}).
		Where("id = ?", classroomID).Update("folder_id", folderID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *folderRepository) RemoveClassroom(ctx context.Context, folderID, classroomID uint64) error {
	result := conn(ctx, r.db).Model(&entity.Classroom{}).
		Where("id = ? AND folder_id = ?", classroomID, folderID).
		Update("folder_id", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *folderRepository) DetachClassrooms(ctx context.Context, folderID uint64) error {
	return conn(ctx, r.db).Model(&entity.Classroom{}).
		Where("folder_id = ?", folderID).Update("folder_id", nil).Error
}
