package repository

import (
	"context"

	"narra/internal/model/entity"
)

type ClassroomRepository interface {
	Create(ctx context.Context, classroom *entity.Classroom) error
	FindByID(ctx context.Context, id uint64) (*entity.Classroom, error)
	List(ctx context.Context) ([]entity.Classroom, error)
	Delete(ctx context.Context, id uint64) error
	UpdateTitle(ctx context.Context, id uint64, title string) error
	UpdateStatus(ctx context.Context, id uint64, status string, generationError *string) error
	ListGeneratingIDs(ctx context.Context) ([]uint64, error)
}
