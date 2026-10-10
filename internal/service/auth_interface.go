package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
)

type AuthService interface {
	SendCode(ctx context.Context, phone, purpose, ip string) error
	Register(ctx context.Context, phone, password, code string) (*responsedto.AuthResult, error)
	LoginPassword(ctx context.Context, phone, password string) (*responsedto.AuthResult, error)
	LoginCode(ctx context.Context, phone, code string) (*responsedto.AuthResult, error)
	GetUser(ctx context.Context, id uint64) (*responsedto.AuthUser, error)
}
