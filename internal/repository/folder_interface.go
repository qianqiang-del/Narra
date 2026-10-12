package repository

import (
	"context"

	"narra/internal/model/entity"
)

type FolderRepository interface {
	Create(ctx context.Context, folder *entity.Folder) error
	List(ctx context.Context, ownerID uint64) ([]entity.Folder, error)
	FindByID(ctx context.Context, ownerID, id uint64) (*entity.Folder, error)
	UpdateName(ctx context.Context, ownerID, id uint64, name string) error
	Delete(ctx context.Context, ownerID, id uint64) error
	CountClassrooms(ctx context.Context, ownerID uint64) (map[uint64]int64, error)
	ListClassrooms(ctx context.Context, ownerID, folderID uint64) ([]entity.Classroom, error)
	AssignClassroom(ctx context.Context, ownerID, classroomID, folderID uint64) error
	RemoveClassroom(ctx context.Context, ownerID, folderID, classroomID uint64) error
	DetachClassrooms(ctx context.Context, ownerID, folderID uint64) error
}
