package repository

import (
	"context"

	"gorm.io/gorm"

	"narra/internal/model/entity"
)

type classroomAgentRepository struct{ db *gorm.DB }

func NewClassroomAgentRepository(db *gorm.DB) ClassroomAgentRepository {
	return &classroomAgentRepository{db: db}
}

// CreateBatch 批量写入角色快照；空切片直接返回。
func (r *classroomAgentRepository) CreateBatch(ctx context.Context, agents []entity.ClassroomAgent) error {
	if len(agents) == 0 {
		return nil
	}
	return conn(ctx, r.db).Create(&agents).Error
}

// ListByClassroom 查某堂课的角色快照。
func (r *classroomAgentRepository) ListByClassroom(ctx context.Context, classroomID uint64) ([]entity.ClassroomAgent, error) {
	var agents []entity.ClassroomAgent
	err := conn(ctx, r.db).
		Where("classroom_id = ?", classroomID).
		Find(&agents).
		Error
	if err != nil {
		return nil, err
	}
	return agents, nil
}
