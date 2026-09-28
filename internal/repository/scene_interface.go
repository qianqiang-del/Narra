package repository

import (
	"context"
	"encoding/json"
	"time"

	"narra/internal/model/entity"
)

type SceneRepository interface {
	FindByID(ctx context.Context, id uint64) (*entity.Scene, error)
	CreateBatch(ctx context.Context, scenes []*entity.Scene) error
	DeleteByClassroom(ctx context.Context, classroomID uint64) error
	ListByClassroom(ctx context.Context, classroomID uint64) ([]entity.Scene, error)
	UpdateContent(ctx context.Context, id uint64, owner string, content, review json.RawMessage, interactiveHTML string) error
	UpdatePhase(ctx context.Context, id uint64, owner string, phase string) error
	UpdateStatus(ctx context.Context, id uint64, owner string, status string, errorMessage *string) error
	AcquireLease(ctx context.Context, id uint64, owner, runID string, ttl time.Duration) (bool, error)
	RenewLease(ctx context.Context, id uint64, owner string, ttl time.Duration) (bool, error)
	ReleaseLease(ctx context.Context, id uint64, owner string) error
}
