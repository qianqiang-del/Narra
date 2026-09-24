package service

import (
	"context"

	requestdto "narra/internal/model/dto/request"
	responsedto "narra/internal/model/dto/response"
)

// ClassroomService 课堂受理与状态查询。
type ClassroomService interface {
	Create(ctx context.Context, input requestdto.CreateClassroom) (*responsedto.Classroom, error)
	List(ctx context.Context) ([]*responsedto.Classroom, error)
	Delete(ctx context.Context, id uint64) error
	Get(ctx context.Context, id uint64) (*responsedto.Classroom, error)
}
