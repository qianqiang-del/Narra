package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	apperrors "narra/pkg/errors"
)

type folderService struct {
	folders repository.FolderRepository
	tx      repository.TransactionManager
}

func NewFolderService(folders repository.FolderRepository, tx repository.TransactionManager) FolderService {
	return &folderService{folders: folders, tx: tx}
}

func (s *folderService) Create(ctx context.Context, input requestdto.Folder) (*responsedto.Folder, error) {
	name, err := folderName(input.Name)
	if err != nil {
		return nil, err
	}
	folder := &entity.Folder{Name: name}
	if err := s.folders.Create(ctx, folder); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "创建文件夹失败", err)
	}
	item := folderResponse(folder, 0)
	return &item, nil
}

func (s *folderService) List(ctx context.Context) ([]responsedto.Folder, error) {
	folders, err := s.folders.List(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询文件夹失败", err)
	}
	counts, err := s.folders.CountClassrooms(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "统计文件夹课堂失败", err)
	}
	items := make([]responsedto.Folder, 0, len(folders))
	for i := range folders {
		items = append(items, folderResponse(&folders[i], counts[folders[i].ID]))
	}
	return items, nil
}

func (s *folderService) Get(ctx context.Context, id uint64) (*responsedto.FolderDetail, error) {
	folder, err := s.specificFolder(ctx, id)
	if err != nil {
		return nil, err
	}
	classrooms, err := s.folders.ListClassrooms(ctx, id)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询文件夹课堂失败", err)
	}
	items := make([]responsedto.FolderClassroom, 0, len(classrooms))
	for _, classroom := range classrooms {
		items = append(items, responsedto.FolderClassroom{
			ID: classroom.ID, Title: classroom.Title, Mode: classroom.Mode,
			Status: classroom.Status, CreatedAt: classroom.CreatedAt, UpdatedAt: classroom.UpdatedAt,
		})
	}
	return &responsedto.FolderDetail{Folder: folderResponse(folder, int64(len(items))), Classrooms: items}, nil
}

func (s *folderService) Rename(ctx context.Context, id uint64, input requestdto.Folder) (*responsedto.Folder, error) {
	name, err := folderName(input.Name)
	if err != nil {
		return nil, err
	}
	if err := s.folders.UpdateName(ctx, id, name); err != nil {
		return nil, folderWriteError(err, "重命名文件夹失败")
	}
	return s.folderWithCount(ctx, id)
}

func (s *folderService) Delete(ctx context.Context, id uint64) error {
	err := s.tx.Run(ctx, func(txCtx context.Context) error {
		if err := s.folders.DetachClassrooms(txCtx, id); err != nil {
			return err
		}
		return s.folders.Delete(txCtx, id)
	})
	if err != nil {
		return folderWriteError(err, "删除文件夹失败")
	}
	return nil
}

func (s *folderService) AddClassroom(ctx context.Context, folderID, classroomID uint64) error {
	if _, err := s.specificFolder(ctx, folderID); err != nil {
		return err
	}
	if err := s.folders.AssignClassroom(ctx, classroomID, folderID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(apperrors.CodeNotFound, "课堂不存在")
		}
		return apperrors.NewWithErr(apperrors.CodeInternalError, "加入文件夹失败", err)
	}
	return nil
}

func (s *folderService) RemoveClassroom(ctx context.Context, folderID, classroomID uint64) error {
	if err := s.folders.RemoveClassroom(ctx, folderID, classroomID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(apperrors.CodeNotFound, "课堂不在此文件夹中")
		}
		return apperrors.NewWithErr(apperrors.CodeInternalError, "移出文件夹失败", err)
	}
	return nil
}

func (s *folderService) folderWithCount(ctx context.Context, id uint64) (*responsedto.Folder, error) {
	folder, err := s.specificFolder(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := s.folders.CountClassrooms(ctx)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "统计文件夹课堂失败", err)
	}
	item := folderResponse(folder, counts[id])
	return &item, nil
}

func (s *folderService) specificFolder(ctx context.Context, id uint64) (*entity.Folder, error) {
	folder, err := s.folders.FindByID(ctx, id)
	if err == nil {
		return folder, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.New(apperrors.CodeNotFound, "文件夹不存在")
	}
	return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查询文件夹失败", err)
}

func folderName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" || utf8.RuneCountInString(name) > 120 || strings.ContainsRune(name, 0) {
		return "", apperrors.New(apperrors.CodeBadRequest, "文件夹名称不能为空且不能超过 120 个字符")
	}
	return name, nil
}

func folderResponse(folder *entity.Folder, count int64) responsedto.Folder {
	return responsedto.Folder{
		ID: folder.ID, Name: folder.Name, ClassroomCount: count,
		CreatedAt: folder.CreatedAt, UpdatedAt: folder.UpdatedAt,
	}
}

func folderWriteError(err error, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.New(apperrors.CodeNotFound, "文件夹不存在")
	}
	return apperrors.NewWithErr(apperrors.CodeInternalError, message, err)
}
