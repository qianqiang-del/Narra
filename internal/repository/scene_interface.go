package repository

import (
	"context"
	"encoding/json"

	"narra/internal/model/entity"
)

type SceneRepository interface {
	CreateBatch(ctx context.Context, scenes []*entity.Scene) error
	DeleteByClassroom(ctx context.Context, classroomID uint64) error
	ListByClassroom(ctx context.Context, classroomID uint64) ([]entity.Scene, error)
	UpdateContent(ctx context.Context, id uint64, content json.RawMessage) error
	UpdateStatus(ctx context.Context, id uint64, status string, errorMessage *string) error
}
