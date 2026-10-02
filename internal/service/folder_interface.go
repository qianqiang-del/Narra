package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

type FolderService interface {
	Create(ctx context.Context, input requestdto.Folder) (*responsedto.Folder, error)
	List(ctx context.Context) ([]responsedto.Folder, error)
	Get(ctx context.Context, id uint64) (*responsedto.FolderDetail, error)
	Rename(ctx context.Context, id uint64, input requestdto.Folder) (*responsedto.Folder, error)
	Delete(ctx context.Context, id uint64) error
	AddClassroom(ctx context.Context, folderID, classroomID uint64) error
	RemoveClassroom(ctx context.Context, folderID, classroomID uint64) error
}
