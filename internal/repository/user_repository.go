package repository

import (
	"context"

	"narra/internal/model/entity"

	"gorm.io/gorm"
)

type userRepository struct{ db *gorm.DB }

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, user *entity.User) error {
	return conn(ctx, r.db).Create(user).Error
}

func (r *userRepository) FindByPhone(ctx context.Context, phone string) (*entity.User, error) {
	var user entity.User
	if err := conn(ctx, r.db).Where("phone = ?", phone).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) FindByID(ctx context.Context, id uint64) (*entity.User, error) {
	var user entity.User
	if err := conn(ctx, r.db).First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}
