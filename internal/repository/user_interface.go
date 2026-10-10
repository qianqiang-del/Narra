package repository

import (
	"context"

	"narra/internal/model/entity"
)

type UserRepository interface {
	Create(ctx context.Context, user *entity.User) error
	FindByPhone(ctx context.Context, phone string) (*entity.User, error)
	FindByID(ctx context.Context, id uint64) (*entity.User, error)
}
