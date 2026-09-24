package repository

import (
	"context"

	"narra/internal/model/entity"
)

type SceneSegmentRepository interface {
	CreateBatch(ctx context.Context, segments []*entity.SceneSegment) error
	DeleteByScene(ctx context.Context, sceneID uint64) error
	ListByScene(ctx context.Context, sceneID uint64) ([]entity.SceneSegment, error)
	UpdateAudio(ctx context.Context, id uint64, audioPath string, status string) error
	UpdateStatus(ctx context.Context, id uint64, status string, errorMessage *string) error
}
