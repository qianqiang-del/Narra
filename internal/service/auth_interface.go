package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
)

type AuthService interface {
	NewCaptcha(ctx context.Context, ip string) (*CaptchaChallenge, error)
	SendCode(ctx context.Context, phone, purpose, ip string, captcha ...CaptchaAnswer) error
	Register(ctx context.Context, phone, password, code string) (*responsedto.AuthResult, error)
	LoginPassword(ctx context.Context, phone, password string, captcha ...CaptchaAnswer) (*responsedto.AuthResult, error)
	LoginCode(ctx context.Context, phone, code string) (*responsedto.AuthResult, error)
	Refresh(ctx context.Context, refreshToken string) (*responsedto.AuthResult, error)
	Logout(ctx context.Context, refreshToken string) error
	GetUser(ctx context.Context, id uint64) (*responsedto.AuthUser, error)
}
