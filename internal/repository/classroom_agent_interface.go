package repository

import (
	"context"

	"narra/internal/model/entity"
)

// ClassroomAgentRepository 课堂角色快照的读写。
type ClassroomAgentRepository interface {
	// CreateBatch 为一门课批量写入角色快照。
	CreateBatch(ctx context.Context, agents []entity.ClassroomAgent) error

	// ListByClassroom 返回某堂课的角色快照。
	ListByClassroom(ctx context.Context, classroomID uint64) ([]entity.ClassroomAgent, error)
}
